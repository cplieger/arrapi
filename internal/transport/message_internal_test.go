package transport

import (
	"strings"
	"testing"
)

func TestStatusErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"error model message", `{"message":"seriesId is required","description":"at Sonarr.Api..."}`, "seriesId is required"},
		{"ProblemDetails title", `{"type":"https://tools.ietf.org/html/rfc9110#section-15.5.5","title":"Not Found","status":404}`, "Not Found"},
		{"message preferred over title", `{"title":"Bad Request","message":"invalid id"}`, "invalid id"},
		{"empty message falls back to title", `{"message":"  ","title":"Conflict"}`, "Conflict"},
		{"non-string message ignored", `{"message":42,"title":"Teapot"}`, "Teapot"},
		{"not JSON", `<html>Bad Gateway</html>`, ""},
		{"JSON array", `["message"]`, ""},
		{"line breaks folded", `{"message":"one\ntwo\u001b[31m"}`, "one two [31m"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := captureBody(t, tc.body, "irrelevant-key").Message; got != tc.want {
				t.Errorf("Message = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusErrorMessageEmptyWhenBodyCut(t *testing.T) {
	body := `{"message":"short","pad":"` + strings.Repeat("x", maxErrorBodyBytes) + `"}`
	if got := captureBody(t, body, "irrelevant-key").Message; got != "" {
		t.Errorf("Message of a cut body = %q, want empty", got)
	}
}

func TestStatusErrorMessageBounded(t *testing.T) {
	tests := map[string]string{
		"long ASCII":                           strings.Repeat("m", 4096),
		"invalid UTF-8 grows under sanitizing": strings.Repeat(`\u00ff`, 100) + strings.Repeat("\xff", 600),
	}
	for name, msg := range tests {
		t.Run(name, func(t *testing.T) {
			got := captureBody(t, `{"message":"`+msg+`"}`, "irrelevant-key").Message
			if len(got) > maxMessageBytes || !strings.HasSuffix(got, cutMarker) {
				t.Errorf("Message is %d bytes, want at most %d ending in the marker", len(got), maxMessageBytes)
			}
		})
	}
}

// TestStatusErrorMessageKeyStraddlingTheCap escapes the key's first rune in a form no
// encoder writes, so only decoding the message rebuilds the key, across byte 512.
func TestStatusErrorMessageKeyStraddlingTheCap(t *testing.T) {
	const key = "key-straddle-0042" // gitleaks:allow (fake key, redaction test fixture)
	escaped := `\u006bey-straddle-0042`
	msg := strings.Repeat("m", maxMessageBytes-len(cutMarker)-4) + escaped + " tail"
	se := captureBody(t, `{"message":"`+msg+`"}`, key)
	if strings.Contains(se.Message, key) {
		t.Errorf("Message holds the key")
	}
	if n := keyPrefixTail(strings.TrimSuffix(se.Message, cutMarker), key); n > 0 {
		t.Errorf("Message ends in a %d-byte key prefix", n)
	}
	if len(se.Message) > maxMessageBytes {
		t.Errorf("Message is %d bytes", len(se.Message))
	}
}
