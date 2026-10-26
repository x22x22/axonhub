package xjson

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestSanitizeNULStringEscapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no escape keeps document untouched",
			input: `{"messages":[{"role":"user","content":"hello"}]}`,
			want:  `{"messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name:  "single nul escape is dropped",
			input: `{"content":"\u0000"}`,
			want:  `{"content":""}`,
		},
		{
			name:  "nul escapes inside tool output are dropped",
			input: `{"content":"Output:\n\u0002binary\u0000\u0000"}`,
			want:  `{"content":"Output:\n\u0002binary"}`,
		},
		{
			name:  "escaped backslash followed by u0000 text is preserved",
			input: `{"content":"\\u0000"}`,
			want:  `{"content":"\\u0000"}`,
		},
		{
			name:  "escape after escaped backslash pair is dropped",
			input: `{"content":"\\\u0000"}`,
			want:  `{"content":"\\"}`,
		},
		{
			name:  "raw nul byte is dropped",
			input: "{\"content\":\"a\x00b\"}",
			want:  `{"content":"ab"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeNULStringEscapes([]byte(tt.input))
			if string(got) != tt.want {
				t.Fatalf("SanitizeNULStringEscapes() = %q, want %q", got, tt.want)
			}

			if !json.Valid(got) {
				t.Fatalf("sanitized document is not valid JSON: %q", got)
			}
		})
	}
}

// TestSanitizeNULStringEscapes_RealWorldPayload mirrors a request body recorded on
// the production gateway, where a shell tool result embedded binary output and
// PostgreSQL rejected the whole document with SQLSTATE 22P05.
func TestSanitizeNULStringEscapes_RealWorldPayload(t *testing.T) {
	payload := []byte(`{"model":"deepseek-v4.1-flash","messages":[{"role":"tool","content":"total lines: 71\nOutput:\n\u0002\u0000\u0000\ufffd\u0041"}],"stream":true}`)

	got := SanitizeNULStringEscapes(payload)

	if !json.Valid(got) {
		t.Fatalf("sanitized document is not valid JSON: %s", got)
	}

	if bytes.Contains(got, nulEscape) {
		t.Fatalf("sanitized document still contains a NUL escape: %s", got)
	}

	// Other escapes must be preserved verbatim.
	if !bytes.Contains(got, []byte(`\ufffd`)) || !bytes.Contains(got, []byte(`\u0041`)) {
		t.Fatalf("sanitizer altered unrelated escapes: %s", got)
	}
}

func TestSanitizeNULStringEscapes_UnchangedFastPath(t *testing.T) {
	payload := []byte(`{"content":"clean"}`)

	got := SanitizeNULStringEscapes(payload)

	if &got[0] != &payload[0] {
		t.Fatal("expected the fast path to return the input slice without copying")
	}
}

func TestSanitizeNULStringEscapes_Empty(t *testing.T) {
	if got := SanitizeNULStringEscapes(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %q", got)
	}

	if got := SanitizeNULStringEscapes([]byte{}); len(got) != 0 {
		t.Fatalf("expected empty slice for empty input, got %q", got)
	}
}
