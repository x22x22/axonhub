package gql

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCachedDashboardStatsReusesResult locks in the point of the cache: the
// dashboard resolvers each run a full-table aggregation, so a repeated load
// inside the TTL must not touch the database again.
func TestCachedDashboardStatsReusesResult(t *testing.T) {
	var calls int32

	load := func(context.Context) (string, error) {
		atomic.AddInt32(&calls, 1)

		return "payload", nil
	}

	first, err := cachedDashboardStats(context.Background(), "unit-reuse", load)
	require.NoError(t, err)
	require.Equal(t, "payload", first)

	second, err := cachedDashboardStats(context.Background(), "unit-reuse", load)
	require.NoError(t, err)
	require.Equal(t, "payload", second)

	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "second load must be served from the cache")
}

// TestCachedDashboardStatsIsolatesKeys makes sure two different arguments do not
// share one entry.
func TestCachedDashboardStatsIsolatesKeys(t *testing.T) {
	var calls int32

	load := func(_ context.Context) (string, error) {
		atomic.AddInt32(&calls, 1)

		return "shared", nil
	}

	_, err := cachedDashboardStats(context.Background(), "unit-key-a", load)
	require.NoError(t, err)

	_, err = cachedDashboardStats(context.Background(), "unit-key-b", load)
	require.NoError(t, err)

	require.Equal(t, int32(2), atomic.LoadInt32(&calls), "different keys must load independently")
}

// TestCachedDashboardStatsDoesNotCacheErrors keeps a transient database failure
// from being pinned for the whole TTL.
func TestCachedDashboardStatsDoesNotCacheErrors(t *testing.T) {
	var calls int32

	boom := errors.New("db down")

	load := func(context.Context) (string, error) {
		atomic.AddInt32(&calls, 1)

		return "", boom
	}

	_, err := cachedDashboardStats(context.Background(), "unit-error", load)
	require.ErrorIs(t, err, boom)

	_, err = cachedDashboardStats(context.Background(), "unit-error", load)
	require.ErrorIs(t, err, boom)

	require.Equal(t, int32(2), atomic.LoadInt32(&calls), "errors must not be cached")
}

// TestCachedDashboardStatsExpires guards the TTL: after it elapses the loader runs
// again so the dashboard does not show stale numbers forever.
func TestCachedDashboardStatsExpires(t *testing.T) {
	var calls int32

	load := func(context.Context) (string, error) {
		atomic.AddInt32(&calls, 1)

		return "payload", nil
	}

	_, err := cachedDashboardStats(context.Background(), "unit-expire", load)
	require.NoError(t, err)

	// Move the entry's expiry into the past instead of sleeping 60s.
	dashboardStatsCache.mu.Lock()
	e := dashboardStatsCache.entries["unit-expire"]
	e.expires = time.Now().Add(-time.Second)
	dashboardStatsCache.entries["unit-expire"] = e
	dashboardStatsCache.mu.Unlock()

	_, err = cachedDashboardStats(context.Background(), "unit-expire", load)
	require.NoError(t, err)

	require.Equal(t, int32(2), atomic.LoadInt32(&calls), "an expired entry must be reloaded")
}
