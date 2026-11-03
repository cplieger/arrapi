package transport

import (
	"errors"
	"regexp"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

var decodeTextGrammar = regexp.MustCompile(`^arrapi: decode ` + regexp.QuoteMeta(decodePath) + `: (` +
	`invalid JSON at byte \d+` +
	`|a date-time is not RFC 3339` +
	`|the answer does not match the expected shape` +
	`|(field (date|id|size|hasFile|ids|nested|eventType|count)(\.(count))?: )?` +
	`a JSON (value|string|number|bool|array|object) cannot decode into [][*.A-Za-z0-9_]+` +
	`)$`)

// decodeContentTypes are the answers' Content-Type headers the decode fuzz target
// picks from, with the ContentType each must give.
var decodeContentTypes = []struct{ header, want string }{
	{"application/json; charset=utf-8", "application/json"},
	{"Text/HTML", "Text/HTML"},
	{"", ""},
	{"application/problem+json \t; q=1", "application/problem+json"},
}

// FuzzDecodeErrorTextIsArrapis pins that whatever body is accepted, the decode error
// is one of arrapi's own sentences over names its code declares, carries the answer's
// media type, and wraps at most the decoder's error with its server values removed:
// a *json.SyntaxError as it came, a rebuilt *json.UnmarshalTypeError, or nothing.
func FuzzDecodeErrorTextIsArrapis(f *testing.F) {
	for i, seed := range []string{
		`[{"date":"20241f3c9a7b4e2d8c6a5b3f1e0d9c8b"}]`,
		`[{"id":99999999999999999999999999}]`,
		`[{"hasFile":"x"}]`,
		`[{"nested":{"count":true}}]`,
		`[{"eventType":{"k":"v"}}]`,
		`{"id":1}`,
		`[1]`,
		`{not json`,
	} {
		f.Add([]byte(seed), uint8(i))
	}
	c, err := New("http://sonarr:8989", "unrelated-client-key", Config{})
	if err != nil {
		f.Fatalf("New: %v", err)
	}
	f.Cleanup(c.Close)
	f.Fuzz(func(t *testing.T, body []byte, pick uint8) {
		ct := decodeContentTypes[int(pick)%len(decodeContentTypes)]
		var out []decodeProbe
		_, err := decodeBody(c, body, ct.header, &out)
		if err == nil {
			return
		}
		if !decodeTextGrammar.MatchString(err.Error()) {
			t.Fatalf("decode error %q is not one of arrapi's sentences", err)
		}
		AssertDecodeCause(t, err)
		if de, _ := errors.AsType[*arrapi.DecodeError](err); de.ContentType != ct.want {
			t.Fatalf("Content-Type %q gave ContentType %q, want %q", ct.header, de.ContentType, ct.want)
		}
	})
}
