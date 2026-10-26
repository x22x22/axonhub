package orchestrator

import (
	"testing"
	"time"
)

type recordingBypass struct {
	calls int
}

func (r *recordingBypass) RecordStickyAffinityBypass() {
	r.calls++
}

// When the pinned channel is only temporarily unavailable (cooling down after a 429) the
// selector must report a bypass instead of silently returning a fallback. The caller uses
// that signal to keep the original affinity target, so a transient cooldown does not
// permanently migrate the conversation and lose its warm prompt cache.
func TestStickyBypassNotifiesRecorderWhenPinnedChannelIsUnavailable(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	provider.record(1, 1001)

	tracker := NewChannelRequestTracker()
	tracker.SetCooldown(1001, time.Now().Add(time.Minute))

	balancer := NewLoadBalancer(affinityRetryPolicy{}, nil,
		NewWeightStrategy(), NewRandomStrategy(),
		NewRateLimitAwareStrategy(tracker, NewDefaultConnectionTracker(256)))

	recorder := &recordingBypass{}

	selector := WithTraceStickyLoadBalancedSelector(
		staticCandidateSelector{candidates: prod2Candidates(fixture)},
		balancer,
		affinityRetryPolicy{},
		provider,
		WithStickyBypassRecorder(recorder),
	)

	if _, err := selector.Select(prod2TraceContext(1), prod2Request(fixture)); err != nil {
		t.Fatalf("Select: %v", err)
	}

	if recorder.calls != 1 {
		t.Fatalf("bypass recorder called %d times, want 1", recorder.calls)
	}
}

func TestStickyBypassNotReportedOnHit(t *testing.T) {
	fixture := mustLoadProd2Fixture(t)
	provider := newStickyTraceProvider()
	provider.record(1, 1001)

	recorder := &recordingBypass{}

	selector := WithTraceStickyLoadBalancedSelector(
		staticCandidateSelector{candidates: prod2Candidates(fixture)},
		prod2FailoverBalancer(),
		affinityRetryPolicy{},
		provider,
		WithStickyBypassRecorder(recorder),
	)

	if _, err := selector.Select(prod2TraceContext(1), prod2Request(fixture)); err != nil {
		t.Fatalf("Select: %v", err)
	}

	if recorder.calls != 0 {
		t.Fatalf("bypass recorder called %d times on a hit, want 0", recorder.calls)
	}
}
