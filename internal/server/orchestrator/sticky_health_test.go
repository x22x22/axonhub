package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/server/biz"
)

// failingMetricsProvider reports a fixed number of consecutive failures for every channel,
// with a failure timestamp of "now" so ErrorAware applies its full penalty.
type failingMetricsProvider struct {
	failures int64
}

func (p failingMetricsProvider) GetChannelMetrics(context.Context, int) (*biz.AggregatedMetrics, error) {
	now := time.Now()
	metrics := &biz.AggregatedMetrics{LastFailureAt: &now}
	metrics.ConsecutiveFailures = p.failures

	return metrics, nil
}

// A channel that keeps failing must not trap a conversation: affinity has to yield to a
// healthy channel. Without this guard the "remember the first attempt channel" fix could
// pin a conversation on a channel that answers 5xx/timeout on every attempt.
func TestStickyPinYieldsWhenPreviousChannelKeepsFailing(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	provider.record(1, 1001)

	balancer := NewLoadBalancer(affinityRetryPolicy{}, nil,
		NewErrorAwareStrategy(failingMetricsProvider{failures: 2}),
		NewWeightStrategy(), prod2RateLimitStrategy())

	selector := prod2StickySelector(fixture, balancer, affinityRetryPolicy{}, provider)

	candidates, err := selector.Select(prod2TraceContext(1), prod2Request(fixture))
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	if len(candidates) == 0 || candidates[0].Channel.ID == 1001 {
		t.Fatalf("expected affinity to yield for a channel with 2 consecutive failures, got %d first", candidates[0].Channel.ID)
	}

	assertStickyOutcome(t, stickyStatsOf(t, selector), StickyRejectedUnavailable)
}

// A single failure (for example an intermittent 429 followed by a success, which resets
// ConsecutiveFailures) must NOT drop the affinity: the prompt cache is still warm.
func TestStickyPinKeptWhenPreviousChannelIsHealthy(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	provider.record(1, 1001)

	balancer := NewLoadBalancer(affinityRetryPolicy{}, nil,
		NewErrorAwareStrategy(failingMetricsProvider{failures: 0}),
		NewWeightStrategy(), prod2RateLimitStrategy())

	selector := prod2StickySelector(fixture, balancer, affinityRetryPolicy{}, provider)

	candidates, err := selector.Select(prod2TraceContext(1), prod2Request(fixture))
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	if candidates[0].Channel.ID != 1001 {
		t.Fatalf("expected affinity to stay on 1001, got %d first", candidates[0].Channel.ID)
	}

	assertStickyOutcome(t, stickyStatsOf(t, selector), StickyHit)
}
