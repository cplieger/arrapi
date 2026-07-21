package arrapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cplieger/runesafe"
)

// captureStatusError runs statusError over a synthetic non-2xx response and
// returns the typed *StatusError.
func captureStatusError(t *testing.T, body, apiKey string) *StatusError {
	t.Helper()
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	err := statusError(resp, "/api/v3/series", apiKey)
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("statusError returned %T, want *StatusError", err)
	}
	return se
}

// TestStatusError_bodySanitizedAtCapture pins the capture-side sanitization
// contract: a hostile or garbled arr response carrying terminal escapes (C0),
// raw C1 escape introducers, or bidi controls is neutralized in the Body field
// itself, so every consumer access path (Error(), direct field reads, slog
// "error" attrs) is safe without a render-side hop.
func TestStatusError_bodySanitizedAtCapture(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			// OSC retitle + CSI clear: the classic log-injection payload.
			"ANSI escape sequences",
			"fail \x1b]0;pwned\x07 and \x1b[2Jgone",
			"fail  ]0;pwned  and  [2Jgone",
		},
		{
			// U+009B is a single-rune CSI introducer emitted raw by slog.
			"C1 controls",
			"bad \u009b31m stuff \u0090 here",
			"bad  31m stuff   here",
		},
		{
			// RLO flips rendered order (Trojan-Source-style spoofing).
			"bidi controls",
			"path \u202e/cod.evil\u202c end",
			"path  /cod.evil  end",
		},
		{
			// A raw newline forges a second log record in a single-line view;
			// JSON encoders escape it, and runesafe's Sanitize policy keeps CR/LF.
			"line separators U+2028/U+2029",
			"one\u2028two\u2029three",
			"one two three",
		},
		{
			"NUL and DEL",
			"a\x00b\x7fc",
			"a b c",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			se := captureStatusError(t, tc.body, "irrelevant-key")
			if se.Body != tc.want {
				t.Errorf("Body = %q, want %q", se.Body, tc.want)
			}
			if got := se.Error(); strings.ContainsAny(got, "\x1b\x00\x7f\u009b\u0090\u202e\u202c\u2028\u2029") {
				t.Errorf("Error() still carries unsafe runes: %q", got)
			}
		})
	}
}

// TestStatusError_cleanBodyByteIdentical pins that sanitization is a no-op on
// a well-behaved response: printable ASCII and multi-byte-but-safe Unicode
// (including CR/LF, which the log policy keeps for JSON-escaping sinks) pass
// through byte-identical, so normal arr error payloads are never distorted.
func TestStatusError_cleanBodyByteIdentical(t *testing.T) {
	const body = `{"message":"S\u00e9rie not found — フリーレン","status":404}` + "\r\nsecond line"
	se := captureStatusError(t, body, "irrelevant-key")
	if se.Body != body {
		t.Errorf("clean body altered by capture:\n got %q\nwant %q", se.Body, body)
	}
}

// TestStatusError_truncationMarked pins the marker contract for a clean
// oversized body: content preserved verbatim up to the maxErrorBodyBytes cut,
// then the truncationMarker appended OUTSIDE the cap (runesafe's bounded
// preset convention), so an operator can tell a truncated capture from a
// genuinely short response.
func TestStatusError_truncationMarked(t *testing.T) {
	body := strings.Repeat("A", maxErrorBodyBytes+512)
	se := captureStatusError(t, body, "irrelevant-key")
	if len(se.Body) != maxErrorBodyBytes+len(truncationMarker) {
		t.Errorf("Body length = %d, want exactly %d (cap + marker)", len(se.Body), maxErrorBodyBytes+len(truncationMarker))
	}
	if se.Body[:maxErrorBodyBytes] != body[:maxErrorBodyBytes] {
		t.Error("capped Body diverges from the leading maxErrorBodyBytes bytes of the wire body")
	}
	if !strings.HasSuffix(se.Body, truncationMarker) {
		t.Errorf("truncated Body does not end in the %q marker", truncationMarker)
	}
}

// TestStatusError_invalidUTF8ExpansionStaysUnderCap pins the post-sanitization
// re-cap: runesafe.Sanitize maps each invalid UTF-8 byte to U+FFFD (3 bytes), so
// a garbage body at the cap would otherwise triple past it. The re-cap must
// hold the 64 KiB bound (plus the marker, which sits outside the cap) and cut
// on a rune boundary so the truncated tail cannot itself reintroduce raw
// 0x80-0x9F (C1) bytes.
func TestStatusError_invalidUTF8ExpansionStaysUnderCap(t *testing.T) {
	body := strings.Repeat("\x92", maxErrorBodyBytes+512) // C1-range invalid bytes
	se := captureStatusError(t, body, "irrelevant-key")
	if len(se.Body) > maxErrorBodyBytes+len(truncationMarker) {
		t.Errorf("Body length = %d, exceeds cap %d + marker after U+FFFD expansion", len(se.Body), maxErrorBodyBytes)
	}
	if !utf8.ValidString(se.Body) {
		t.Error("re-capped Body is not valid UTF-8")
	}
	if strings.ContainsRune(se.Body, '\u0092') {
		t.Error("Body still contains a C1 control rune")
	}
	if !strings.HasSuffix(se.Body, truncationMarker) {
		t.Errorf("truncated Body does not end in the %q marker", truncationMarker)
	}
}

// TestStatusError_redactionStillMatchesRawBytes pins the sanitize-last
// ordering: a reflected API key adjacent to control bytes must still be
// redacted, which requires redaction to see the raw wire bytes before
// sanitization rewrites them.
func TestStatusError_redactionStillMatchesRawBytes(t *testing.T) {
	const apiKey = "reflected-secret-key"
	se := captureStatusError(t, "unauthorized:\x1b[31m "+apiKey+" \x1b[0m(rejected)", apiKey)
	if strings.Contains(se.Body, apiKey) {
		t.Errorf("Body leaks the API key: %q", se.Body)
	}
	if !strings.Contains(se.Body, "REDACTED") {
		t.Errorf("Body = %q, want it to contain REDACTED", se.Body)
	}
	if strings.ContainsRune(se.Body, '\x1b') {
		t.Errorf("Body still contains ESC: %q", se.Body)
	}
}

// TestStatusError_bodyMatchesSharedSanitizerPolicy pins that the captured Body
// is a fixed point of runesafe.Sanitize for arbitrary-ish wire bytes:
// whatever the policy says is unsafe cannot survive capture, so the field is
// safe under the exact same contract consumers' own emit boundaries use.
func TestStatusError_bodyMatchesSharedSanitizerPolicy(t *testing.T) {
	body := "mix \x1b\u009b\u202e\u2028\x00ok\xff\xfe tail"
	se := captureStatusError(t, body, "irrelevant-key")
	if got := runesafe.Sanitize(se.Body); got != se.Body {
		t.Errorf("Body is not sanitizer-stable:\n got %q\nafter %q", se.Body, got)
	}
}
