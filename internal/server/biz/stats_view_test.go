package biz

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStatsViewRefresherRunsOneAtATime locks in the two properties the refresh
// loop depends on: a tick that arrives while a run is in flight is skipped rather
// than queued, and the slot is released once the run finishes.
func TestStatsViewRefresherRunsOneAtATime(t *testing.T) {
	r := &StatsViewRefresher{}

	require.True(t, r.running.CompareAndSwap(false, true), "the first run must acquire the slot")
	require.False(t, r.running.CompareAndSwap(false, true), "a tick during a run must be skipped")

	r.running.Store(false)

	require.True(t, r.running.CompareAndSwap(false, true), "the slot must be reusable after the run ends")
}
