package biz

import "testing"

func TestNormalizeTraceStickyMode(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: TraceStickyPreferPreviousChannel},
		{in: "   ", want: TraceStickyPreferPreviousChannel},
		{in: TraceStickyPreferPreviousChannel, want: TraceStickyPreferPreviousChannel},
		{in: "PREFER_PREVIOUS_CHANNEL", want: TraceStickyPreferPreviousChannel},
		{in: " prefer_previous_channel ", want: TraceStickyPreferPreviousChannel},
		{in: "prefer-previous-channel", want: TraceStickyPreferPreviousChannel},
		{in: TraceStickyDisabled, want: TraceStickyDisabled},
		{in: "DISABLED", want: TraceStickyDisabled},
		{in: "off", want: TraceStickyDisabled},
		{in: "none", want: TraceStickyDisabled},
		// Unknown values must not silently disable affinity.
		{in: "always", want: TraceStickyPreferPreviousChannel},
		{in: "bogus", want: TraceStickyPreferPreviousChannel},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := NormalizeTraceStickyMode(tt.in); got != tt.want {
				t.Fatalf("NormalizeTraceStickyMode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDefaultRetryPolicyPrefersPreviousChannel(t *testing.T) {
	if defaultRetryPolicy.TraceStickyMode != TraceStickyPreferPreviousChannel {
		t.Fatalf("default trace sticky mode = %q, want %q",
			defaultRetryPolicy.TraceStickyMode, TraceStickyPreferPreviousChannel)
	}
}

// A retry policy persisted before this field existed has no value; normalisation must fill
// the default instead of leaving affinity disabled by accident.
func TestNormalizeRetryPolicyFillsTraceStickyMode(t *testing.T) {
	policy := &RetryPolicy{}
	normalizeRetryPolicy(policy)

	if policy.TraceStickyMode != TraceStickyPreferPreviousChannel {
		t.Fatalf("normalized trace sticky mode = %q, want %q",
			policy.TraceStickyMode, TraceStickyPreferPreviousChannel)
	}
}

func TestNormalizeRetryPolicyKeepsDisabled(t *testing.T) {
	policy := &RetryPolicy{TraceStickyMode: "  DISABLED "}
	normalizeRetryPolicy(policy)

	if policy.TraceStickyMode != TraceStickyDisabled {
		t.Fatalf("normalized trace sticky mode = %q, want %q", policy.TraceStickyMode, TraceStickyDisabled)
	}
}

func TestNormalizeRetryPolicyKeepsLoadBalancerStrategy(t *testing.T) {
	policy := &RetryPolicy{LoadBalancerStrategy: "weighted"}
	normalizeRetryPolicy(policy)

	if policy.LoadBalancerStrategy != LoadBalancerStrategyFailover {
		t.Fatalf("load balancer strategy = %q, want %q", policy.LoadBalancerStrategy, LoadBalancerStrategyFailover)
	}
}
