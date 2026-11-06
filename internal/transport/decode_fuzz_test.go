package transport

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
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

// FuzzGetFileContentType pins that a file read accepts an answer exactly when its
// Content-Type parses as the media type the call reads, and that a refusal's
// ContentType is one line of at most ServerTextBytes.
func FuzzGetFileContentType(f *testing.F) {
	for _, seed := range []string{
		"image/jpeg",
		"IMAGE/JPEG; charset=binary",
		"image/jpeg,",
		"image/jpeg2000",
		"image/jpeg; =x",
		"text/html",
		"",
		"image/jpeg\r\nX-Injected: 1",
		"image/" + strings.Repeat("j", 200),
		"\x1b[31mimage/jpeg",
	} {
		f.Add(seed)
	}
	c, err := New("http://sonarr:8989", "unrelated-client-key", Config{})
	if err != nil {
		f.Fatalf("New: %v", err)
	}
	f.Cleanup(c.Close)
	f.Fuzz(func(t *testing.T, header string) {
		var out []byte
		a := &attempt{method: http.MethodGet, path: decodePath, shown: decodePath, limit: MaxObjectBytes, media: "image/jpeg", out: &out}
		resp := &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": {header}},
			Body:          io.NopCloser(strings.NewReader("\xff\xd8\xff")),
			ContentLength: -1,
		}
		readErr, err := c.readInto(a, resp)
		if readErr != nil {
			t.Fatalf("readInto: read error %v", readErr)
		}
		mt, _, perr := mime.ParseMediaType(header)
		if want := perr == nil && mt == "image/jpeg"; (err == nil) != want {
			t.Fatalf("Content-Type %q: accepted %v, want %v (err %v)", header, err == nil, want, err)
		}
		if err == nil {
			if string(out) != "\xff\xd8\xff" {
				t.Fatalf("accepted %q, want the body", out)
			}
			return
		}
		de, ok := errors.AsType[*arrapi.DecodeError](err)
		if !ok {
			t.Fatalf("refusal %T %v, want *arrapi.DecodeError", err, err)
		}
		if strings.ContainsAny(de.ContentType, "\r\n") || len(de.ContentType) > ServerTextBytes {
			t.Fatalf("ContentType %q is not one line of at most %d bytes", de.ContentType, ServerTextBytes)
		}
		if out != nil {
			t.Fatalf("refusal left out = %q", out)
		}
	})
}
