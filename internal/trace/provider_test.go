package trace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProviderWithoutEndpointIsNoop(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	provider, err := NewProvider()
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.NoError(t, provider.Shutdown(context.Background()))
}

// Endpoint values commonly carry a scheme and a base path (for example
// http://collector:4318/otel). WithEndpoint only accepts host:port and would keep the
// scheme/path in the host, so the constructor uses WithEndpointURL; this test pins that a
// full URL is accepted and shutdown is clean.
func TestNewProviderAcceptsEndpointWithSchemeAndPath(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318/otel")

	provider, err := NewProvider()
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.NoError(t, provider.Shutdown(context.Background()))
}

func TestNewProviderRejectsEndpointWithoutHost(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector:4318")

	_, err := NewProvider()
	require.Error(t, err)
}
