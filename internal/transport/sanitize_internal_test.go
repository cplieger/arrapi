package transport

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/runesafe/v3"
)

func internalClient(t *testing.T, key arrapi.APIKey) *Client {
	t.Helper()
	c, err := New("http://sonarr:8989", key, Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// captureBody runs the failure capture over a synthetic 500 answer.
func captureBody(t *testing.T, body string, key arrapi.APIKey) *arrapi.StatusError {
	t.Helper()
	resp := &http.Response{StatusCode: http.StatusInternalServerError, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	return internalClient(t, key).capture(500, resp, "/api/v3/series")
}

func TestStatusErrorBodySanitizedAtCapture(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"ANSI escape sequences", "fail \x1b]0;pwned\x07 and \x1b[2Jgone", "fail  ]0;pwned  and  [2Jgone"},
		{"C1 controls", "bad \u009b31m stuff \u0090 here", "bad  31m stuff   here"},
		{"bidi controls", "path \u202e/cod.evil\u202c end", "path  /cod.evil  end"},
		{"line separators U+2028 and U+2029", "one\u2028two\u2029three", "one two three"},
		{"NUL and DEL", "a\x00b\x7fc", "a b c"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			se := captureBody(t, tc.body, "irrelevant-key")
			if se.Body != tc.want {
				t.Errorf("Body = %q, want %q", se.Body, tc.want)
			}
			if got := se.Error(); strings.ContainsAny(got, "\x1b\x00\x7f\u009b\u0090\u202e\u202c\u2028\u2029") {
				t.Errorf("Error() still carries unsafe runes: %q", got)
			}
		})
	}
}

func TestStatusErrorCleanBodyByteIdentical(t *testing.T) {
	const body = `{"message":"S\u00e9rie not found — フリーレン","status":404}` + "\r\nsecond line"
	if se := captureBody(t, body, "irrelevant-key"); se.Body != body {
		t.Errorf("clean body altered by capture:\n got %q\nwant %q", se.Body, body)
	}
}

func TestStatusErrorTruncationMarkedInsideTheCap(t *testing.T) {
	body := strings.Repeat("A", maxErrorBodyBytes+512)
	se := captureBody(t, body, "irrelevant-key")
	if len(se.Body) != maxErrorBodyBytes {
		t.Errorf("Body length = %d, want exactly %d (the cap, marker included)", len(se.Body), maxErrorBodyBytes)
	}
	if !strings.HasSuffix(se.Body, cutMarker) || se.Body[:maxErrorBodyBytes-3] != body[:maxErrorBodyBytes-3] {
		t.Error("cut Body is not the leading bytes of the wire body followed by the marker")
	}
}

func TestStatusErrorInvalidUTF8ExpansionStaysUnderCap(t *testing.T) {
	se := captureBody(t, strings.Repeat("\x92", maxErrorBodyBytes+512), "irrelevant-key")
	if len(se.Body) > maxErrorBodyBytes {
		t.Errorf("Body length = %d, exceeds the %d cap after U+FFFD expansion", len(se.Body), maxErrorBodyBytes)
	}
	if !utf8.ValidString(se.Body) || strings.ContainsRune(se.Body, '\u0092') {
		t.Error("re-capped Body is not valid UTF-8 or still holds a C1 rune")
	}
	if !strings.HasSuffix(se.Body, cutMarker) {
		t.Errorf("cut Body does not end in %q", cutMarker)
	}
}

func TestStatusErrorRedactionMatchesRawBytes(t *testing.T) {
	const key = "reflected-secret-key"
	se := captureBody(t, "unauthorized:\x1b[31m "+key+" \x1b[0m(rejected)", key)
	if strings.Contains(se.Body, key) || !strings.Contains(se.Body, redactedMask) || strings.ContainsRune(se.Body, '\x1b') {
		t.Errorf("Body = %q, want the key masked and ESC gone", se.Body)
	}
}

func TestStatusErrorBodyIsSanitizerStable(t *testing.T) {
	se := captureBody(t, "mix \x1b\u009b\u202e\u2028\x00ok\xff\xfe tail", "irrelevant-key")
	if got := runesafe.Sanitize(se.Body); got != se.Body {
		t.Errorf("Body is not sanitizer-stable:\n got %q\nafter %q", se.Body, got)
	}
}

func TestCutMarked(t *testing.T) {
	c := internalClient(t, "0123456789abcdef")
	limit := maxErrorBodyBytes
	for name, s := range map[string]string{
		"ascii":                 strings.Repeat("A", limit+100),
		"invalid UTF-8":         strings.Repeat("\xff", limit+100),
		"multi-byte":            strings.Repeat("é", limit),
		"key prefix at the cut": strings.Repeat("Z", limit-5) + "0123456789",
	} {
		got := c.cutMarked(s, limit)
		if len(got) > limit {
			t.Errorf("cutMarked(%s) is %d bytes", name, len(got))
		}
		if !strings.HasSuffix(got, cutMarker) {
			t.Errorf("cutMarked(%s) has no marker", name)
		}
		if !utf8.ValidString(strings.TrimSuffix(got, cutMarker)) && utf8.ValidString(s) {
			t.Errorf("cutMarked(%s) cut inside a rune", name)
		}
		if n := keyPrefixTail(strings.TrimSuffix(got, cutMarker), "0123456789abcdef"); n > 0 {
			t.Errorf("cutMarked(%s) kept a %d-byte key prefix", name, n)
		}
	}
}

func TestPresentRedactsTheKey(t *testing.T) {
	const key = "ab.c-d"
	tests := []struct {
		name string
		in   string
	}{
		{"raw", "x " + key + " y"},
		{"between control bytes", "x\x1b" + key + "\r\ny"},
		{"straddling the cap", strings.Repeat("Z", 60) + key},
	}
	c := internalClient(t, key)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Present(tc.in, ServerTextBytes)
			if len(got) > ServerTextBytes {
				t.Errorf("Present is %d bytes, want at most %d", len(got), ServerTextBytes)
			}
			if strings.Contains(got, key) {
				t.Errorf("Present = %q holds the key", got)
			}
			if n := keyPrefixTail(strings.TrimSuffix(got, cutMarker), key); n > 0 {
				t.Errorf("Present = %q ends in a %d-byte key prefix", got, n)
			}
			for _, r := range got {
				if runesafe.IsUnsafeSingleLine(r) {
					t.Errorf("Present = %q holds unsafe rune %U", got, r)
				}
			}
		})
	}
}

// TestRedactionMasksOverlappingOccurrencesOnce: every byte an occurrence covers is
// found before any is replaced, and one "[redacted]" replaces each run, the rule
// runesafe.RedactSingleLineCapped documents; a mask per occurrence would leave a
// fragment of the second one ("[redacted]ba").
func TestRedactionMasksOverlappingOccurrencesOnce(t *testing.T) {
	const key = "aba"
	if got := internalClient(t, key).Present("x ababa y", ServerTextBytes); got != "x [redacted] y" {
		t.Errorf("Present = %q, want %q", got, "x [redacted] y")
	}
	se := captureBody(t, `{"message":"x ababa y"}`, key)
	if se.Body != `{"message":"x [redacted] y"}` {
		t.Errorf("Body = %q, want %q", se.Body, `{"message":"x [redacted] y"}`)
	}
	if se.Message != "x [redacted] y" {
		t.Errorf("Message = %q, want %q", se.Message, "x [redacted] y")
	}
}

// TestNoKeySurvivesAKeyThatMatchesAMarker covers keys that occur in or overlap the
// mask or the cut marker: replacing the key with the mask, or appending the marker,
// must not put the key back.
func TestNoKeySurvivesAKeyThatMatchesAMarker(t *testing.T) {
	long := strings.Repeat("Z", 600)
	for _, key := range []string{"redacted", "act", "d", ".", "...", "Z."} {
		c := internalClient(t, arrapi.APIKey(key))
		outputs := map[string]string{
			"Present of an echo":   c.Present("x "+key+" y", ServerTextBytes),
			"Present of a cut":     c.Present(long, ServerTextBytes),
			"Present of a cut key": c.Present(long+key, ServerTextBytes),
			"Body of an echo":      captureBody(t, "x "+key+" y", arrapi.APIKey(key)).Body,
			"Body of a cut":        captureBody(t, strings.Repeat("Z", maxErrorBodyBytes+10), arrapi.APIKey(key)).Body,
			"Message of an echo":   captureBody(t, `{"message":"x `+key+` y"}`, arrapi.APIKey(key)).Message,
			"Message of a cut":     captureBody(t, `{"message":"`+long+`"}`, arrapi.APIKey(key)).Message,
		}
		for what, got := range outputs {
			if strings.Contains(got, key) {
				t.Errorf("key %q: %s = %q holds the key", key, what, got)
			}
		}
	}
}

// keyPrefixTail returns the length of the longest prefix of key that s ends with.
func keyPrefixTail(s, key string) int {
	for n := len(key); n >= 1; n-- {
		if strings.HasSuffix(s, key[:n]) {
			return n
		}
	}
	return 0
}
