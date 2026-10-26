package orchestrator

import (
	"testing"
	"time"
)

// stickyStatsOf exposes the affinity counters of a selector for assertions.
func stickyStatsOf(t *testing.T, selector CandidateSelector) map[StickyOutcome]int64 {
	t.Helper()

	lb, ok := selector.(*LoadBalancedSelector)
	if !ok {
		t.Fatalf("selector is %T, want *LoadBalancedSelector", selector)
	}

	if lb.stats == nil {
		t.Fatal("selector has no sticky stats")
	}

	return lb.stats.snapshot()
}

func assertStickyOutcome(t *testing.T, stats map[StickyOutcome]int64, want StickyOutcome) {
	t.Helper()

	if stats[want] != 1 {
		t.Fatalf("%s = %d, want 1 (all outcomes: %v)", want, stats[want], stats)
	}

	for _, other := range []StickyOutcome{StickyHit, StickyMissNoCache, StickyMissNotCandidate, StickyRejectedUnavailable} {
		if other != want && stats[other] != 0 {
			t.Fatalf("%s = %d, want 0 when %s happened (all outcomes: %v)", other, stats[other], want, stats)
		}
	}
}

func TestStickyStatsRecordsHit(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	provider.record(1, 1001)

	selector := prod2StickySelector(fixture, prod2FailoverBalancer(), affinityRetryPolicy{}, provider)

	if _, err := selector.Select(prod2TraceContext(1), prod2Request(fixture)); err != nil {
		t.Fatalf("Select: %v", err)
	}

	assertStickyOutcome(t, stickyStatsOf(t, selector), StickyHit)
}

func TestStickyStatsRecordsMissNoCache(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()

	selector := prod2StickySelector(fixture, prod2FailoverBalancer(), affinityRetryPolicy{}, provider)

	if _, err := selector.Select(prod2TraceContext(1), prod2Request(fixture)); err != nil {
		t.Fatalf("Select: %v", err)
	}

	assertStickyOutcome(t, stickyStatsOf(t, selector), StickyMissNoCache)
}

func TestStickyStatsRecordsMissNotCandidate(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	// A channel that is no longer in the candidate pool: archived or filtered out.
	provider.record(1, 99999)

	selector := prod2StickySelector(fixture, prod2FailoverBalancer(), affinityRetryPolicy{}, provider)

	if _, err := selector.Select(prod2TraceContext(1), prod2Request(fixture)); err != nil {
		t.Fatalf("Select: %v", err)
	}

	assertStickyOutcome(t, stickyStatsOf(t, selector), StickyMissNotCandidate)
}

func TestStickyStatsRecordsRejectedUnavailable(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	provider.record(1, 1001)

	// The pinned channel is cooling down after a 429: affinity must yield, and that yield
	// has to be visible in the counters (otherwise a falling hit rate looks like a bug).
	tracker := NewChannelRequestTracker()
	tracker.SetCooldown(1001, time.Now().Add(time.Minute))

	balancer := NewLoadBalancer(affinityRetryPolicy{}, nil,
		NewWeightStrategy(), NewRandomStrategy(),
		NewRateLimitAwareStrategy(tracker, NewDefaultConnectionTracker(256)))

	selector := prod2StickySelector(fixture, balancer, affinityRetryPolicy{}, provider)

	if _, err := selector.Select(prod2TraceContext(1), prod2Request(fixture)); err != nil {
		t.Fatalf("Select: %v", err)
	}

	assertStickyOutcome(t, stickyStatsOf(t, selector), StickyRejectedUnavailable)
}
