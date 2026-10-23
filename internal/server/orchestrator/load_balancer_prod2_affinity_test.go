package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

// prod2Fixture mirrors a real AIS agent request captured from prod2 (testdata fixture):
// a codex_cli_rs/opencode client sends every header value as a JSON array string and
// pins the conversation with X-Session-Affinity.
type prod2Fixture struct {
	Headers map[string]string `json:"headers"`
	Body    struct {
		Model        string `json:"model"`
		Stream       bool   `json:"stream"`
		MessageCount int    `json:"message_count"`
		ToolCount    int    `json:"tool_count"`
		BodyBytes    int64  `json:"body_bytes"`
	} `json:"body"`
	ChannelPool struct {
		Count          int `json:"count"`
		OrderingWeight int `json:"ordering_weight"`
	} `json:"channel_pool"`
	ObservedTurns struct {
		Turns            int `json:"turns"`
		DistinctChannels int `json:"distinct_channels"`
		ChannelPoolSize  int `json:"channel_pool_size"`
	} `json:"observed_turns"`
}

func loadProd2Fixture() (prod2Fixture, error) {
	var fixture prod2Fixture

	raw, err := os.ReadFile(filepath.Join("testdata", "ais_prod2_session_request.json"))
	if err != nil {
		return fixture, err
	}

	if err := json.Unmarshal(raw, &fixture); err != nil {
		return fixture, err
	}

	return fixture, nil
}

func mustLoadProd2Fixture(t *testing.T) prod2Fixture {
	t.Helper()

	fixture, err := loadProd2Fixture()
	if err != nil {
		t.Fatalf("load prod2 fixture: %v", err)
	}

	return fixture
}

// prod2Candidates builds the channel pool of the captured request: 48 accounts of the
// same model with an identical ordering weight, so only the random tiebreaker or the
// conversation affinity can tell them apart.
func prod2Candidates(fixture prod2Fixture) []*ChannelModelsCandidate {
	candidates := make([]*ChannelModelsCandidate, 0, fixture.ChannelPool.Count)
	for i := 1; i <= fixture.ChannelPool.Count; i++ {
		candidates = append(candidates, &ChannelModelsCandidate{
			Channel: &biz.Channel{Channel: &ent.Channel{
				ID:             1000 + i,
				Name:           fmt.Sprintf("zai-coding-pool-%02d", i),
				OrderingWeight: fixture.ChannelPool.OrderingWeight,
			}},
			Priority: 0,
		})
	}

	return candidates
}

func prod2Request(fixture prod2Fixture) *llm.Request {
	stream := fixture.Body.Stream

	return &llm.Request{Model: fixture.Body.Model, Stream: &stream}
}

func prod2RateLimitStrategy() LoadBalanceStrategy {
	return NewRateLimitAwareStrategy(NewChannelRequestTracker(), NewDefaultConnectionTracker(256))
}

// prod2FailoverBalancer is the strategy set used by the API-key profile that reproduced the
// incident: weight + random tiebreak + rate limit, and no trace-aware strategy at all.
func prod2FailoverBalancer() *LoadBalancer {
	return NewLoadBalancer(affinityRetryPolicy{}, nil,
		NewWeightStrategy(), NewRandomStrategy(), prod2RateLimitStrategy())
}

func prod2AdaptiveBalancer(provider ChannelTraceProvider) *LoadBalancer {
	return NewLoadBalancer(affinityRetryPolicy{}, nil,
		NewTraceAwareStrategy(provider),
		NewErrorAwareStrategy(affinityMetricsProvider{}),
		NewWeightRoundRobinStrategy(affinityMetricsProvider{}),
		NewLatencyAwareStrategy(affinityMetricsProvider{}),
		prod2RateLimitStrategy())
}

// staticCandidateSelector returns a fixed candidate set, standing in for the model
// association lookup performed by DefaultSelector.
type staticCandidateSelector struct {
	candidates []*ChannelModelsCandidate
}

func (s staticCandidateSelector) Select(context.Context, *llm.Request) ([]*ChannelModelsCandidate, error) {
	return s.candidates, nil
}

func prod2StickySelector(fixture prod2Fixture, balancer *LoadBalancer, policy RetryPolicyProvider, provider PreviousChannelProvider) CandidateSelector {
	return WithTraceStickyLoadBalancedSelector(
		staticCandidateSelector{candidates: prod2Candidates(fixture)},
		balancer,
		policy,
		provider,
	)
}

// stickyTraceProvider emulates RequestService.GetPreviousChannelID backed by the
// previous-channel cache (30 minute TTL) and counts lookups, so tests can prove how many
// lookups a selection costs.
type stickyTraceProvider struct {
	mu    sync.Mutex
	last  map[int]int
	calls atomic.Int64
}

func newStickyTraceProvider() *stickyTraceProvider {
	return &stickyTraceProvider{last: make(map[int]int)}
}

func (p *stickyTraceProvider) GetPreviousChannelID(_ context.Context, traceID int) (int, error) {
	p.calls.Add(1)

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.last[traceID], nil
}

func (p *stickyTraceProvider) GetPreviousChannelIDByThread(_ context.Context, threadID int) (int, error) {
	p.calls.Add(1)

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.last[-threadID], nil
}

// record emulates UpdateRequestChannelID persisting the selected channel into the cache as
// soon as a channel is chosen.
func (p *stickyTraceProvider) record(traceID, channelID int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.last[traceID] = channelID
}

func (p *stickyTraceProvider) resetCalls() {
	p.calls.Store(0)
}

// countingStrategy counts how often the load balancer scores a channel, which is how the
// tests observe whether the affinity path skipped balancing entirely.
type countingStrategy struct {
	calls atomic.Int64
}

func (s *countingStrategy) Score(context.Context, *biz.Channel) float64 {
	s.calls.Add(1)

	return 100
}

func (s *countingStrategy) ScoreWithDebug(ctx context.Context, channel *biz.Channel) (float64, StrategyScore) {
	return s.Score(ctx, channel), StrategyScore{StrategyName: s.Name(), Score: 100}
}

func (s *countingStrategy) Name() string {
	return "Counting"
}

func prod2TraceContext(traceID int) context.Context {
	return contexts.WithTrace(context.Background(), &ent.Trace{ID: traceID})
}

// The captured session (fsd-pd0253-d5c1ec5d) walked 35 channels over 59 turns while the
// gateway ran without conversation affinity. Through the real selector path every turn
// after the first must stay on the same channel, even though the strategy set itself has
// no trace-aware strategy.
func TestProd2SessionStaysOnOneChannelThroughSelector(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	selector := prod2StickySelector(fixture, prod2FailoverBalancer(), affinityRetryPolicy{}, provider)
	ctx := prod2TraceContext(42)
	req := prod2Request(fixture)

	var first int

	for turn := 0; turn < fixture.ObservedTurns.Turns; turn++ {
		result, err := selector.Select(ctx, req)
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}

		if len(result) == 0 {
			t.Fatalf("turn %d: no candidates", turn)
		}

		selected := result[0].Channel.ID
		if turn == 0 {
			first = selected
		} else if selected != first {
			t.Fatalf("turn %d moved to channel %d, want %d (conversation must stay on one channel)", turn, selected, first)
		}

		// The request middleware writes the chosen channel right after selection, which is
		// what the next turn reads back.
		provider.record(42, selected)
	}
}

// A single-selection hit must not rank the whole pool: one cache lookup plus one scoring
// pass over the pinned channel (the availability check), instead of scoring all 48.
func TestStickySelectorScoresOnlyPinnedChannel(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	provider.record(42, 1007)

	counting := &countingStrategy{}
	balancer := NewLoadBalancer(affinityRetryPolicy{}, nil, counting, prod2RateLimitStrategy())
	selector := prod2StickySelector(fixture, balancer, affinityRetryPolicy{}, provider)
	ctx := prod2TraceContext(42)

	provider.resetCalls()

	result, err := selector.Select(ctx, prod2Request(fixture))
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	if len(result) == 0 || result[0].Channel.ID != 1007 {
		t.Fatalf("pinned channel = %v, want 1007", result)
	}

	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("previous channel lookups = %d, want exactly 1 per selection", got)
	}

	if got := counting.calls.Load(); got != 1 {
		t.Fatalf("channel scoring calls = %d, want 1 (the pinned channel availability check)", got)
	}
}

// Red test for the pre-fix behaviour: the same 59 turns scatter across the 48 channel pool
// when the selector does no affinity and the strategy set has no trace-aware strategy.
// The captured production session used 35 distinct channels, close to the uniform-random
// expectation of 48*(1-e^(-59/48)) ~= 33.9.
func TestProd2SessionDriftsWithoutAffinity(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	selector := WithLoadBalancedSelector(
		staticCandidateSelector{candidates: prod2Candidates(fixture)},
		prod2FailoverBalancer(),
		affinityRetryPolicy{},
	)
	ctx := prod2TraceContext(42)
	req := prod2Request(fixture)
	distinct := make(map[int]struct{})

	for turn := 0; turn < fixture.ObservedTurns.Turns; turn++ {
		result, err := selector.Select(ctx, req)
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}

		distinct[result[0].Channel.ID] = struct{}{}
	}

	if len(distinct) <= 10 {
		t.Fatalf("without affinity the session used only %d channels, expected the drift observed in production", len(distinct))
	}

	t.Logf("without affinity: %d distinct channels over %d turns", len(distinct), fixture.ObservedTurns.Turns)
}

// Affinity must yield to availability: a channel cooling down after a 429 is not pinned.
func TestStickySelectorYieldsToCoolingChannel(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	provider.record(42, 1007)

	tracker := NewChannelRequestTracker()
	tracker.SetCooldown(1007, time.Now().Add(time.Minute))

	balancer := NewLoadBalancer(affinityRetryPolicy{}, nil,
		NewWeightStrategy(), NewRandomStrategy(),
		NewRateLimitAwareStrategy(tracker, NewDefaultConnectionTracker(256)))
	selector := prod2StickySelector(fixture, balancer, affinityRetryPolicy{}, provider)

	result, err := selector.Select(prod2TraceContext(42), prod2Request(fixture))
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("no candidates")
	}

	if result[0].Channel.ID == 1007 {
		t.Fatal("pinned a channel that is cooling down")
	}
}

// The previous channel may have left the pool (archived, filtered by a profile). Selection
// must fall back instead of returning nothing.
func TestStickySelectorFallsBackWhenPreviousChannelLeftPool(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	provider.record(42, 999999) // not in the pool
	selector := prod2StickySelector(fixture, prod2FailoverBalancer(), affinityRetryPolicy{}, provider)

	result, err := selector.Select(prod2TraceContext(42), prod2Request(fixture))
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("no candidates when the previous channel is gone")
	}

	if result[0].Channel.ID == 999999 {
		t.Fatal("selected a channel that is not in the candidate pool")
	}
}

// A request without a trace (non-opencode client, failed trace middleware) must not fail.
func TestStickySelectorWithoutTraceContext(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	selector := prod2StickySelector(fixture, prod2FailoverBalancer(), affinityRetryPolicy{}, provider)

	result, err := selector.Select(context.Background(), prod2Request(fixture))
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("no candidates without trace context")
	}
}

// With channel retries enabled the pinned channel stays first and the rest of the retry
// budget is filled from normal load balancing.
func TestStickySelectorWithRetryKeepsPinnedChannelFirst(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	provider.record(42, 1007)

	policy := retryEnabledPolicy{maxChannelRetries: 2}
	// The balancer and the selector must agree on the retry budget, otherwise the fallback
	// fill uses a different top-K than the selector asks for.
	balancer := NewLoadBalancer(policy, nil, NewWeightStrategy(), NewRandomStrategy(), prod2RateLimitStrategy())
	selector := prod2StickySelector(fixture, balancer, policy, provider)

	result, err := selector.Select(prod2TraceContext(42), prod2Request(fixture))
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	if len(result) != 3 {
		t.Fatalf("retry selection returned %d candidates, want 3", len(result))
	}

	if result[0].Channel.ID != 1007 {
		t.Fatalf("first candidate = %d, want the pinned channel 1007", result[0].Channel.ID)
	}

	if result[1].Channel.ID == 1007 || result[2].Channel.ID == 1007 {
		t.Fatal("pinned channel must not occupy a fallback slot")
	}
}

// Concurrent selections share the provider; run under -race in CI.
func TestStickySelectorConcurrentIsRaceFree(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	selector := prod2StickySelector(fixture, prod2FailoverBalancer(), affinityRetryPolicy{}, provider)
	req := prod2Request(fixture)

	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)

		go func(id int) {
			defer wg.Done()

			ctx := prod2TraceContext(id)
			provider.record(id, 1000+id)

			for j := 0; j < 20; j++ {
				result, err := selector.Select(ctx, req)
				if err != nil || len(result) == 0 {
					t.Errorf("goroutine %d: select failed: %v", id, err)

					return
				}
			}
		}(i)
	}

	wg.Wait()
}

func BenchmarkProd2StickySelectorHit(b *testing.B) {
	fixture, err := loadProd2Fixture()
	if err != nil {
		b.Fatal(err)
	}

	provider := newStickyTraceProvider()
	provider.record(42, 1007)
	selector := prod2StickySelector(fixture, prod2FailoverBalancer(), affinityRetryPolicy{}, provider)
	req := prod2Request(fixture)
	ctx := prod2TraceContext(42)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		selector.Select(ctx, req)
	}
}

func BenchmarkProd2SelectionWithoutAffinity(b *testing.B) {
	fixture, err := loadProd2Fixture()
	if err != nil {
		b.Fatal(err)
	}

	selector := WithLoadBalancedSelector(
		staticCandidateSelector{candidates: prod2Candidates(fixture)},
		prod2FailoverBalancer(),
		affinityRetryPolicy{},
	)
	req := prod2Request(fixture)
	ctx := prod2TraceContext(42)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		selector.Select(ctx, req)
	}
}

func BenchmarkProd2StickySelectorMiss(b *testing.B) {
	fixture, err := loadProd2Fixture()
	if err != nil {
		b.Fatal(err)
	}

	provider := newStickyTraceProvider()
	selector := prod2StickySelector(fixture, prod2FailoverBalancer(), affinityRetryPolicy{}, provider)
	req := prod2Request(fixture)
	ctx := prod2TraceContext(42)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		selector.Select(ctx, req)
	}
}

// TraceStickyMode=disabled must skip the affinity path entirely: no previous-channel
// lookup, and independent balancing on every turn.
type stickyDisabledPolicy struct{}

func (stickyDisabledPolicy) RetryPolicyOrDefault(context.Context) *biz.RetryPolicy {
	return &biz.RetryPolicy{Enabled: false, TraceStickyMode: biz.TraceStickyDisabled}
}

func TestStickySelectorDisabledModeBalancesIndependently(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	provider.record(42, 1007)

	selector := prod2StickySelector(fixture, prod2FailoverBalancer(), stickyDisabledPolicy{}, provider)
	ctx := prod2TraceContext(42)
	req := prod2Request(fixture)

	provider.resetCalls()

	distinct := make(map[int]struct{})

	for turn := 0; turn < fixture.ObservedTurns.Turns; turn++ {
		result, err := selector.Select(ctx, req)
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}

		distinct[result[0].Channel.ID] = struct{}{}
	}

	if got := provider.calls.Load(); got != 0 {
		t.Fatalf("previous channel lookups = %d, want 0 when trace sticky mode is disabled", got)
	}

	if len(distinct) <= 1 {
		t.Fatalf("disabled mode still pinned one channel (%d distinct), want independent balancing", len(distinct))
	}
}
