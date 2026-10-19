package transport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/runesafe/v3"
)

// FuzzRedactionLeavesNoKey: whatever a server answers, with or without an echo of an
// accepted key, no presented text holds the key, a presented copy is one line within
// its cap, and Body stays within the 64 KiB cap (docs/request-behavior.md "Errors").
func FuzzRedactionLeavesNoKey(f *testing.F) {
	f.Add("aba", "x ababa y", 2)
	f.Add("redacted", "[redacted]", 0)
	f.Add("...", strings.Repeat("Z", 70), 61)
	f.Add("d", "d\r\nd\x1bd", 1)
	f.Add("k.y", "k.\xffk.y.y", 4)
	f.Fuzz(func(t *testing.T, rawKey, text string, at int) {
		key := strings.Map(func(r rune) rune {
			if strings.ContainsRune(keyAlphabet, r) {
				return r
			}
			return -1
		}, rawKey)
		if key == "" {
			t.Skip()
		}
		at = min(max(at, 0), len(text))
		for _, in := range []string{text, text[:at] + key + text[at:]} {
			c := internalClient(t, arrapi.APIKey(key))
			got := c.Present(in, ServerTextBytes)
			if strings.Contains(got, key) || len(got) > ServerTextBytes || strings.ContainsFunc(got, runesafe.IsUnsafeSingleLine) {
				t.Fatalf("key %q: Present(%q) = %q", key, in, got)
			}
			body, err := json.Marshal(map[string]string{"message": in})
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			for _, raw := range []string{in, string(body)} {
				se := captureBody(t, raw, arrapi.APIKey(key))
				detail := strings.TrimPrefix(se.Error(), "arrapi: /api/v3/series: HTTP 500")
				if strings.Contains(se.Body, key) || strings.Contains(se.Message, key) || strings.Contains(detail, key) {
					t.Fatalf("key %q: answer %q gave Body %q, Message %q, Error() %q", key, raw, se.Body, se.Message, se.Error())
				}
				if len(se.Body) > maxErrorBodyBytes || len(se.Message) > maxMessageBytes || strings.ContainsFunc(se.Error(), runesafe.IsUnsafeSingleLine) {
					t.Fatalf("key %q: answer %q gave a Body of %d bytes, a Message of %d or Error() %q", key, raw, len(se.Body), len(se.Message), se.Error())
				}
			}
		}
	})
}
