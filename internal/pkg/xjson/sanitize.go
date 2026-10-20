package xjson

import "bytes"

// nulEscape is the six-byte JSON escape sequence that encodes the NUL character.
var nulEscape = []byte(`\u0000`)

// SanitizeNULStringEscapes removes JSON `\u0000` escapes from a raw JSON document.
//
// PostgreSQL's jsonb type cannot represent U+0000 inside strings and rejects any
// document that contains the `\u0000` escape with SQLSTATE 22P05
// ("unsupported Unicode escape sequence"), while other databases accept it.
// Bodies produced by tools (shell output, file reads, ...) regularly embed such
// escapes, which made the request bodies fail to persist and fall back to a
// placeholder record.
//
// The rewrite is escape aware: only a NUL escape that is really active is
// removed. A literal sequence such as `"\\u0000"` (an escaped backslash
// followed by the text "u0000") is left untouched, so valid JSON keeps its
// meaning. Raw NUL bytes, which are invalid JSON in the first place, are
// dropped as well.
//
// The function returns the input slice unchanged (same backing array) when
// nothing needs to be rewritten, so the common case stays allocation free.
func SanitizeNULStringEscapes(b []byte) []byte {
	if len(b) == 0 {
		return b
	}

	// Fast path: nothing to sanitize.
	if bytes.IndexByte(b, 0) < 0 && !bytes.Contains(b, nulEscape) {
		return b
	}

	out := make([]byte, 0, len(b))

	for i := 0; i < len(b); {
		c := b[i]
		if c == 0 {
			// Raw NUL is not valid JSON; drop it.
			i++
			continue
		}

		if c != '\\' {
			out = append(out, c)
			i++

			continue
		}

		// Count the run of backslashes. JSON escapes pair up, so only an odd
		// count means the last backslash introduces an escape sequence.
		j := i
		for j < len(b) && b[j] == '\\' {
			j++
		}

		run := j - i
		if run%2 == 1 && bytes.HasPrefix(b[j:], nulEscape[1:]) {
			// Keep the escaped backslash pairs, drop the escape itself.
			out = append(out, b[i:i+run-1]...)

			i = j + len(nulEscape) - 1

			continue
		}

		out = append(out, b[i:j]...)

		i = j
	}

	return out
}
