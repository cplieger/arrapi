package transport_test

import (
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

const coverPath = "/api/v3/mediacover/41/poster-250.jpg"

// serveFile answers 200 with the version header, Content-Type ct (none when ct is
// absent) and body.
func serveFile(ct, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Application-Version", v4)
		if ct == absent {
			w.Header()["Content-Type"] = nil
		} else {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

const absent = "\x00absent"

func getFile(t *testing.T, h http.HandlerFunc, limit int64) ([]byte, transport.Result) {
	t.Helper()
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(h)
	var out []byte
	r := c.GetFile(t.Context(), d, coverPath, limit, "image/jpeg", &out)
	if n := len(f.requests()); n != 1 {
		t.Errorf("server saw %d requests, want 1", n)
	}
	return out, r
}

func TestGetFileAcceptsTheMediaType(t *testing.T) {
	for name, ct := range map[string]string{
		"exact":               "image/jpeg",
		"with a parameter":    "image/jpeg; charset=binary",
		"in another case":     "IMAGE/JPEG",
		"with spaces":         "image/jpeg ; q=1",
		"with a quoted param": `image/jpeg; name="a;b"`,
	} {
		t.Run(name, func(t *testing.T) {
			out, r := getFile(t, serveFile(ct, "\xff\xd8\xff"), 1<<20)
			if r.Err != nil || r.Verdict != transport.VerdictAccept || !r.Accepted() {
				t.Fatalf("GetFile = (err %v, verdict %v), want accepted", r.Err, r.Verdict)
			}
			if string(out) != "\xff\xd8\xff" {
				t.Errorf("out = %q, want the three bytes sent", out)
			}
		})
	}
}

func TestGetFileRefusesAnotherMediaType(t *testing.T) {
	for name, tc := range map[string]struct{ header, want string }{
		"html":           {"text/html; charset=utf-8", "text/html"},
		"octet-stream":   {"application/octet-stream", "application/octet-stream"},
		"another image":  {"image/png", "image/png"},
		"absent":         {absent, ""},
		"unparsable":     {"image/jpeg,", "image/jpeg,"},
		"a bad param":    {"image/jpeg; =x", "image/jpeg"},
		"a jpeg subtype": {"image/jpeg2000", "image/jpeg2000"},
	} {
		t.Run(name, func(t *testing.T) {
			out, r := getFile(t, serveFile(tc.header, "\xff\xd8\xff"), 1<<20)
			de, ok := errors.AsType[*arrapi.DecodeError](r.Err)
			if !ok {
				t.Fatalf("GetFile err = %v (verdict %v), want *arrapi.DecodeError", r.Err, r.Verdict)
			}
			if de.Status != http.StatusOK || de.Method != http.MethodGet || de.ContentType != tc.want || de.Path != coverPath {
				t.Errorf("DecodeError = {Status %d, Method %q, ContentType %q, Path %q}, want {200, GET, %q, %q}",
					de.Status, de.Method, de.ContentType, de.Path, tc.want, coverPath)
			}
			if want := "arrapi: decode " + coverPath + ": the answer is not image/jpeg"; de.Error() != want {
				t.Errorf("Error() = %q, want %q", de.Error(), want)
			}
			if r.Accepted() || out != nil {
				t.Errorf("GetFile = (out %q, accepted %v), want no bytes, not accepted", out, r.Accepted())
			}
		})
	}
}

func TestGetFileHoldsTheLimit(t *testing.T) {
	const limit = 8
	chunked := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Application-Version", v4)
			w.Header().Set("Content-Type", "image/jpeg")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			_, _ = w.Write([]byte(body))
		}
	}
	declared := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			serveFile("image/jpeg", body)(w, r)
		}
	}
	t.Run("exactly the limit", func(t *testing.T) {
		out, r := getFile(t, declared("12345678"), limit)
		if !r.Accepted() || string(out) != "12345678" {
			t.Errorf("GetFile = (%q, err %v), want the 8 bytes", out, r.Err)
		}
	})
	t.Run("exactly the limit, chunked", func(t *testing.T) {
		out, r := getFile(t, chunked("12345678"), limit)
		if !r.Accepted() || string(out) != "12345678" {
			t.Errorf("GetFile = (%q, err %v), want the 8 bytes", out, r.Err)
		}
	})
	for name, h := range map[string]http.HandlerFunc{
		"one over, declared": declared("123456789"),
		"one over, chunked":  chunked("123456789"),
		"far over, declared": declared(string(make([]byte, 64<<10))),
	} {
		t.Run(name, func(t *testing.T) {
			out, r := getFile(t, h, limit)
			e, ok := errors.AsType[*arrapi.ResponseTooLargeError](r.Err)
			if !ok || e.Limit != limit || e.Path != coverPath {
				t.Fatalf("GetFile err = %v, want *arrapi.ResponseTooLargeError{Path %s, Limit %d}", r.Err, coverPath, limit)
			}
			if r.Verdict != transport.VerdictAccept || r.Next != transport.NextDone || out != nil {
				t.Errorf("GetFile = (out %q, verdict %v, next %v), want no bytes, accept, done", out, r.Verdict, r.Next)
			}
		})
	}
}

func TestGetFileRefusesAnEmptyBody(t *testing.T) {
	out, r := getFile(t, serveFile("image/jpeg", ""), 1<<20)
	de, ok := errors.AsType[*arrapi.DecodeError](r.Err)
	if !ok {
		t.Fatalf("GetFile err = %v, want *arrapi.DecodeError", r.Err)
	}
	if want := "arrapi: decode " + coverPath + ": the answer is empty"; de.Error() != want || de.ContentType != "image/jpeg" {
		t.Errorf("DecodeError = (%q, ContentType %q), want (%q, image/jpeg)", de.Error(), de.ContentType, want)
	}
	if out != nil {
		t.Errorf("out = %q, want nil", out)
	}
}

func TestGetFileOtherVerdicts(t *testing.T) {
	cut := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Application-Version", v4)
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("\xff\xd8\xff"))
	}
	for name, tc := range map[string]struct {
		h       http.HandlerFunc
		verdict transport.Verdict
		err     bool
	}{
		"an attributed 404":  {reply(404, v4, `{"message":"NotFound"}`), transport.VerdictAbsent, false},
		"a header-less 200":  {reply(200, "", "\xff\xd8\xff"), transport.VerdictNoHeader, true},
		"an attributed 500":  {reply(500, v4, "boom"), transport.VerdictStatus, true},
		"a cut body":         {cut, transport.VerdictTransport, true},
		"a 200 from Sonarr5": {reply(200, v5, "\xff\xd8\xff"), transport.VerdictChanged, true},
	} {
		t.Run(name, func(t *testing.T) {
			out, r := getFile(t, tc.h, 1<<20)
			if r.Verdict != tc.verdict || (r.Err != nil) != tc.err {
				t.Errorf("GetFile = (verdict %v, err %v), want (%v, error %v)", r.Verdict, r.Err, tc.verdict, tc.err)
			}
			if r.Accepted() || out != nil {
				t.Errorf("GetFile = (out %q, accepted %v), want no bytes, not accepted", out, r.Accepted())
			}
		})
	}
}
