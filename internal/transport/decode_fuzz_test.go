package transport

import (
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
)

var decodeTextGrammar = regexp.MustCompile(`^arrapi: decode ` + regexp.QuoteMeta(decodePath) + `: (` +
	`invalid JSON at byte \d+` +
	`|a date-time is not RFC 3339` +
	`|the answer does not match the expected shape` +
	`|(field (date|id|size|hasFile|ids|nested|eventType|count)(\.(count))?: )?` +
	`a JSON (value|string|number|bool|array|object) cannot decode into [][*.A-Za-z0-9_]+` +
	`)$`)

// FuzzDecodeErrorTextIsArrapis pins that whatever body is accepted, the decode error
// is one of arrapi's own sentences over names its code declares, and wraps nothing.
func FuzzDecodeErrorTextIsArrapis(f *testing.F) {
	for _, seed := range []string{
		`[{"date":"20241f3c9a7b4e2d8c6a5b3f1e0d9c8b"}]`,
		`[{"id":99999999999999999999999999}]`,
		`[{"hasFile":"x"}]`,
		`[{"nested":{"count":true}}]`,
		`[{"eventType":{"k":"v"}}]`,
		`{"id":1}`,
		`[1]`,
		`{not json`,
	} {
		f.Add([]byte(seed))
	}
	c, err := New("http://sonarr:8989", "unrelated-client-key", Config{})
	if err != nil {
		f.Fatalf("New: %v", err)
	}
	f.Cleanup(c.Close)
	f.Fuzz(func(t *testing.T, body []byte) {
		var out []decodeProbe
		_, err := c.readInto(io.NopCloser(strings.NewReader(string(body))), MaxListBytes, decodePath, &out)
		if err == nil {
			return
		}
		if !decodeTextGrammar.MatchString(err.Error()) {
			t.Fatalf("decode error %q is not one of arrapi's sentences", err)
		}
		if errors.Unwrap(err) != nil {
			t.Fatalf("decode error wraps %v", errors.Unwrap(err))
		}
	})
}
