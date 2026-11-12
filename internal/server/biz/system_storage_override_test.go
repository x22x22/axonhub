package biz

import (
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

// TestStoragePolicyForProject verifies that per-project overrides win over the
// global flags and that a missing override falls back to the global value.
func TestStoragePolicyForProject(t *testing.T) {
	policy := &StoragePolicy{
		StoreRequestBody:  true,
		StoreResponseBody: true,
		ProjectOverrides: map[string]ProjectStorageOverride{
			// Project 8: stop persisting inbound request bodies, keep responses.
			"8": {StoreRequestBody: lo.ToPtr(false)},
			// Project 9: disable both.
			"9": {
				StoreRequestBody:  lo.ToPtr(false),
				StoreResponseBody: lo.ToPtr(false),
			},
			// Project 10: explicit true, overriding nothing but proving the pointer path.
			"10": {StoreRequestBody: lo.ToPtr(true)},
		},
	}

	t.Run("no override falls back to global", func(t *testing.T) {
		require.True(t, policy.StoreRequestBodyFor(1))
		require.True(t, policy.StoreResponseBodyFor(1))
	})

	t.Run("partial override only affects request body", func(t *testing.T) {
		require.False(t, policy.StoreRequestBodyFor(8))
		// response flag has no override for project 8, so the global value applies
		require.True(t, policy.StoreResponseBodyFor(8))
	})

	t.Run("full override disables both", func(t *testing.T) {
		require.False(t, policy.StoreRequestBodyFor(9))
		require.False(t, policy.StoreResponseBodyFor(9))
	})

	t.Run("explicit true override is honoured", func(t *testing.T) {
		require.True(t, policy.StoreRequestBodyFor(10))
	})

	t.Run("nil policy defaults to storing bodies", func(t *testing.T) {
		var nilPolicy *StoragePolicy

		require.True(t, nilPolicy.StoreRequestBodyFor(1))
		require.True(t, nilPolicy.StoreResponseBodyFor(1))
	})

	t.Run("inbound-only override keeps outbound bodies", func(t *testing.T) {
		// Project 8 stops persisting the original client payload but keeps the
		// transformed upstream payload (the "outbound only" mode).
		inboundOff := &StoragePolicy{
			StoreRequestBody:  true,
			StoreResponseBody: true,
			ProjectOverrides: map[string]ProjectStorageOverride{
				"8": {StoreInboundRequestBody: lo.ToPtr(false)},
			},
		}

		require.False(t, inboundOff.StoreInboundRequestBodyFor(8))
		require.True(t, inboundOff.StoreOutboundRequestBodyFor(8))
		// coarse and response flags are untouched
		require.True(t, inboundOff.StoreRequestBodyFor(8))
		require.True(t, inboundOff.StoreResponseBodyFor(8))
	})

	t.Run("outbound-only override keeps inbound bodies", func(t *testing.T) {
		outboundOff := &StoragePolicy{
			StoreRequestBody:  true,
			StoreResponseBody: true,
			ProjectOverrides: map[string]ProjectStorageOverride{
				"3": {StoreOutboundRequestBody: lo.ToPtr(false)},
			},
		}

		require.True(t, outboundOff.StoreInboundRequestBodyFor(3))
		require.False(t, outboundOff.StoreOutboundRequestBodyFor(3))
	})

	t.Run("directional override wins over coarse switch", func(t *testing.T) {
		mixed := &StoragePolicy{
			StoreRequestBody:  false,
			StoreResponseBody: true,
			ProjectOverrides: map[string]ProjectStorageOverride{
				// coarse off, but explicitly re-enable outbound only
				"5": {
					StoreRequestBody:         lo.ToPtr(false),
					StoreOutboundRequestBody: lo.ToPtr(true),
				},
			},
		}

		require.False(t, mixed.StoreInboundRequestBodyFor(5))
		require.True(t, mixed.StoreOutboundRequestBodyFor(5))
	})

	t.Run("directional override falls back to coarse then global", func(t *testing.T) {
		coarse := &StoragePolicy{
			StoreRequestBody:  true,
			StoreResponseBody: true,
			ProjectOverrides: map[string]ProjectStorageOverride{
				"6": {StoreRequestBody: lo.ToPtr(false)},
			},
		}

		// no directional override -> coarse applies to both directions
		require.False(t, coarse.StoreInboundRequestBodyFor(6))
		require.False(t, coarse.StoreOutboundRequestBodyFor(6))
		// unlisted project -> global
		require.True(t, coarse.StoreInboundRequestBodyFor(7))
	})

	t.Run("global false with per-project true override", func(t *testing.T) {
		globalOff := &StoragePolicy{
			StoreRequestBody:  false,
			StoreResponseBody: false,
			ProjectOverrides: map[string]ProjectStorageOverride{
				"3": {StoreRequestBody: lo.ToPtr(true)},
			},
		}

		require.True(t, globalOff.StoreRequestBodyFor(3))
		require.False(t, globalOff.StoreResponseBodyFor(3))
		require.False(t, globalOff.StoreRequestBodyFor(4))
	})
}
