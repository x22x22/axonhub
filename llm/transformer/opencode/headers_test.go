package opencode

import (
	"net/http"
	"testing"
)

func TestGetSessionIDFromHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{
			name:    "X-Opencode-Session wins",
			headers: map[string]string{SessionHeader: "ses_new", SessionIDHeader: "ses_id", SessionAffinityHeader: "ses_aff"},
			want:    "ses_new",
		},
		{
			name:    "X-Session-Id next",
			headers: map[string]string{SessionIDHeader: "ses_id", SessionAffinityHeader: "ses_aff"},
			want:    "ses_id",
		},
		{
			name:    "legacy X-Session-Affinity",
			headers: map[string]string{SessionAffinityHeader: "[\"ses_aff\"]"},
			want:    "[\"ses_aff\"]",
		},
		{
			name:    "trimmed",
			headers: map[string]string{SessionHeader: "  ses_trim  "},
			want:    "ses_trim",
		},
		{
			name:    "empty header is skipped",
			headers: map[string]string{SessionHeader: "   ", SessionAffinityHeader: "ses_aff"},
			want:    "ses_aff",
		},
		{
			name:    "no session headers",
			headers: map[string]string{"User-Agent": "codex_cli_rs/0.38.0"},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			for key, value := range tt.headers {
				headers.Set(key, value)
			}

			if got := GetSessionIDFromHeaders(headers); got != tt.want {
				t.Fatalf("GetSessionIDFromHeaders() = %q, want %q", got, tt.want)
			}
		})
	}
}
