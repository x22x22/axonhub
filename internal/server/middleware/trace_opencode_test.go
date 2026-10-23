package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/looplj/axonhub/internal/tracing"
	"github.com/looplj/axonhub/llm/transformer/opencode"
)

func newTraceTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	return ctx, recorder
}

func TestTryExtractTraceIDFromOpenCodeRequest(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{
			name:    "X-Opencode-Session",
			headers: map[string]string{opencode.SessionHeader: "ses_oc"},
			want:    "ses_oc",
		},
		{
			name:    "X-Session-Id",
			headers: map[string]string{opencode.SessionIDHeader: "ses_id"},
			want:    "ses_id",
		},
		{
			name:    "legacy X-Session-Affinity",
			headers: map[string]string{opencode.SessionAffinityHeader: "[\"ses_aff\"]"},
			want:    "[\"ses_aff\"]",
		},
		{
			name:    "none",
			headers: map[string]string{"User-Agent": "codex_cli_rs/0.38.0"},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := newTraceTestContext()
			for key, value := range tt.headers {
				ctx.Request.Header.Set(key, value)
			}

			if got := tryExtractTraceIDFromOpenCodeRequest(ctx); got != tt.want {
				t.Fatalf("tryExtractTraceIDFromOpenCodeRequest() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ExtraTraceHeaders must keep working and win over the OpenCode extraction: a deployment
// configures it for clients whose session header is not known to axonhub.
func TestExtraTraceHeadersWinOverOpenCodeHeader(t *testing.T) {
	ctx, _ := newTraceTestContext()
	ctx.Request.Header.Set("X-Custom-Session", "ses_custom")
	ctx.Request.Header.Set(opencode.SessionAffinityHeader, "ses_affinity")

	config := tracing.Config{ExtraTraceHeaders: []string{"X-Custom-Session"}}
	if got := getTraceIDFromHeader(ctx, config); got != "ses_custom" {
		t.Fatalf("getTraceIDFromHeader() = %q, want ses_custom", got)
	}
}

// The primary trace header still has the highest priority.
func TestPrimaryTraceHeaderWinsOverExtraHeaders(t *testing.T) {
	ctx, _ := newTraceTestContext()
	ctx.Request.Header.Set("AH-Trace-Id", "at-primary")
	ctx.Request.Header.Set("X-Custom-Session", "ses_custom")

	config := tracing.Config{ExtraTraceHeaders: []string{"X-Custom-Session"}}
	if got := getTraceIDFromHeader(ctx, config); got != "at-primary" {
		t.Fatalf("getTraceIDFromHeader() = %q, want at-primary", got)
	}
}
