package biz

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStoragePolicyDecodeLegacyAndOverrides pins the backward-compatibility
// behaviour of the policy decoder. Review caught a regression where key-substring
// probing made a nested override look like a global flag and disabled body storage
// for every project.
func TestStoragePolicyDecodeLegacyAndOverrides(t *testing.T) {
	t.Run("legacy document without body flags keeps them enabled", func(t *testing.T) {
		// This is what older versions stored: no store_request_body / store_response_body.
		value := `{"store_chunks":false,"live_preview":false,"cleanup_options":[{"resource_type":"requests","enabled":true,"cleanup_days":3}]}`

		policy := StoragePolicy{
			StoreRequestBody:  true,
			StoreResponseBody: true,
		}
		require.NoError(t, json.Unmarshal([]byte(value), &policy))

		require.True(t, policy.StoreRequestBody, "legacy document must default to storing request bodies")
		require.True(t, policy.StoreResponseBody, "legacy document must default to storing response bodies")
	})

	t.Run("nested override must not be mistaken for the global flag", func(t *testing.T) {
		// Regression: the old decoder scanned the raw JSON for the literal
		// "store_request_body". A document that only carries it nested inside
		// project_overrides used to leave the global flag at its zero value.
		value := `{"store_chunks":false,"live_preview":false,"cleanup_options":[],"project_overrides":{"8":{"store_request_body":false}}}`

		policy := StoragePolicy{
			StoreRequestBody:  true,
			StoreResponseBody: true,
		}
		require.NoError(t, json.Unmarshal([]byte(value), &policy))

		require.True(t, policy.StoreRequestBody, "the global flag was absent, so it must stay true")
		require.True(t, policy.StoreResponseBody)

		// project 8 opted out through the coarse override...
		require.False(t, policy.StoreRequestBodyFor(8))
		// ...while unrelated projects keep the global behaviour.
		require.True(t, policy.StoreRequestBodyFor(1))
	})

	t.Run("production-shaped policy from the apply script decodes as intended", func(t *testing.T) {
		// This is the exact document apply-storage-policy.sh writes.
		value := `{"store_chunks":false,"live_preview":false,"store_request_body":true,` +
			`"store_response_body":true,` +
			`"project_overrides":{"8":{"store_inbound_request_body":false}},` +
			`"cleanup_options":[` +
			`{"resource_type":"requests","enabled":true,"cleanup_days":3},` +
			`{"resource_type":"usage_logs","enabled":true,"cleanup_days":30},` +
			`{"resource_type":"request_bodies","enabled":true,"cleanup_days":2},` +
			`{"resource_type":"response_bodies","enabled":true,"cleanup_days":2},` +
			`{"resource_type":"response_chunks","enabled":false,"cleanup_days":3}]}`

		policy := StoragePolicy{
			StoreRequestBody:  true,
			StoreResponseBody: true,
		}
		require.NoError(t, json.Unmarshal([]byte(value), &policy))

		// Global flags untouched: request and response bodies still stored widely.
		require.True(t, policy.StoreRequestBody)
		require.True(t, policy.StoreResponseBody)

		// Project 8: inbound off, outbound on -> "outbound only" mode.
		require.False(t, policy.StoreInboundRequestBodyFor(8))
		require.True(t, policy.StoreOutboundRequestBodyFor(8))
		require.True(t, policy.StoreRequestBodyFor(8), "coarse flag is not set for project 8")

		// Other projects unaffected.
		require.True(t, policy.StoreInboundRequestBodyFor(3))
		require.True(t, policy.StoreOutboundRequestBodyFor(3))

		// Retention options survive decoding, including the newly honoured ones.
		days := map[string]int{}
		for _, opt := range policy.CleanupOptions {
			days[opt.ResourceType] = opt.CleanupDays
		}

		require.Equal(t, 3, days["requests"], "request log retention must stay at 3 days")
		require.Equal(t, 2, days["request_bodies"])
		require.Equal(t, 2, days["response_bodies"])
	})

	t.Run("round-trip keeps overrides", func(t *testing.T) {
		original := StoragePolicy{
			StoreRequestBody:  true,
			StoreResponseBody: true,
			ProjectOverrides: map[string]ProjectStorageOverride{
				"8": {StoreInboundRequestBody: boolPtr(false)},
			},
		}

		encoded, err := json.Marshal(original)
		require.NoError(t, err)

		decoded := StoragePolicy{
			StoreRequestBody:  true,
			StoreResponseBody: true,
		}
		require.NoError(t, json.Unmarshal(encoded, &decoded))

		require.False(t, decoded.StoreInboundRequestBodyFor(8))
		require.True(t, decoded.StoreOutboundRequestBodyFor(8))
		require.Contains(t, string(encoded), "project_overrides")
	})
}

func boolPtr(v bool) *bool {
	return &v
}
