package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"
)

const decodePath = "/api/v3/history/since"

// decodeProbe has a field of each kind the engines' DTOs decode: time, int, int64,
// bool, a list, a nested object and a token type whose UnmarshalJSON quotes its input.
type decodeProbe struct {
	Date    time.Time   `json:"date"`
	ID      int         `json:"id"`
	Size    int64       `json:"size"`
	HasFile bool        `json:"hasFile"`
	IDs     []int       `json:"ids"`
	Nested  decodeInner `json:"nested"`
	Event   quotingType `json:"eventType"`
}

type decodeInner struct {
	Count int `json:"count"`
}

// quotingType stands for any Unmarshaler whose error renders the value it rejected.
type quotingType int

func (*quotingType) UnmarshalJSON(b []byte) error {
	if b[0] == '{' {
		return fmt.Errorf("bad token %s", b)
	}
	return nil
}

func decodeErr(t *testing.T, c *Client, body string) error {
	t.Helper()
	var out []decodeProbe
	readErr, err := c.readInto(io.NopCloser(strings.NewReader(body)), MaxListBytes, decodePath, &out)
	if readErr != nil || err == nil {
		t.Fatalf("readInto(%.80q) = %v, %v; want a decode error", body, readErr, err)
	}
	return err
}

// keyWindow returns the first 8-byte window of key found in s, or "".
func keyWindow(s, key string) string {
	for i := 0; i+8 <= len(key); i++ {
		if strings.Contains(s, key[i:i+8]) {
			return key[i : i+8]
		}
	}
	return ""
}

func randomKey(r *rand.Rand, alphabet string) string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = alphabet[r.IntN(len(alphabet))]
	}
	return string(b)
}

// TestDecodeErrorCarriesNoServerText pins, over random keys echoed into every field
// kind, that a decode error's text does not depend on the value the server sent:
// it holds no 8-byte window of the key, equals the text of the same slot holding
// another value, and wraps nothing. A key opening with four digits parses as a
// year, which time.ParseError then follows with the rest of the key.
func TestDecodeErrorCarriesNoServerText(t *testing.T) {
	const hex = "0123456789abcdef"
	const digits = "0123456789"
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	slots := []struct {
		name     string
		alphabet string
		body     func(k string) string
		want     string
	}{
		{"date", hex, func(k string) string { return `[{"date":` + q(k) + `}]` }, "a date-time is not RFC 3339"},
		{"int from a string", hex, func(k string) string { return `[{"id":` + q(k) + `}]` }, "field id: a JSON string cannot decode into int"},
		{"int from a number", digits, func(k string) string { return `[{"id":` + k + `}]` }, "field id: a JSON number cannot decode into int"},
		{"int64 from a number", digits, func(k string) string { return `[{"size":` + k + `}]` }, "field size: a JSON number cannot decode into int64"},
		{"bool from a number", digits, func(k string) string { return `[{"hasFile":` + k + `}]` }, "field hasFile: a JSON number cannot decode into bool"},
		{"bool from a string", hex, func(k string) string { return `[{"hasFile":` + q(k) + `}]` }, "field hasFile: a JSON string cannot decode into bool"},
		{"list element", hex, func(k string) string { return `[{"ids":[1,` + q(k) + `]}]` }, "field ids: a JSON string cannot decode into int"},
		{"nested field", hex, func(k string) string { return `[{"nested":{"count":` + q(k) + `}}]` }, "field nested.count: a JSON string cannot decode into int"},
		{"token type", hex, func(k string) string { return `[{"eventType":{"k":` + q(k) + `}}]` }, "the answer does not match the expected shape"},
	}
	r := rand.New(rand.NewPCG(1, 2))
	for _, s := range slots {
		t.Run(s.name, func(t *testing.T) {
			for i := range 200 {
				key := randomKey(r, s.alphabet)
				switch {
				case i%2 == 0:
					key = "2024" + key[4:]
				case key[0] == '0':
					key = "1" + key[1:] // a JSON number has no leading zero
				}
				c := internalClient(t, "unrelated-client-key")
				err := decodeErr(t, c, s.body(key))
				want := "arrapi: decode " + decodePath + ": " + s.want
				for what, text := range map[string]string{"Error()": err.Error(), "%+v": fmt.Sprintf("%+v", err)} {
					if w := keyWindow(text, key); w != "" {
						t.Fatalf("key %s: %s holds %q: %s", key, what, w, text)
					}
					if text != want {
						t.Fatalf("key %s: %s = %q, want %q", key, what, text, want)
					}
				}
				if errors.Unwrap(err) != nil {
					t.Fatalf("key %s: error wraps %v, want nothing", key, errors.Unwrap(err))
				}
				if multi, ok := err.(interface{ Unwrap() []error }); ok {
					t.Fatalf("key %s: error wraps %v, want nothing", key, multi.Unwrap())
				}
			}
		})
	}
}

func TestDecodeErrorSyntax(t *testing.T) {
	c := internalClient(t, "unrelated-client-key")
	for body, want := range map[string]string{
		`{not json`: "invalid JSON at byte 2",
		``:          "invalid JSON at byte 0",
		`[{"id":1}`: "invalid JSON at byte 9",
	} {
		err := decodeErr(t, c, body)
		if got := err.Error(); got != "arrapi: decode "+decodePath+": "+want {
			t.Errorf("decode(%q) = %q, want %q", body, got, want)
		}
		if errors.Unwrap(err) != nil {
			t.Errorf("decode(%q) wraps %v, want nothing", body, errors.Unwrap(err))
		}
	}
}

// TestDecodeErrorFieldOnlyFromTheType pins that a field path keeps only JSON names
// out's type declares, never a Go name, an unexported field's name or a segment the
// input chose.
func TestDecodeErrorFieldOnlyFromTheType(t *testing.T) {
	names := declaredNames(reflect.TypeFor[[]decodeProbe]())
	for field, want := range map[string]string{
		"0.id":                    "id",
		"0.nested.count":          "nested.count",
		"3.ids.12":                "ids",
		"Nested":                  "",
		"wall":                    "",
		"0.date.loc":              "",
		"0.nested.2024abcdef":     "",
		"0.2024abcdef.id":         "",
		"20241234567890123456789": "",
		"":                        "",
	} {
		if got := fieldPath(field, names); got != want {
			t.Errorf("fieldPath(%q) = %q, want %q", field, got, want)
		}
	}
}

type decodeKeyed struct {
	//deadset:ignore DS1003 -- the decoder writes it; the test reads only the error the decode reports
	ByName map[string]decodeInner `json:"byName"`
}

// TestDecodeErrorFieldThroughTheDecoder pins the field path the toolchain's decoder
// really reports: it carries list indices and map keys, an empty key included, and a
// key the type does not declare drops the path.
func TestDecodeErrorFieldThroughTheDecoder(t *testing.T) {
	c := internalClient(t, "unrelated-client-key")
	for body, want := range map[string]string{
		`[{"ids":[1,2,"x"]}]`:                     "field ids: a JSON string cannot decode into int",
		`{"byName":{"":{"count":"x"}}}`:           "field byName.count: a JSON string cannot decode into int",
		`{"byName":{"count":{"count":"x"}}}`:      "field byName.count.count: a JSON string cannot decode into int",
		`{"byName":{"2024abcdef":{"count":"x"}}}`: "a JSON string cannot decode into int",
		`{"byName":{"a.b":{"count":"x"}}}`:        "a JSON string cannot decode into int",
		`{"byName":{"12":{"count":"x"}}}`:         "field byName.count: a JSON string cannot decode into int",
	} {
		var out any = &[]decodeProbe{}
		if body[0] == '{' {
			out = &decodeKeyed{}
		}
		readErr, err := c.readInto(io.NopCloser(strings.NewReader(body)), MaxListBytes, decodePath, out)
		if readErr != nil || err == nil {
			t.Errorf("readInto(%q) = %v, %v; want a decode error", body, readErr, err)
			continue
		}
		if got := err.Error(); got != "arrapi: decode "+decodePath+": "+want {
			t.Errorf("decode(%q) = %q, want %q", body, got, "arrapi: decode "+decodePath+": "+want)
		}
	}
}
