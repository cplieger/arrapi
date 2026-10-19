package transport_test

import (
	"context"
	"errors"
	"fmt"
	"io"
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

func TestEachMethodSendsOneAttempt(t *testing.T) {
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{MaxAttempts: 3})
	d := detect(t, c, f, v4)
	f.set(reply(503, v4, `busy`))
	var out any
	results := map[string]transport.Result{
		"GetJSON": c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out),
		"GetByID": c.GetByID(t.Context(), d, "/api/v3/series/1", &out),
		"Post":    c.Post(t.Context(), d, "/api/v3/command", map[string]any{}),
	}
	_, results["Probe"] = c.Probe(t.Context(), transport.AnchoredRun(d), "/api/v3/system/status", &out)
	if n := len(f.requests()); n != 4 {
		t.Errorf("server saw %d requests for four 503s, want 4: transport never retries", n)
	}
	for name, r := range results {
		want := transport.NextRetry
		if name == "Post" {
			want = transport.NextDone
		}
		if r.Verdict != transport.VerdictStatus || r.Next != want || !r.Sent {
			t.Errorf("%s = (%v, %v, sent %v), want (VerdictStatus, %v, sent)", name, r.Verdict, r.Next, r.Sent, want)
		}
	}
}

func TestOutUntouchedOffAccept(t *testing.T) {
	tests := []struct {
		name    string
		h       http.HandlerFunc
		verdict transport.Verdict
	}{
		{"status", reply(500, v4, `[{"id":1}]`), transport.VerdictStatus},
		{"unidentified", reply(404, "", `[{"id":1}]`), transport.VerdictUnidentified},
		{"changed", reply(200, v5, `[{"id":1}]`), transport.VerdictChanged},
		{"no header", reply(200, "", `[{"id":1}]`), transport.VerdictNoHeader},
		{"a valid partial 206", reply(206, v4, `[{"id":1}]`), transport.VerdictStatus},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(tc.h)
			out := []map[string]any{{"untouched": true}}
			res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
			if res.Verdict != tc.verdict {
				t.Errorf("verdict = %v, want %v", res.Verdict, tc.verdict)
			}
			if len(out) != 1 || out[0]["untouched"] != true {
				t.Errorf("out = %v, want it untouched", out)
			}
		})
	}
}

func TestGetByIDAbsentOnlyOnAnAttributed404(t *testing.T) {
	tests := []struct {
		name    string
		h       http.HandlerFunc
		verdict transport.Verdict
		next    transport.Next
		status  int // the *StatusError code reachable from Err; 0 for a nil Err
	}{
		{"404 with the sent header", reply(404, v4, `{"title":"Not Found"}`), transport.VerdictAbsent, transport.NextDone, 0},
		{"404 with the sent header and an empty body", reply(404, v4, ``), transport.VerdictAbsent, transport.NextDone, 0},
		{"404 without the header", reply(404, "", `<html>not found</html>`), transport.VerdictUnidentified, transport.NextDone, 404},
		{"404 from another version", reply(404, "4.0.21.1", ``), transport.VerdictChanged, transport.NextFresh, 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(tc.h)
			out := map[string]any{"untouched": true}
			res := c.GetByID(t.Context(), d, "/api/v3/series/99999999", &out)
			if res.Verdict != tc.verdict || res.Next != tc.next {
				t.Errorf("result = (%v, %v), want (%v, %v)", res.Verdict, res.Next, tc.verdict, tc.next)
			}
			if tc.status == 0 && res.Err != nil {
				t.Errorf("Err = %v, want nil for an absent item", res.Err)
			}
			if tc.status != 0 {
				if se, ok := errors.AsType[*arrapi.StatusError](res.Err); !ok || se.Code != tc.status {
					t.Errorf("Err = %v, want a reachable *StatusError %d", res.Err, tc.status)
				}
			}
			if out["untouched"] != true {
				t.Errorf("out = %v, want it untouched", out)
			}
		})
	}
}

func TestChangedCarriesTheFailureAsStatus(t *testing.T) {
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	check := func(t *testing.T, r transport.Result, code int) {
		t.Helper()
		sc, ok := errors.AsType[*arrapi.ServerChangedError](r.Err)
		if !ok || r.Verdict != transport.VerdictChanged {
			t.Fatalf("Err = %v (verdict %v), want *ServerChangedError", r.Err, r.Verdict)
		}
		if sc.From != v4 || sc.To != v5 {
			t.Errorf("From %q To %q, want %q to %q", sc.From, sc.To, v4, v5)
		}
		if code == 0 {
			if sc.Status != nil {
				t.Errorf("Status = %v, want nil for a success", sc.Status)
			}
			return
		}
		if se, ok := errors.AsType[*arrapi.StatusError](r.Err); !ok || se.Code != code || sc.Status == nil {
			t.Errorf("Status = %v, want a *StatusError %d reachable through errors.As", sc.Status, code)
		}
	}
	var out any
	cases := []struct {
		name string
		code int
		send func(*transport.Detection) transport.Result
	}{
		{"row 8 read 503", 503, func(d *transport.Detection) transport.Result {
			return c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		}},
		{"row 8 read 206", 206, func(d *transport.Detection) transport.Result {
			return c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		}},
		{"row 8 read 200", 0, func(d *transport.Detection) transport.Result {
			return c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		}},
		{"row 10 mutation 500", 500, func(d *transport.Detection) transport.Result {
			return c.Post(t.Context(), d, "/api/v3/command", map[string]any{})
		}},
		{"row 12 anchored probe 500", 500, func(d *transport.Detection) transport.Result {
			_, r := c.Probe(t.Context(), transport.AnchoredRun(d), "/api/v3/system/status", &out)
			return r
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := detect(t, c, f, v4)
			code := tc.code
			if code == 0 {
				code = 200
			}
			f.set(reply(code, v5, `{"message":"from five"}`))
			check(t, tc.send(d), tc.code)
		})
	}
}

func TestCommandOutcomeUnknown(t *testing.T) {
	tests := []struct {
		name   string
		v      string
		asType func(error) bool
	}{
		{"row 6 2xx without the header", "", func(err error) bool {
			e, ok := errors.AsType[*arrapi.UnsupportedAPIError](err)
			return ok && e.Reason == arrapi.ReasonNoVersionHeader
		}},
		{"row 11 2xx from another version", v5, func(err error) bool {
			e, ok := errors.AsType[*arrapi.ServerChangedError](err)
			return ok && e.To == v5 && e.Status == nil
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(reply(201, tc.v, `{"id":1}`))
			res := c.Post(t.Context(), d, "/api/v3/command", map[string]any{})
			if res.Verdict != transport.VerdictUnconfirmed || res.Next != transport.NextDone {
				t.Errorf("result = (%v, %v), want (VerdictUnconfirmed, NextDone)", res.Verdict, res.Next)
			}
			if !errors.Is(res.Err, arrapi.ErrCommandOutcomeUnknown) || !tc.asType(res.Err) {
				t.Errorf("Err = %v, want ErrCommandOutcomeUnknown over its cause", res.Err)
			}
			if !strings.HasPrefix(res.Err.Error(), "arrapi: the command's outcome is unknown and it was not re-sent: arrapi: ") {
				t.Errorf("Error() = %q", res.Err.Error())
			}
		})
	}
}

// TestChangedVersionTextIsPresented: a key of digits and dots is a well-formed
// X-Application-Version, so a server echoing it there must not print it as To.
func TestChangedVersionTextIsPresented(t *testing.T) {
	const key = "7.3.1.9"
	f := newFake(t, nil)
	c, err := transport.New(f.srv.URL, key, transport.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	d := detect(t, c, f, v4)
	f.set(reply(200, key, `[]`))
	var out any
	changed := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
	refused := c.GetJSON(t.Context(), d, "/api/v3/tag", transport.MaxListBytes, &out)
	for name, r := range map[string]transport.Result{"changed": changed, "refused": refused} {
		sc, ok := errors.AsType[*arrapi.ServerChangedError](r.Err)
		if !ok {
			t.Fatalf("%s Err = %v, want *ServerChangedError", name, r.Err)
		}
		if sc.To != "[redacted]" {
			t.Errorf("%s To = %q, want the echoed key redacted", name, sc.To)
		}
		assertChainClean(t, r.Err, key)
	}
}

func TestNoHeaderSuccessIsRefused(t *testing.T) {
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(reply(200, "", `[]`))
	var out []any
	res := c.GetJSON(t.Context(), d, "/api/v3/tag?x=1", transport.MaxListBytes, &out)
	e, ok := errors.AsType[*arrapi.UnsupportedAPIError](res.Err)
	if !ok || e.Reason != arrapi.ReasonNoVersionHeader || e.Want != "Sonarr" || e.Path != "/api/v3/tag" {
		t.Errorf("Err = %#v, want ReasonNoVersionHeader for Sonarr at /api/v3/tag", res.Err)
	}
}

func TestBodyReadFailureIsATransportOutcome(t *testing.T) {
	t.Run("connection reset after the header", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(func(w http.ResponseWriter, _ *http.Request) {
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nX-Application-Version: " + v4 + "\r\nContent-Length: 1000\r\n\r\n[{\"id\":1},")
			_ = buf.Flush()
			_ = conn.Close()
		})
		out := []any{"untouched"}
		res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		if res.Verdict != transport.VerdictTransport || res.Next != transport.NextRetry {
			t.Errorf("result = (%v, %v), want a transient VerdictTransport with NextRetry", res.Verdict, res.Next)
		}
		if res.Err == nil || !strings.HasPrefix(res.Err.Error(), "arrapi: request /api/v3/series: ") {
			t.Errorf("Err = %v, want the safe transport text", res.Err)
		}
	})
	stall := func(t *testing.T) (*transport.Client, *transport.Detection) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Application-Version", v4)
			if r.URL.Path == "/api" {
				_, _ = w.Write([]byte(`{}`))
				return
			}
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[`))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
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
	t.Run("body stalled past WithTimeout under a live caller", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			c, d := stall(t)
			var out []any
			res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
			if res.Verdict != transport.VerdictTransport || res.Next != transport.NextRetry {
				t.Errorf("result = (%v, %v), want VerdictTransport with NextRetry", res.Verdict, res.Next)
			}
		})
	})
	t.Run("body stalled past the caller deadline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			c, d := stall(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			var out []any
			res := c.GetJSON(ctx, d, "/api/v3/series", transport.MaxListBytes, &out)
			if res.Verdict != transport.VerdictTransport || res.Next != transport.NextDone {
				t.Errorf("result = (%v, %v), want VerdictTransport with NextDone", res.Verdict, res.Next)
			}
		})
	})
}

// countingBody is an endless chunked body that counts the bytes read from it.
type countingBody struct {
	read   *atomic.Int64
	closed *atomic.Bool
}

func (b countingBody) Read(p []byte) (int, error) {
	if b.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	n := min(len(p), 8<<20-int(b.read.Load()))
	if n <= 0 {
		return 0, io.EOF
	}
	for i := range n {
		p[i] = 'x'
	}
	b.read.Add(int64(n))
	return n, nil
}

func (b countingBody) Close() error { b.closed.Store(true); return nil }

func TestDiscardedBodyIsBounded(t *testing.T) {
	type drained struct {
		name    string
		code    int
		v       string
		call    transport.Call
		verdict transport.Verdict
	}
	rows := []drained{
		{"row 2 mutation success", 201, v4, transport.CallMutation, transport.VerdictAccept},
		{"row 3 absent", 404, v4, transport.CallReadByID, transport.VerdictAbsent},
		{"row 5 no header", 200, "", transport.CallRead, transport.VerdictNoHeader},
		{"row 6 command without the header", 201, "", transport.CallMutation, transport.VerdictUnconfirmed},
		{"row 8 success from another version", 200, v5, transport.CallRead, transport.VerdictChanged},
		{"row 11 command from another version", 201, v5, transport.CallMutation, transport.VerdictUnconfirmed},
		{"row 12 probe success from another version", 200, v5, transport.CallProbe, transport.VerdictChanged},
	}
	for _, declared := range []bool{false, true} {
		for _, tc := range rows {
			t.Run(fmt.Sprintf("%s declared %v", tc.name, declared), func(t *testing.T) {
				var read atomic.Int64
				var closed, armed atomic.Bool
				rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
					if !armed.Load() {
						return &http.Response{StatusCode: http.StatusOK, Header: hdr(v4), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
					}
					length := int64(-1)
					if declared {
						length = 8 << 20
					}
					body := countingBody{read: &read, closed: &closed}
					return &http.Response{StatusCode: tc.code, Header: hdr(tc.v), ContentLength: length, Body: body, Request: req}, nil
				})
				c := newClient(t, "http://sonarr:8989", transport.Config{HTTPClient: &http.Client{Transport: rt}})
				r := transport.NewProbeRun()
				var out any
				c.Probe(t.Context(), r, "/api", &out)
				epoch, _ := r.Anchor()
				d := &transport.Detection{Identity: transport.Identity{App: "Sonarr", AppVersion: v4}, Epoch: epoch}
				armed.Store(true)
				var res transport.Result
				switch tc.call {
				case transport.CallMutation:
					res = c.Post(t.Context(), d, "/api/v3/command", map[string]any{})
				case transport.CallReadByID:
					res = c.GetByID(t.Context(), d, "/api/v3/series/1", &out)
				case transport.CallProbe:
					_, res = c.Probe(t.Context(), transport.AnchoredRun(d), "/api/v3/system/status", &out)
				default:
					res = c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
				}
				if res.Verdict != tc.verdict {
					t.Fatalf("verdict = %v, want %v", res.Verdict, tc.verdict)
				}
				limit := int64(2048)
				if declared {
					limit = 0
				}
				if n := read.Load(); n > limit {
					t.Errorf("read %d bytes of a discarded body, want at most %d", n, limit)
				}
				if !closed.Load() {
					t.Error("discarded body was not closed")
				}
			})
		}
	}
}
