package transport_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

func TestEveryRequestCarriesKeyAndUserAgent(t *testing.T) {
	var keys, agents []string
	f := newFake(t, nil)
	f.set(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("X-Api-Key"))
		agents = append(agents, r.Header.Get("User-Agent"))
		reply(200, v4, `{"appName":"Sonarr","version":"4"}`)(w, r)
	})
	c := newClient(t, f.srv.URL, transport.Config{})
	r := transport.NewProbeRun()
	var out map[string]any
	c.Probe(t.Context(), r, "/api", &out)
	epoch, _ := r.Anchor()
	d := &transport.Detection{Identity: transport.Identity{App: "Sonarr", AppVersion: v4}, Epoch: epoch}
	c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
	c.GetByID(t.Context(), d, "/api/v3/series/1", &out)
	c.Post(t.Context(), d, "/api/v3/command", map[string]any{"name": "RescanSeries"})
	if len(keys) != 4 {
		t.Fatalf("server saw %d requests, want 4", len(keys))
	}
	for i := range keys {
		if keys[i] != string(testKey) {
			t.Errorf("request %d X-Api-Key = %q, want the key", i, keys[i])
		}
		if !strings.Contains(agents[i], "arrapi") {
			t.Errorf("request %d User-Agent = %q, want it to name arrapi", i, agents[i])
		}
	}
}

func TestProbeMetaAppVersion(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
		major   int
		verdict transport.Verdict
	}{
		{"200 with the header", reply(200, v4, `{}`), v4, 4, transport.VerdictAccept},
		{"404 with the header", reply(404, "5.0.0.2635", ``), "5.0.0.2635", 5, transport.VerdictStatus},
		{"200 without the header", reply(200, "", `{}`), "", 0, transport.VerdictAccept},
		{"200 with a malformed header", reply(200, "abc", `{}`), "", 0, transport.VerdictAccept},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, tc.handler)
			c := newClient(t, f.srv.URL, transport.Config{})
			var out map[string]any
			meta, res := c.Probe(t.Context(), transport.NewProbeRun(), "/api/v5/system/status", &out)
			if meta.AppVersion != tc.want || meta.Major != tc.major {
				t.Errorf("Meta = %+v, want AppVersion %q major %d", meta, tc.want, tc.major)
			}
			if res.Verdict != tc.verdict {
				t.Errorf("verdict = %v, want %v", res.Verdict, tc.verdict)
			}
		})
	}
}

func TestGetSucceedsOnlyOn200(t *testing.T) {
	for _, code := range []int{200, 201, 203, 204, 206} {
		t.Run(fmt.Sprintf("HTTP %d", code), func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(reply(code, v4, `[{"id":1}]`))
			out := []map[string]any{{"untouched": true}}
			res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
			byID := map[string]any{"untouched": true}
			resByID := c.GetByID(t.Context(), d, "/api/v3/series/1", &byID)
			if code == 200 {
				if res.Err != nil {
					t.Fatalf("GetJSON 200: %v", res.Err)
				}
				if len(out) != 1 || out[0]["id"] != float64(1) {
					t.Errorf("GetJSON 200 decoded %v", out)
				}
				return
			}
			for name, r := range map[string]transport.Result{"GetJSON": res, "GetByID": resByID} {
				se, ok := errors.AsType[*arrapi.StatusError](r.Err)
				if !ok || se.Code != code {
					t.Errorf("%s error = %v, want *StatusError %d", name, r.Err, code)
				}
			}
			if out[0]["untouched"] != true || byID["untouched"] != true {
				t.Errorf("out was written on a %d: %v %v", code, out, byID)
			}
		})
	}
}

func TestPostThroughTheCommandClient(t *testing.T) {
	t.Run("body and content type", func(t *testing.T) {
		var got map[string]any
		var ctype string
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(func(w http.ResponseWriter, r *http.Request) {
			ctype = r.Header.Get("Content-Type")
			_ = json.NewDecoder(r.Body).Decode(&got)
			reply(201, v4, `{"id":7}`)(w, r)
		})
		res := c.Post(t.Context(), d, "/api/v3/command", map[string]any{"name": "RescanSeries", "seriesId": 3})
		if res.Err != nil || res.Verdict != transport.VerdictAccept {
			t.Fatalf("Post = %v (verdict %v), want accepted", res.Err, res.Verdict)
		}
		if got["name"] != "RescanSeries" || got["seriesId"] != float64(3) {
			t.Errorf("server decoded %v", got)
		}
		if ctype != "application/json" {
			t.Errorf("Content-Type = %q", ctype)
		}
	})
	for _, code := range []int{200, 201, 202, 204} {
		t.Run(fmt.Sprintf("HTTP %d accepted", code), func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(reply(code, v4, ``))
			if res := c.Post(t.Context(), d, "/api/v3/command", map[string]any{}); res.Err != nil || res.Verdict != transport.VerdictAccept {
				t.Errorf("Post %d = %v (verdict %v), want accepted", code, res.Err, res.Verdict)
			}
		})
	}
	t.Run("sent once on a 503", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{MaxAttempts: 3})
		d := detect(t, c, f, v4)
		f.set(reply(503, v4, `busy`))
		res := c.Post(t.Context(), d, "/api/v3/command", map[string]any{})
		if res.Next != transport.NextDone {
			t.Errorf("Next = %v, want NextDone", res.Next)
		}
		if n := len(f.requests()); n != 1 {
			t.Errorf("server saw %d POSTs, want 1", n)
		}
	})
}

func TestBodyCaps(t *testing.T) {
	tests := []struct {
		name  string
		size  int
		limit int64
		byID  bool
	}{
		{"list over 64 MiB", 64<<20 + 16, transport.MaxListBytes, false},
		{"object over 1 MiB", 1<<20 + 16, transport.MaxObjectBytes, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(reply(200, v4, `"`+strings.Repeat("x", tc.size)+`"`))
			var out string
			var res transport.Result
			if tc.byID {
				res = c.GetByID(t.Context(), d, "/api/v3/series/1", &out)
			} else {
				res = c.GetJSON(t.Context(), d, "/api/v3/series", tc.limit, &out)
			}
			e, ok := errors.AsType[*arrapi.ResponseTooLargeError](res.Err)
			if !ok || e.Limit != tc.limit {
				t.Fatalf("error = %v, want *ResponseTooLargeError at %d", res.Err, tc.limit)
			}
			if res.Next != transport.NextDone || out != "" {
				t.Errorf("Next = %v, out %d bytes; want NextDone and nothing decoded", res.Next, len(out))
			}
		})
	}
	t.Run("probe over 1 MiB", func(t *testing.T) {
		f := newFake(t, reply(200, v4, `"`+strings.Repeat("x", 1<<20)+`"`))
		c := newClient(t, f.srv.URL, transport.Config{})
		var out string
		_, res := c.Probe(t.Context(), transport.NewProbeRun(), "/api", &out)
		if _, ok := errors.AsType[*arrapi.ResponseTooLargeError](res.Err); !ok {
			t.Errorf("error = %v, want *ResponseTooLargeError", res.Err)
		}
	})
}

func TestMalformedJSONIsADecodeError(t *testing.T) {
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(reply(200, v4, `{not json`))
	var out []any
	res := c.GetJSON(t.Context(), d, "/api/v3/series?x=1", transport.MaxListBytes, &out)
	if res.Err == nil || !strings.HasPrefix(res.Err.Error(), "arrapi: decode /api/v3/series: ") {
		t.Errorf("error = %v, want arrapi: decode /api/v3/series: ...", res.Err)
	}
	if res.Next != transport.NextDone {
		t.Errorf("Next = %v, want NextDone", res.Next)
	}
}

func TestNullAnswerIsADecodeError(t *testing.T) {
	type movie struct {
		Title string `json:"title"`
	}
	for name, body := range map[string]string{"null": `null`, "null in JSON whitespace": " \r\n\tnull \n"} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(reply(200, v4, body))
			list := []movie{{Title: "kept"}}
			res := c.GetJSON(t.Context(), d, "/api/v3/movie?x=1", transport.MaxListBytes, &list)
			if want := "arrapi: decode /api/v3/movie: the answer is null"; res.Err == nil || res.Err.Error() != want {
				t.Errorf("GetJSON error = %v, want %q", res.Err, want)
			}
			if res.Verdict != transport.VerdictAccept || res.Next != transport.NextDone || res.Accepted() {
				t.Errorf("GetJSON = (verdict %v, next %v, accepted %v), want an accepted answer that failed to decode, done", res.Verdict, res.Next, res.Accepted())
			}
			if len(list) != 1 || list[0].Title != "kept" {
				t.Errorf("GetJSON wrote %+v into out, want it untouched", list)
			}
			one := movie{Title: "kept"}
			res = c.GetByID(t.Context(), d, "/api/v3/movie/9", &one)
			if want := "arrapi: decode /api/v3/movie/9: the answer is null"; res.Err == nil || res.Err.Error() != want {
				t.Errorf("GetByID error = %v, want %q", res.Err, want)
			}
			if one.Title != "kept" {
				t.Errorf("GetByID wrote %+v into out, want it untouched", one)
			}
		})
	}
}

// TestGetJSONDecodeErrorCarriesNoServerText pins, through GetJSON, that a decode error
// renders none of the value the server sent and wraps nothing. A key opening with
// four digits parses as a year, after which time.ParseError quotes the rest of it.
func TestGetJSONDecodeErrorCarriesNoServerText(t *testing.T) {
	const path = "/api/v3/history/since"
	type record struct {
		Date time.Time `json:"date"`
		ID   int       `json:"id"`
	}
	tests := []struct {
		name, key, body, want string
	}{
		{"a key echoed into a date field", string(testKey), `[{"date":"` + string(testKey) + `"}]`, "a date-time is not RFC 3339"},
		{
			"a digit-prefixed key echoed into a date field", "20241f3c9a7b4e2d8c6a5b3f1e0d9c8b", // gitleaks:allow
			`[{"date":"20241f3c9a7b4e2d8c6a5b3f1e0d9c8b"}]`, "a date-time is not RFC 3339", // gitleaks:allow
		},
		{"a key echoed escaped, which decoding rebuilds", string(testKey), `[{"date":"` + strings.ReplaceAll(string(testKey), "-", `\u002d`) + `"}]`, "a date-time is not RFC 3339"},
		{"a digits key echoed as a number", "20241234567890123456789012345678", `[{"id":20241234567890123456789012345678}]`, "field id: a JSON number cannot decode into int"},
		{"a long multi-line value", string(testKey), `[{"date":"` + strings.Repeat(`x\n\u202e`, 20000) + `"}]`, "a date-time is not RFC 3339"},
		{"malformed JSON", string(testKey), `{not json`, "invalid JSON at byte 2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, nil)
			c, err := transport.New(f.srv.URL, arrapi.APIKey(tc.key), transport.Config{BaseDelay: time.Millisecond})
			if err != nil {
				t.Fatalf("transport.New: %v", err)
			}
			t.Cleanup(c.Close)
			d := detect(t, c, f, v4)
			f.set(reply(200, v4, tc.body))
			var out []record
			res := c.GetJSON(t.Context(), d, path+"?date=x", transport.MaxListBytes, &out)
			if res.Next != transport.NextDone {
				t.Errorf("Next = %v, want NextDone", res.Next)
			}
			if want := "arrapi: decode " + path + ": " + tc.want; res.Err == nil || res.Err.Error() != want {
				t.Fatalf("error = %v, want %q", res.Err, want)
			}
			if errors.Unwrap(res.Err) != nil {
				t.Errorf("error wraps %v, want nothing", errors.Unwrap(res.Err))
			}
			assertChainClean(t, res.Err, tc.key, tc.key[4:])
		})
	}
}

func TestLargeStreamedListDecodes(t *testing.T) {
	const n = 100000
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Application-Version", v4)
		bw := bufio.NewWriter(w)
		_, _ = bw.WriteString("[")
		for i := range n {
			if i > 0 {
				_, _ = bw.WriteString(",")
			}
			_, _ = fmt.Fprintf(bw, `{"id":%d,"title":"%s"}`, i, strings.Repeat("x", 60))
			if i%2000 == 0 {
				_ = bw.Flush()
				w.(http.Flusher).Flush()
			}
		}
		_, _ = bw.WriteString("]")
		_ = bw.Flush()
	})
	var out []struct{ ID int }
	if res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out); res.Err != nil {
		t.Fatalf("GetJSON: %v", res.Err)
	}
	if len(out) != n {
		t.Errorf("decoded %d items, want %d", len(out), n)
	}
}

func TestWithHTTPClientIsUsed(t *testing.T) {
	f := newFake(t, nil)
	var used atomic.Int64
	hc := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		used.Add(1)
		return http.DefaultTransport.RoundTrip(req)
	})}
	c := newClient(t, f.srv.URL, transport.Config{HTTPClient: hc})
	d := detect(t, c, f, v4)
	f.set(reply(200, v4, `[]`))
	var out []any
	c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
	c.Post(t.Context(), d, "/api/v3/command", map[string]any{})
	if n := used.Load(); n != 3 {
		t.Errorf("caller transport carried %d requests, want 3 (probe, read, command)", n)
	}
}

type closeCounter struct {
	http.RoundTripper
	closed atomic.Int64
}

func (c *closeCounter) CloseIdleConnections() { c.closed.Add(1) }

func TestCloseReachesTheCallerPool(t *testing.T) {
	cc := &closeCounter{RoundTripper: http.DefaultTransport}
	c, err := transport.New("http://sonarr:8989", testKey, transport.Config{HTTPClient: &http.Client{Transport: cc}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.Close()
	c.Close()
	if n := cc.closed.Load(); n != 2 {
		t.Errorf("CloseIdleConnections reached the caller transport %d times, want 2", n)
	}
}

func TestTimeouts(t *testing.T) {
	slow := func(d time.Duration) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(d):
			case <-r.Context().Done():
				return
			}
			reply(200, v4, `[{"id":1}]`)(w, r)
		}
	}
	setup := func(t *testing.T, h http.HandlerFunc) (*transport.Client, *transport.Detection) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api" {
				reply(200, v4, `{}`)(w, r)
				return
			}
			h(w, r)
		}))
		c, err := transport.New(srv.URL, testKey, transport.Config{HTTPClient: srv.Client(), Timeout: 20 * time.Second})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		r := transport.NewProbeRun()
		var out any
		c.Probe(t.Context(), r, "/api", &out)
		epoch, _ := r.Anchor()
		return c, &transport.Detection{Identity: transport.Identity{App: "Sonarr", AppVersion: v4}, Epoch: epoch}
	}
	t.Run("WithTimeout bounds an attempt as a transient error", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			c, d := setup(t, slow(200*time.Second))
			start := time.Now()
			var out []any
			res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
			if elapsed := time.Since(start); elapsed != 20*time.Second {
				t.Errorf("attempt ran %v, want exactly the 20s WithTimeout", elapsed)
			}
			if res.Verdict != transport.VerdictTransport || res.Next != transport.NextRetry {
				t.Errorf("result = (%v, %v), want a transient VerdictTransport with NextRetry", res.Verdict, res.Next)
			}
		})
	})
	t.Run("the caller deadline is authoritative", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			c, d := setup(t, slow(80*time.Second))
			ctx, cancel := context.WithTimeout(t.Context(), 500*time.Second)
			defer cancel()
			start := time.Now()
			var out []any
			if res := c.GetJSON(ctx, d, "/api/v3/series", transport.MaxListBytes, &out); res.Err != nil {
				t.Fatalf("GetJSON: %v", res.Err)
			}
			if elapsed := time.Since(start); elapsed != 80*time.Second {
				t.Errorf("attempt ran %v, want 80s under the caller's 500s deadline", elapsed)
			}
		})
	})
	t.Run("the caller deadline is terminal", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			c, d := setup(t, slow(200*time.Second))
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			start := time.Now()
			var out []any
			res := c.GetJSON(ctx, d, "/api/v3/series", transport.MaxListBytes, &out)
			if elapsed := time.Since(start); elapsed != 20*time.Second {
				t.Errorf("attempt ran %v, want the 20s caller deadline", elapsed)
			}
			if !errors.Is(res.Err, context.DeadlineExceeded) {
				t.Errorf("error = %v, want context.DeadlineExceeded", res.Err)
			}
			if res.Verdict != transport.VerdictTransport || res.Next != transport.NextDone {
				t.Errorf("result = (%v, %v), want VerdictTransport with NextDone", res.Verdict, res.Next)
			}
		})
	})
}
