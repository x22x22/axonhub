package orchestrator

import (
	"context"
	"net/http"
	"testing"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/opencode"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

// setSessionHeader forwards the conversation session id so an upstream provider can keep
// per-session cache/stickiness across turns.
func TestSetSessionHeader(t *testing.T) {
	t.Run("sets header from context", func(t *testing.T) {
		req := &httpclient.Request{}
		ctx := shared.WithSessionID(context.Background(), "ses_123")

		setSessionHeader(ctx, req)

		if got := req.Headers.Get(opencode.SessionHeader); got != "ses_123" {
			t.Fatalf("header = %q, want ses_123", got)
		}
	})

	t.Run("keeps a client supplied header", func(t *testing.T) {
		req := &httpclient.Request{Headers: http.Header{opencode.SessionHeader: []string{"ses_client"}}}
		ctx := shared.WithSessionID(context.Background(), "ses_context")

		setSessionHeader(ctx, req)

		if got := req.Headers.Get(opencode.SessionHeader); got != "ses_client" {
			t.Fatalf("header = %q, want the client value ses_client", got)
		}
	})

	t.Run("no session does not add a header", func(t *testing.T) {
		req := &httpclient.Request{}

		setSessionHeader(context.Background(), req)

		if got := req.Headers.Get(opencode.SessionHeader); got != "" {
			t.Fatalf("header = %q, want empty", got)
		}
	})

	t.Run("blank session does not add a header", func(t *testing.T) {
		req := &httpclient.Request{}
		ctx := shared.WithSessionID(context.Background(), "   ")

		setSessionHeader(ctx, req)

		if got := req.Headers.Get(opencode.SessionHeader); got != "" {
			t.Fatalf("header = %q, want empty", got)
		}
	})

	t.Run("nil request is a no-op", func(t *testing.T) {
		setSessionHeader(shared.WithSessionID(context.Background(), "ses_123"), nil)
	})

	t.Run("initialises nil headers", func(t *testing.T) {
		req := &httpclient.Request{Headers: nil}
		ctx := shared.WithSessionID(context.Background(), "ses_456")

		setSessionHeader(ctx, req)

		if req.Headers == nil {
			t.Fatal("headers were not initialised")
		}

		if got := req.Headers.Get(opencode.SessionHeader); got != "ses_456" {
			t.Fatalf("header = %q, want ses_456", got)
		}
	})
}
