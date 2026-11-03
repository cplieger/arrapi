package transport

import (
	"encoding/json"
	"errors"
	"fmt"
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

// FuzzDecodeErrorLeavesNoKey: whatever body and Content-Type a server answers, with an
// echo of an accepted key in both, the DecodeError's ContentType and Err hold no key,
// ContentType is one line within its cap, and Error() does not depend on the key the
// client holds (FuzzDecodeErrorTextIsArrapis pins that it is arrapi's own sentence).
func FuzzDecodeErrorLeavesNoKey(f *testing.F) {
	f.Add("x", `{`, "application/json", 1)
	f.Add("date", `[{"date":1}]`, "text/html; charset=", 3)
	f.Add("test-key-9b1d", `[{"id":"`, "application/", 8)
	f.Add("ab", `[{"ids":[`, "Text/", 0)
	f.Fuzz(func(t *testing.T, rawKey, body, contentType string, at int) {
		key := strings.Map(func(r rune) rune {
			if strings.ContainsRune(keyAlphabet, r) {
				return r
			}
			return -1
		}, rawKey)
		if key == "" {
			t.Skip()
		}
		bodyAt, ctAt := min(max(at, 0), len(body)), min(max(at, 0), len(contentType))
		body = body[:bodyAt] + key + body[bodyAt:]
		contentType = contentType[:ctAt] + key + contentType[ctAt:]
		var out, otherOut []decodeProbe
		_, err := decodeBody(internalClient(t, arrapi.APIKey(key)), []byte(body), contentType, &out)
		if err == nil {
			return
		}
		de, ok := errors.AsType[*arrapi.DecodeError](err)
		if !ok {
			t.Fatalf("key %q: body %q gave %v (%T), want an *arrapi.DecodeError", key, body, err, err)
		}
		if strings.Contains(de.ContentType, key) || len(de.ContentType) > ServerTextBytes || strings.ContainsFunc(de.ContentType, runesafe.IsUnsafeSingleLine) {
			t.Fatalf("key %q: Content-Type %q gave ContentType %q", key, contentType, de.ContentType)
		}
		if de.Err != nil && (strings.Contains(de.Err.Error(), key) || strings.Contains(fmt.Sprintf("%+v", de.Err), key)) {
			t.Fatalf("key %q: body %q gave an Err rendering it: %v", key, body, de.Err)
		}
		_, other := decodeBody(internalClient(t, "unrelated-client-key"), []byte(body), contentType, &otherOut)
		if other == nil || other.Error() != de.Error() {
			t.Fatalf("key %q: body %q gave Error() %q, but %v under another key", key, body, de.Error(), other)
		}
	})
}
