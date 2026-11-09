package orchestrator

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStrategyLogGateIsPerKey locks in the fix for a shared-state bug: the load
// balancing strategy is resolved per API key, so the "last logged" bookkeeping must
// be per key too. With a single shared slot, two keys on different strategies
// alternated the recorded value and every request was reported as a transition,
// which is what the production gateway was logging.
func TestStrategyLogGateIsPerKey(t *testing.T) {
	var store sync.Map

	prev, changed := strategyLogGate(&store, 1, "adaptive")
	require.True(t, changed, "first observation for a key is a transition")
	require.Empty(t, prev)

	_, changed = strategyLogGate(&store, 1, "adaptive")
	require.False(t, changed, "same key and same strategy must not log again")

	// A second key on a different strategy must not disturb key 1.
	prev, changed = strategyLogGate(&store, 2, "failover")
	require.True(t, changed)
	require.Equal(t, "", prev)

	// This is the production bug: with a shared slot, key 1 would look changed
	// again after key 2 was recorded.
	_, changed = strategyLogGate(&store, 1, "adaptive")
	require.False(t, changed, "another key's strategy must not make this key look changed")

	// A real change for the same key reports the previous value.
	prev, changed = strategyLogGate(&store, 2, "adaptive")
	require.True(t, changed)
	require.Equal(t, "failover", prev)

	require.False(t, func() bool {
		_, c := strategyLogGate(&store, 2, "adaptive")
		return c
	}(), "the transition is recorded, so the next identical call is quiet")
}
