package orchestrator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
)

// affinityRetryPolicy disables channel retries so Sort only keeps the top candidate.
type affinityRetryPolicy struct{}

func (affinityRetryPolicy) RetryPolicyOrDefault(context.Context) *biz.RetryPolicy {
	return &biz.RetryPolicy{Enabled: false}
}

// affinityMetricsProvider reports empty metrics, which is the worst case for trace
// affinity: every other channel scores its maximum and only the trace bonus can win.
type affinityMetricsProvider struct{}

func (affinityMetricsProvider) GetChannelMetrics(context.Context, int) (*biz.AggregatedMetrics, error) {
	return &biz.AggregatedMetrics{}, nil
}

// affinityTraceProvider reports a fixed last successful channel for every trace.
type affinityTraceProvider struct {
	channelID int
}

func (p affinityTraceProvider) GetLastSuccessfulChannelID(context.Context, int) (int, error) {
	return p.channelID, nil
}

func newAffinityCandidates(n int) []*ChannelModelsCandidate {
	candidates := make([]*ChannelModelsCandidate, 0, n)
	for i := 1; i <= n; i++ {
		candidates = append(candidates, &ChannelModelsCandidate{
			Channel: &biz.Channel{Channel: &ent.Channel{
				ID:             i,
				Name:           fmt.Sprintf("channel-%d", i),
				OrderingWeight: 100,
			}},
			Priority: 0,
		})
	}

	return candidates
}

func newTestLoadBalancers(t *testing.T) map[string]*LoadBalancer {
	t.Helper()

	return newTestLoadBalancersWithTracker(t, NewChannelRequestTracker())
}

func newTestLoadBalancersWithTracker(t *testing.T, tracker *ChannelRequestTracker) map[string]*LoadBalancer {
	t.Helper()

	adaptive, failover, circuitBreaker := newLoadBalancers(
		affinityRetryPolicy{},
		nil,
		affinityMetricsProvider{},
		affinityTraceProvider{channelID: 7},
		biz.NewModelCircuitBreaker(),
		NewRateLimitAwareStrategy(tracker, NewDefaultConnectionTracker(256)),
	)

	return map[string]*LoadBalancer{
		"adaptive":        adaptive,
		"failover":        failover,
		"circuit-breaker": circuitBreaker,
	}
}

// Every strategy set must keep the trace aware strategy. Without it a per-API-key
// profile override (failover / circuit-breaker) makes each request pick a random
// channel, which throws away the upstream prompt cache on every conversation turn.
func TestLoadBalancerStrategySetsContainTraceAware(t *testing.T) {
	for name, balancer := range newTestLoadBalancers(t) {
		t.Run(name, func(t *testing.T) {
			found := false

			for _, strategy := range balancer.strategies {
				if strategy.Name() == "TraceAware" {
					found = true

					break
				}
			}

			if !found {
				t.Fatalf("%s load balancer is missing TraceAwareStrategy", name)
			}
		})
	}
}

// With 48 equally weighted channels, only the trace bonus can keep the request on the
// channel that already served this trace.
func TestLoadBalancersKeepTraceAffinity(t *testing.T) {
	ctx := contexts.WithTrace(context.Background(), &ent.Trace{ID: 42})
	candidates := newAffinityCandidates(48)

	for name, balancer := range newTestLoadBalancers(t) {
		t.Run(name, func(t *testing.T) {
			sorted := balancer.Sort(ctx, candidates, "ais-prod/glm-5.3", true)
			if len(sorted) == 0 {
				t.Fatalf("%s load balancer returned no candidates", name)
			}

			if got := sorted[0].Channel.ID; got != 7 {
				t.Fatalf("%s load balancer selected channel %d, want the trace channel 7", name, got)
			}
		})
	}
}

// Trace affinity must never resurrect a channel that is cooling down after a 429:
// availability still wins over cache reuse.
func TestLoadBalancersRespectCooldownOverTraceAffinity(t *testing.T) {
	tracker := NewChannelRequestTracker()
	tracker.SetCooldown(7, time.Now().Add(time.Minute))

	ctx := contexts.WithTrace(context.Background(), &ent.Trace{ID: 42})
	candidates := newAffinityCandidates(48)

	for name, balancer := range newTestLoadBalancersWithTracker(t, tracker) {
		t.Run(name, func(t *testing.T) {
			sorted := balancer.Sort(ctx, candidates, "ais-prod/glm-5.3", true)
			if len(sorted) == 0 {
				t.Fatalf("%s load balancer returned no candidates", name)
			}

			if sorted[0].Channel.ID == 7 {
				t.Fatalf("%s load balancer kept the cooling channel 7", name)
			}
		})
	}
}
