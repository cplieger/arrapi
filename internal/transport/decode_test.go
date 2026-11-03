package transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

type siteRecord struct {
	//deadset:ignore DS1003 -- the decoder writes it; the test reads only the error the decode reports
	Date time.Time `json:"date"`
	//deadset:ignore DS1003 -- the decoder writes it; the test reads only the error the decode reports
	Tags []int `json:"tags"`
	//deadset:ignore DS1003 -- the decoder writes it; the test reads only the error the decode reports
	Shape shapeToken `json:"shape"`
	ID    int        `json:"id"`
}

// shapeToken refuses every value with an error of its own type, as an UnmarshalJSON
// method of any other type than the decoder's does.
type shapeToken struct{}

//deadset:ignore DS1103 -- encoding/json calls it for the shape field; readInto decodes through an attempt's any, which deadset cannot follow
func (*shapeToken) UnmarshalJSON([]byte) error { return errors.New("refused") }

// wantCause is the Err a DecodeError carries: kind "syntax" at offset, kind "type"
// with its kind word, field and Go type, or kind "" for nil.
type wantCause struct {
	kind, value, field, goType string
	offset                     int64
}

func checkCause(t *testing.T, got error, want wantCause) {
	t.Helper()
	switch want.kind {
	case "syntax":
		se, ok := got.(*json.SyntaxError)
		if !ok || se.Offset != want.offset {
			t.Errorf("Err = %#v, want a *json.SyntaxError at offset %d", got, want.offset)
		}
	case "type":
		te, ok := got.(*json.UnmarshalTypeError)
		if !ok {
			t.Errorf("Err = %#v, want a *json.UnmarshalTypeError", got)
			return
		}
		if te.Value != want.value || te.Field != want.field || te.Type == nil || te.Type.String() != want.goType || te.Err != nil {
			t.Errorf("Err = %#v, want Value %q, Field %q, Type %s and no Err", te, want.value, want.field, want.goType)
		}
	default:
		if got != nil {
			t.Errorf("Err = %#v, want nil", got)
		}
	}
}

// TestDecodeErrorAtEverySite pins, for each place an accepted answer fails to decode,
// the *arrapi.DecodeError a read returns: GET, the path without its query, status
// 200, arrapi's sentence and the scrubbed decoder error.
func TestDecodeErrorAtEverySite(t *testing.T) {
	const path = "/api/v3/episode"
	list := func(ctx context.Context, c *transport.Client, d *transport.Detection) error {
		var out []siteRecord
		return c.GetJSON(ctx, d, path+"?seriesId=1", transport.MaxListBytes, &out).Err
	}
	for _, tc := range []struct {
		name, body, want string
		read             func(context.Context, *transport.Client, *transport.Detection) error
		cause            wantCause
	}{
		{"invalid JSON", `[{"id":1}`, "invalid JSON at byte 9", list, wantCause{kind: "syntax", offset: 9}},
		{"a type error at the top level", `"x"`, "a JSON string cannot decode into int", func(ctx context.Context, c *transport.Client, d *transport.Detection) error {
			var n int
			return c.GetJSON(ctx, d, path+"?seriesId=1", transport.MaxObjectBytes, &n).Err
		}, wantCause{kind: "type", value: "string", goType: "int"}},
		{"a type error under a declared field", `[{"id":"x"}]`, "field id: a JSON string cannot decode into int", list, wantCause{kind: "type", value: "string", field: "id", goType: "int"}},
		{"a date that is not RFC 3339", `[{"date":"x"}]`, "a date-time is not RFC 3339", list, wantCause{}},
		{"a value its own type refuses", `[{"shape":{}}]`, "the answer does not match the expected shape", list, wantCause{}},
		{"a null answer", `null`, "the answer is null", list, wantCause{}},
		{"a null item of a declared array", `[{"id":1,"tags":[null]}]`, "field tags: item 0 is null", list, wantCause{}},
		{"a null item of the list", `[{"id":1},null]`, "list item 1 is null", func(ctx context.Context, c *transport.Client, d *transport.Detection) error {
			_, res := transport.GetList(ctx, c, d, path+"?seriesId=1", func(r *siteRecord) int { return r.ID })
			return res.Err
		}, wantCause{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(reply(200, v4, tc.body))
			err := tc.read(t.Context(), c, d)
			de, ok := errors.AsType[*arrapi.DecodeError](err)
			if !ok {
				t.Fatalf("error = %v (%T), want an *arrapi.DecodeError", err, err)
			}
			if want := "arrapi: decode " + path + ": " + tc.want; de.Error() != want {
				t.Errorf("Error() = %q, want %q", de.Error(), want)
			}
			if de.Method != http.MethodGet || de.Path != path || de.Status != http.StatusOK {
				t.Errorf("DecodeError = {Method %q, Path %q, Status %d}, want {GET, %s, 200}", de.Method, de.Path, de.Status, path)
			}
			checkCause(t, de.Err, tc.cause)
			if errors.Unwrap(de) != de.Err {
				t.Errorf("Unwrap() = %v, want Err %v", errors.Unwrap(de), de.Err)
			}
			if n := len(f.requests()); n != 1 {
				t.Errorf("sent %d requests, want 1", n)
			}
		})
	}
}

// TestDecodeErrorContentType pins ContentType: the media type as sent, parameters
// dropped, case kept, key redacted, at most 64 bytes, "" when the answer has none.
func TestDecodeErrorContentType(t *testing.T) {
	long := "application/" + strings.Repeat("a", 188)
	for _, tc := range []struct {
		name   string
		header []string
		want   string
	}{
		{"parameters dropped", []string{"application/json; charset=utf-8"}, "application/json"},
		{"spaces before the parameters trimmed", []string{"application/json \t; charset=utf-8"}, "application/json"},
		{"case kept", []string{"Text/HTML"}, "Text/HTML"},
		{"absent", nil, ""},
		{"the key redacted", []string{"application/x-" + string(testKey) + "; v=1"}, "application/x-[redacted]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			de := contentTypeDecodeError(t, tc.header)
			if de.ContentType != tc.want {
				t.Errorf("ContentType = %q, want %q", de.ContentType, tc.want)
			}
		})
	}
	t.Run("a 200-byte header cut to 64 bytes", func(t *testing.T) {
		de := contentTypeDecodeError(t, []string{long})
		if len(de.ContentType) != 64 || !strings.HasPrefix(de.ContentType, "application/aaaa") || strings.ContainsAny(de.ContentType, "\r\n") {
			t.Errorf("ContentType = %q (%d bytes), want the header's first bytes, cut to 64", de.ContentType, len(de.ContentType))
		}
	})
}

// contentTypeDecodeError reads a malformed answer sent with the Content-Type values
// header (none when nil) and returns its DecodeError.
func contentTypeDecodeError(t *testing.T, header []string) *arrapi.DecodeError {
	t.Helper()
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Application-Version", v4)
		w.Header()["Content-Type"] = header // nil also stops net/http sniffing one
		_, _ = w.Write([]byte(`{not json`))
	})
	var out []siteRecord
	res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
	de, ok := errors.AsType[*arrapi.DecodeError](res.Err)
	if !ok {
		t.Fatalf("error = %v (%T), want an *arrapi.DecodeError", res.Err, res.Err)
	}
	return de
}

// TestNeverADecodeError pins the answers whose body is not decoded, or whose read
// fails before the decoder runs: none of them is a DecodeError.
func TestNeverADecodeError(t *testing.T) {
	const path = "/api/v3/series"
	read := func(ctx context.Context, t *testing.T, h http.HandlerFunc) transport.Result {
		t.Helper()
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(h)
		var out []siteRecord
		return c.GetJSON(ctx, d, path, transport.MaxListBytes, &out)
	}
	noDecodeError := func(t *testing.T, err error) {
		t.Helper()
		if de, ok := errors.AsType[*arrapi.DecodeError](err); ok {
			t.Errorf("error = %v, holds an *arrapi.DecodeError %+v", err, de)
		}
	}
	t.Run("a 500 with a body that is not JSON", func(t *testing.T) {
		res := read(t.Context(), t, reply(500, v4, "<html>upstream down</html>"))
		se, ok := errors.AsType[*arrapi.StatusError](res.Err)
		if !ok || se.Code != 500 || se.Message != "" {
			t.Errorf("error = %v, want a 500 *arrapi.StatusError with no Message", res.Err)
		}
		noDecodeError(t, res.Err)
	})
	t.Run("a body over its cap", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(reply(200, v4, `{not json and longer than the cap`))
		var out []siteRecord
		res := c.GetJSON(t.Context(), d, path, 8, &out)
		if _, ok := errors.AsType[*arrapi.ResponseTooLargeError](res.Err); !ok {
			t.Errorf("error = %v, want an *arrapi.ResponseTooLargeError", res.Err)
		}
		noDecodeError(t, res.Err)
	})
	t.Run("a body cut by the connection", func(t *testing.T) {
		res := read(t.Context(), t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Application-Version", v4)
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":1`))
			w.(http.Flusher).Flush()
			conn, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		})
		if res.Err == nil || res.Verdict != transport.VerdictTransport {
			t.Errorf("Result = (verdict %v, err %v), want a transport error", res.Verdict, res.Err)
		}
		noDecodeError(t, res.Err)
	})
	t.Run("a context cancelled mid-body", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		res := read(ctx, t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Application-Version", v4)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":1`))
			w.(http.Flusher).Flush()
			cancel()
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		})
		if !errors.Is(res.Err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled in its chain", res.Err)
		}
		noDecodeError(t, res.Err)
	})
	t.Run("a 200 without X-Application-Version", func(t *testing.T) {
		res := read(t.Context(), t, reply(200, "", `{not json`))
		ue, ok := errors.AsType[*arrapi.UnsupportedAPIError](res.Err)
		if !ok || ue.Reason != arrapi.ReasonNoVersionHeader {
			t.Errorf("error = %v, want ReasonNoVersionHeader", res.Err)
		}
		noDecodeError(t, res.Err)
	})
	t.Run("a command's 2xx with a malformed body", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(reply(201, v4, `{not json`))
		res := c.Post(t.Context(), d, "/api/v3/command", map[string]string{"name": "RefreshSeries"})
		if res.Err != nil || res.Verdict != transport.VerdictAccept {
			t.Errorf("Post = (verdict %v, err %v), want an accepted command", res.Verdict, res.Err)
		}
	})
}

// TestDecodeErrorRedactsTheKey pins that a key the server echoes into a body reaches
// neither the DecodeError's text nor its Err, and that a cause whose text would
// render a short hand-set key is dropped while the error keeps its type and text.
func TestDecodeErrorRedactsTheKey(t *testing.T) {
	const path = "/api/v3/history/since"
	decode := func(t *testing.T, key arrapi.APIKey, body string, out any) *arrapi.DecodeError {
		t.Helper()
		f := newFake(t, nil)
		c, err := transport.New(f.srv.URL, key, transport.Config{BaseDelay: time.Millisecond})
		if err != nil {
			t.Fatalf("transport.New: %v", err)
		}
		t.Cleanup(c.Close)
		d := detect(t, c, f, v4)
		f.set(reply(200, v4, body))
		res := c.GetJSON(t.Context(), d, path, transport.MaxListBytes, out)
		de, ok := errors.AsType[*arrapi.DecodeError](res.Err)
		if !ok {
			t.Fatalf("error = %v (%T), want an *arrapi.DecodeError", res.Err, res.Err)
		}
		assertChainClean(t, res.Err, string(key))
		return de
	}
	t.Run("a map key equal to the key", func(t *testing.T) {
		const key = "map-key-echo-3e91"
		var out struct {
			M map[string]int `json:"m"`
		}
		de := decode(t, key, `{"m":{"`+key+`":"x"}}`, &out)
		if want := "arrapi: decode " + path + ": a JSON string cannot decode into int"; de.Error() != want {
			t.Errorf("Error() = %q, want %q", de.Error(), want)
		}
		checkCause(t, de.Err, wantCause{kind: "type", value: "string", goType: "int"})
	})
	t.Run("a date holding the key", func(t *testing.T) {
		const key = "2024-date-echo-key-77"
		var out struct {
			D time.Time `json:"d"`
		}
		de := decode(t, key, `{"d":"`+key+`"}`, &out)
		if want := "arrapi: decode " + path + ": a date-time is not RFC 3339"; de.Error() != want {
			t.Errorf("Error() = %q, want %q", de.Error(), want)
		}
		checkCause(t, de.Err, wantCause{})
	})
	t.Run("a 1-byte key the syntax error quotes", func(t *testing.T) {
		var out []siteRecord
		de := decode(t, "x", `{x`, &out)
		if want := "arrapi: decode " + path + ": invalid JSON at byte 2"; de.Error() != want {
			t.Errorf("Error() = %q, want %q", de.Error(), want)
		}
		if de.Err != nil || de.Status != http.StatusOK || de.Path != path {
			t.Errorf("DecodeError = {Err %v, Status %d, Path %q}, want {nil, 200, %s}", de.Err, de.Status, de.Path, path)
		}
	})
}
