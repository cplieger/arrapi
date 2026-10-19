package transport_test

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

func redirectTo(code int, loc, v string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if v != "" {
			w.Header().Set("X-Application-Version", v)
		}
		w.Header().Set("Location", loc)
		w.WriteHeader(code)
	}
}

// routes answers each path with its handler and 404 elsewhere.
func routes(m map[string]http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h, ok := m[r.URL.Path]; ok {
			h(w, r)
			return
		}
		w.WriteHeader(http.StatusTeapot)
	}
}

func TestObserveStoresOnlyOnContradiction(t *testing.T) {
	var out any
	read := func(c *transport.Client, d *transport.Detection) transport.Result {
		return c.GetJSON(t.Context(), d, "/api/v3/series?x=1", transport.MaxListBytes, &out)
	}
	stores := []struct {
		name string
		h    http.HandlerFunc
		want string // the stored AppVersion
		post bool
	}{
		{"another version", reply(200, v5, `[]`), v5, false},
		{"header-less 200", reply(200, "", `[]`), "", false},
		{"header-less 201 to a command", reply(201, "", ``), "", true},
		{"header-less 400", reply(400, "", ``), "", false},
		{"header-less 404", reply(404, "", ``), "", false},
		{"header-less 500", reply(500, "", ``), "", false},
		{"header-less terminal 300", redirectTo(300, "/x", ""), "", false},
		{"header-less terminal 304", reply(304, "", ``), "", false},
		{"malformed header", reply(200, "abc", `[]`), "", false},
	}
	for _, tc := range stores {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			before := c.State()
			f.set(tc.h)
			if tc.post {
				c.Post(t.Context(), d, "/api/v3/command", map[string]any{})
			} else {
				read(c, d)
			}
			s := c.State()
			wantPath := "/api/v3/series"
			if tc.post {
				wantPath = "/api/v3/command"
			}
			if s.Epoch != before.Epoch+1 || s.AppVersion != tc.want || s.Path != wantPath || s.Det != nil {
				t.Errorf("State = %+v, want epoch %d, version %q, path %s, no detection", s, before.Epoch+1, tc.want, wantPath)
			}
		})
	}
	nothing := []struct {
		name string
		h    http.HandlerFunc
	}{
		{"an agreeing answer", reply(200, v4, `[]`)},
		{"an agreeing failure", reply(500, v4, ``)},
	}
	for _, code := range []int{301, 302, 303, 307, 308} {
		nothing = append(nothing, struct {
			name string
			h    http.HandlerFunc
		}{fmt.Sprintf("a followed header-less %d", code), routes(map[string]http.HandlerFunc{
			"/api/v3/series": redirectTo(code, "/hop", ""),
			"/hop":           reply(200, v4, `[]`),
		})})
	}
	for _, tc := range nothing {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			before := c.State()
			f.set(tc.h)
			read(c, d)
			if c.State() != before {
				t.Errorf("State = %+v, want the same pointer %+v", c.State(), before)
			}
		})
	}
	t.Run("an unanchored run's header-less answer", func(t *testing.T) {
		f := newFake(t, reply(404, "", ``))
		c := newClient(t, f.srv.URL, transport.Config{})
		before := c.State()
		_, res := c.Probe(t.Context(), transport.NewProbeRun(), "/api", &out)
		if c.State() != before || res.Verdict != transport.VerdictStatus {
			t.Errorf("State %+v (verdict %v), want it unchanged and VerdictStatus", c.State(), res.Verdict)
		}
	})
	t.Run("an anchored run's header-less 500", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(reply(500, "", ``))
		_, res := c.Probe(t.Context(), transport.AnchoredRun(d), "/api/v3/system/status", &out)
		if s := c.State(); s.Epoch != d.Epoch+1 || s.AppVersion != "" || res.Next != transport.NextDone {
			t.Errorf("State %+v, Next %v; want the epoch advanced with no version and NextDone", s, res.Next)
		}
	})
	t.Run("a dial failure", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		before := c.State()
		f.srv.Close()
		if res := read(c, d); res.Verdict != transport.VerdictTransport || c.State() != before {
			t.Errorf("verdict %v, State %+v; want VerdictTransport and the State unchanged", res.Verdict, c.State())
		}
	})
	t.Run("a header-less answer after another one stores again", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		release, unblock := releaser(t)
		f.set(routes(map[string]http.HandlerFunc{
			"/api/v3/held": func(w http.ResponseWriter, r *http.Request) {
				<-release
				reply(500, "", ``)(w, r)
			},
			"/api/v3/series": reply(503, "", ``),
		}))
		var wg sync.WaitGroup
		wg.Go(func() { c.GetJSON(t.Context(), d, "/api/v3/held", transport.MaxListBytes, &out) })
		waitFor(t, f, "GET /api/v3/held")
		first := read(c, d)
		if s := c.State(); s.Epoch != d.Epoch+1 || first.SelfMark != s.Epoch {
			t.Fatalf("State %+v, SelfMark %d; want the read's own mark", s, first.SelfMark)
		}
		unblock()
		wg.Wait()
		if s := c.State(); s.Epoch != d.Epoch+2 || s.AppVersion != "" {
			t.Errorf("State = %+v, want epoch %d: an attempt admitted at an older epoch stores again", s, d.Epoch+2)
		}
	})
}

// waitFor blocks until f has logged want.
func waitFor(t *testing.T, f *fake, want string) {
	t.Helper()
	for range 2000 {
		if slices.Contains(f.requests(), want) {
			return
		}
		yield()
	}
	t.Fatalf("server never saw %q (log %v)", want, f.requests())
}

func TestSelfMarkAdmitsOnlyOwnRetry(t *testing.T) {
	var out []any
	setup := func(t *testing.T) (*fake, *transport.Client, *transport.Detection, transport.Result) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(reply(503, "", `busy`))
		first := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		if first.Verdict != transport.VerdictUnidentified || first.Next != transport.NextFresh || first.SelfMark != d.Epoch+1 {
			t.Fatalf("first answer = (%v, %v, mark %d), want Unidentified, Fresh and mark %d", first.Verdict, first.Next, first.SelfMark, d.Epoch+1)
		}
		f.reset()
		return f, c, d, first
	}
	t.Run("the retry under the mark is sent and decoded", func(t *testing.T) {
		f, c, d, first := setup(t)
		f.set(reply(200, v4, `[1]`))
		res := c.GetJSON(t.Context(), d.UnderSelfMark(first.SelfMark), "/api/v3/series", transport.MaxListBytes, &out)
		if res.Err != nil || res.Verdict != transport.VerdictAccept || len(out) != 1 {
			t.Fatalf("retry = %v (verdict %v, out %v), want decoded", res.Err, res.Verdict, out)
		}
		if s := c.State(); s.Epoch != first.SelfMark+1 || s.AppVersion != v4 || s.Det != nil {
			t.Errorf("State = %+v, want epoch %d naming %s with no detection", s, first.SelfMark+1, v4)
		}
	})
	t.Run("a self-marked header-less answer stores nothing", func(t *testing.T) {
		f, c, d, first := setup(t)
		before := c.State()
		f.set(reply(503, "", `busy`))
		res := c.GetJSON(t.Context(), d.UnderSelfMark(first.SelfMark), "/api/v3/series", transport.MaxListBytes, &out)
		if c.State() != before || res.SelfMark != first.SelfMark {
			t.Errorf("State %+v, SelfMark %d; want the State unchanged and the mark held", c.State(), res.SelfMark)
		}
	})
	outside := []struct {
		name  string
		evict func(t *testing.T, f *fake, c *transport.Client, d *transport.Detection)
	}{
		{"another call's answer naming the detection's version", func(t *testing.T, f *fake, c *transport.Client, _ *transport.Detection) {
			f.set(reply(200, v4, `{}`))
			var o any
			c.Probe(t.Context(), transport.NewProbeRun(), "/api", &o)
		}},
		{"a failed run's MarkUnknown", func(_ *testing.T, _ *fake, c *transport.Client, _ *transport.Detection) {
			c.MarkUnknown("/api/v3/system/status")
		}},
	}
	for _, tc := range outside {
		t.Run("refused after "+tc.name, func(t *testing.T) {
			f, c, d, first := setup(t)
			tc.evict(t, f, c, d)
			f.reset()
			f.set(reply(200, v4, `[1]`))
			res := c.GetJSON(t.Context(), d.UnderSelfMark(first.SelfMark), "/api/v3/series", transport.MaxListBytes, &out)
			assertRefusedUnconfirmed(t, f, res)
		})
	}
	t.Run("refused after a header-less answer to an attempt admitted earlier", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		release, unblock := releaser(t)
		f.set(routes(map[string]http.HandlerFunc{
			"/api/v3/held": func(w http.ResponseWriter, r *http.Request) {
				<-release
				reply(500, "", ``)(w, r)
			},
			"/api/v3/series": reply(503, "", ``),
		}))
		var wg sync.WaitGroup
		wg.Go(func() { c.GetJSON(t.Context(), d, "/api/v3/held", transport.MaxListBytes, &out) })
		waitFor(t, f, "GET /api/v3/held")
		first := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		unblock()
		wg.Wait()
		f.reset()
		res := c.GetJSON(t.Context(), d.UnderSelfMark(first.SelfMark), "/api/v3/series", transport.MaxListBytes, &out)
		assertRefusedUnconfirmed(t, f, res)
	})
	t.Run("a redirect naming another version ends the mark", func(t *testing.T) {
		f, c, d, first := setup(t)
		f.set(routes(map[string]http.HandlerFunc{
			"/api/v3/series": redirectTo(302, "/hop", v5),
			"/hop":           reply(200, v5, `[]`),
		}))
		res := c.GetJSON(t.Context(), d.UnderSelfMark(first.SelfMark), "/api/v3/series", transport.MaxListBytes, &out)
		if s := c.State(); s.Epoch != first.SelfMark+1 || s.AppVersion != v5 {
			t.Errorf("State = %+v, want epoch %d naming %s", s, first.SelfMark+1, v5)
		}
		if res.Verdict != transport.VerdictRefused || res.SelfMark != 0 {
			t.Errorf("result = (%v, mark %d), want the hop refused and no mark", res.Verdict, res.SelfMark)
		}
		if got := f.requests(); len(got) != 1 {
			t.Errorf("server saw %v, want only the 302", got)
		}
	})
	t.Run("Install refuses the copy", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		c.MarkUnknown("/x")
		f.set(reply(200, v4, `{}`))
		var o any
		c.Probe(t.Context(), transport.NewProbeRun(), "/api", &o)
		fresh := *d
		fresh.Epoch = c.State().Epoch
		c.Install(fresh.UnderSelfMark(fresh.Epoch))
		if c.Current() != nil {
			t.Error("Install stored an UnderSelfMark copy")
		}
		c.Install(&fresh)
		if c.Current() != &fresh {
			t.Error("Install refused the plain detection of the current epoch")
		}
	})
}

func assertRefusedUnconfirmed(t *testing.T, f *fake, res transport.Result) {
	t.Helper()
	if res.Verdict != transport.VerdictRefused || res.Sent {
		t.Errorf("result = (%v, sent %v), want VerdictRefused and nothing sent", res.Verdict, res.Sent)
	}
	if got := f.requests(); len(got) != 0 {
		t.Errorf("server saw %v, want nothing", got)
	}
	sc, ok := errors.AsType[*arrapi.ServerChangedError](res.Err)
	if !ok {
		t.Fatalf("Err = %v, want *ServerChangedError", res.Err)
	}
	if sc.To != "" || sc.From != v4 || !strings.Contains(sc.Error(), "is no longer confirmed") {
		t.Errorf("error = %+v (%q), want To empty and the no-longer-confirmed text", sc, sc.Error())
	}
}

func TestHopPathNeverInErrors(t *testing.T) {
	hop := "/" + string(testKey) + "/api/v3/series?apikey=" + string(testKey)
	for _, tc := range []struct {
		name string
		v    string
	}{{"hop answered by another version", v5}, {"hop answered header-less", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v3/series" {
					redirectTo(302, hop, "")(w, r)
					return
				}
				reply(500, tc.v, `{}`)(w, r)
			})
			var out any
			own := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
			if own.Err == nil {
				t.Fatal("the hop's failure returned no error")
			}
			assertChainClean(t, own.Err, string(testKey))
			if s := c.State(); s.Path != "/api/v3/series" {
				t.Errorf("State.Path = %q, want the call's own path", s.Path)
			}
			refused := c.GetJSON(t.Context(), d, "/api/v3/tag", transport.MaxListBytes, &out)
			for name, err := range map[string]error{"refused read": refused.Err, "end check": c.ChangedError(c.State(), v4)} {
				sc, ok := errors.AsType[*arrapi.ServerChangedError](err)
				if !ok || sc.Path != "/api/v3/series" {
					t.Errorf("%s = %v, want a *ServerChangedError with Path /api/v3/series", name, err)
					continue
				}
				assertChainClean(t, err, string(testKey))
			}
		})
	}
}

func TestObserveSeesEveryHop(t *testing.T) {
	cross := newFake(t, reply(200, v5, `[]`))
	crossURL := strings.Replace(cross.srv.URL, "127.0.0.1", "localhost", 1)
	cases := map[string]func(*http.Request) string{
		"refused":   func(*http.Request) string { return crossURL + "/api/v3/series" },
		"malformed": func(*http.Request) string { return "http://[::1" },
		"followed":  func(*http.Request) string { return "/hop" },
	}
	clients := map[string]func(*fake) *http.Client{
		"default client": func(*fake) *http.Client { return nil },
		"caller client": func(f *fake) *http.Client {
			return &http.Client{CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				if req.URL.Host != strings.TrimPrefix(f.srv.URL, "http://") {
					return errors.New("caller refuses another host")
				}
				return nil
			}}
		},
	}
	for cname, mk := range clients {
		for name, loc := range cases {
			t.Run(cname+" "+name, func(t *testing.T) {
				f := newFake(t, nil)
				c := newClient(t, f.srv.URL, transport.Config{HTTPClient: mk(f)})
				d := detect(t, c, f, v4)
				f.set(routes(map[string]http.HandlerFunc{
					"/api/v3/series": func(w http.ResponseWriter, r *http.Request) { redirectTo(302, loc(r), v5)(w, r) },
					"/hop":           reply(200, v5, `[]`),
				}))
				var out any
				res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
				if s := c.State(); s.AppVersion != v5 || s.Epoch != d.Epoch+1 {
					t.Errorf("State = %+v, want epoch %d naming %s before the call returned", s, d.Epoch+1, v5)
				}
				if res.Err == nil {
					t.Error("a redirect from another version returned no error")
				}
			})
		}
	}
}

// hopFails serves a header-less 302 to /hop and fails the hop at dial.
func hopFails(v string) roundTripFunc {
	return func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api":
			return &http.Response{StatusCode: http.StatusOK, Header: hdr(v), Body: http.NoBody, Request: req}, nil
		case "/hop":
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
		}
		h := hdr("")
		h.Set("Location", "/hop")
		return &http.Response{StatusCode: http.StatusFound, Header: h, Body: http.NoBody, Request: req}, nil
	}
}

func TestHeaderlessRedirectJudgedWhereTheCallEnds(t *testing.T) {
	var out any
	t.Run("followed with an answer stores nothing", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		before := c.State()
		f.set(routes(map[string]http.HandlerFunc{"/api/v3/series": redirectTo(302, "/hop", ""), "/hop": reply(200, v4, `[]`)}))
		if res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out); res.Err != nil || c.State() != before {
			t.Errorf("result %v, State %+v; want success and the State unchanged", res.Err, c.State())
		}
	})
	t.Run("refused marks the detection stale", func(t *testing.T) {
		cross := newFake(t, reply(200, v4, `[]`))
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(redirectTo(302, strings.Replace(cross.srv.URL, "127.0.0.1", "localhost", 1)+"/x", ""))
		res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		if s := c.State(); s.Epoch != d.Epoch+1 || s.AppVersion != "" || res.Next != transport.NextDone || res.SelfMark != s.Epoch {
			t.Errorf("State %+v, result (%v, mark %d); want the mark stored and NextDone", s, res.Next, res.SelfMark)
		}
		if got := cross.requests(); len(got) != 0 {
			t.Errorf("the refused target saw %v", got)
		}
	})
	t.Run("a command's marks the detection stale", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(redirectTo(302, "/hop", ""))
		res := c.Post(t.Context(), d, "/api/v3/command", map[string]any{})
		if s := c.State(); s.Epoch != d.Epoch+1 || res.Verdict != transport.VerdictUnidentified {
			t.Errorf("State %+v, verdict %v; want the mark and VerdictUnidentified", s, res.Verdict)
		}
	})
	t.Run("a hop the send check refuses clears the record", func(t *testing.T) {
		f := newFake(t, nil)
		var c *transport.Client
		hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			c.MarkUnknown("/elsewhere")
			return nil
		}}
		c = newClient(t, f.srv.URL, transport.Config{HTTPClient: hc})
		d := detect(t, c, f, v4)
		f.set(routes(map[string]http.HandlerFunc{"/api/v3/series": redirectTo(302, "/hop", ""), "/hop": reply(200, v4, `[]`)}))
		res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		if s := c.State(); s.Epoch != d.Epoch+1 || s.Path != "/elsewhere" || res.Verdict != transport.VerdictRefused {
			t.Errorf("State %+v, verdict %v; want only the outside mark and VerdictRefused", s, res.Verdict)
		}
	})
	t.Run("a read's failed hop marks and goes fresh", func(t *testing.T) {
		c := newClient(t, "http://sonarr:8989", transport.Config{HTTPClient: &http.Client{Transport: hopFails(v4)}})
		r := transport.NewProbeRun()
		c.Probe(t.Context(), r, "/api", &out)
		epoch, _ := r.Anchor()
		d := &transport.Detection{Identity: transport.Identity{App: "Sonarr", AppVersion: v4}, Epoch: epoch}
		res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		s := c.State()
		if s.Epoch != epoch+1 || s.AppVersion != "" {
			t.Errorf("State = %+v, want the detection marked stale", s)
		}
		if res.Verdict != transport.VerdictTransport || res.Next != transport.NextFresh || res.SelfMark != s.Epoch {
			t.Errorf("result = (%v, %v, mark %d), want row 14: Transport, Fresh, mark %d", res.Verdict, res.Next, res.SelfMark, s.Epoch)
		}
	})
	t.Run("an anchored probe's failed hop marks at once", func(t *testing.T) {
		c := newClient(t, "http://sonarr:8989", transport.Config{HTTPClient: &http.Client{Transport: hopFails(v4)}})
		r := transport.NewProbeRun()
		c.Probe(t.Context(), r, "/api", &out)
		epoch, _ := r.Anchor()
		_, res := c.Probe(t.Context(), r, "/api/v3/system/status", &out)
		if s := c.State(); s.Epoch != epoch+1 || res.Next != transport.NextDone {
			t.Errorf("State %+v, Next %v; want the mark and NextDone", s, res.Next)
		}
	})
	t.Run("an unanchored probe's failed hop marks nothing", func(t *testing.T) {
		c := newClient(t, "http://sonarr:8989", transport.Config{HTTPClient: &http.Client{Transport: hopFails(v4)}})
		before := c.State()
		_, res := c.Probe(t.Context(), transport.NewProbeRun(), "/api/v3/system/status", &out)
		if c.State() != before || res.Next != transport.NextRetry {
			t.Errorf("State %+v, Next %v; want it unchanged and row 13's NextRetry", c.State(), res.Next)
		}
	})
}

func TestMalformedLocationIsStatusError(t *testing.T) {
	var out any
	for _, tc := range []struct {
		name    string
		v       string
		verdict transport.Verdict
		marks   bool
		send    func(*transport.Client, *transport.Detection) transport.Result
	}{
		{"GetJSON header-less", "", transport.VerdictUnidentified, true, func(c *transport.Client, d *transport.Detection) transport.Result {
			return c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		}},
		{"GetByID header-less", "", transport.VerdictUnidentified, true, func(c *transport.Client, d *transport.Detection) transport.Result {
			return c.GetByID(t.Context(), d, "/api/v3/series/1", &out)
		}},
		{"anchored probe header-less", "", transport.VerdictUnidentified, true, func(c *transport.Client, d *transport.Detection) transport.Result {
			_, r := c.Probe(t.Context(), transport.AnchoredRun(d), "/api/v3/system/status", &out)
			return r
		}},
		{"unanchored probe header-less", "", transport.VerdictStatus, false, func(c *transport.Client, _ *transport.Detection) transport.Result {
			_, r := c.Probe(t.Context(), transport.NewProbeRun(), "/api/v3/system/status", &out)
			return r
		}},
		{"GetJSON from another version", v5, transport.VerdictChanged, true, func(c *transport.Client, d *transport.Detection) transport.Result {
			return c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		}},
		{"anchored probe from another version", v5, transport.VerdictChanged, true, func(c *transport.Client, d *transport.Detection) transport.Result {
			_, r := c.Probe(t.Context(), transport.AnchoredRun(d), "/api/v3/system/status", &out)
			return r
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			before := c.State()
			f.set(redirectTo(302, "http://[::1", tc.v))
			res := tc.send(c, d)
			if res.Verdict != tc.verdict {
				t.Errorf("verdict = %v, want %v", res.Verdict, tc.verdict)
			}
			se, ok := errors.AsType[*arrapi.StatusError](res.Err)
			if !ok || se.Code != 302 || se.Body != "" || se.Message != "" {
				t.Errorf("Err = %#v, want *StatusError{Code: 302} with no Body or Message", res.Err)
			}
			if marked := c.State() != before; marked != tc.marks || marked && c.Current() != nil {
				t.Errorf("State %+v changed %v, want %v with no detection left", c.State(), marked, tc.marks)
			}
		})
	}
}

func TestSendCheckEachAttemptAndHop(t *testing.T) {
	var out any
	contradicted := func(t *testing.T) (*fake, *transport.Client, *transport.Detection) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(reply(200, v5, `[]`))
		c.GetJSON(t.Context(), d, "/api/v3/tag", transport.MaxListBytes, &out)
		f.reset()
		return f, c, d
	}
	check := func(t *testing.T, f *fake, res transport.Result, path string) {
		t.Helper()
		if res.Verdict != transport.VerdictRefused {
			t.Errorf("verdict = %v, want VerdictRefused", res.Verdict)
		}
		sc, ok := errors.AsType[*arrapi.ServerChangedError](res.Err)
		if !ok || sc.From != v4 || sc.To != v5 || sc.Path != path {
			t.Errorf("Err = %+v, want From %s, To %s, Path %s", res.Err, v4, v5, path)
		}
		if got := f.requests(); len(got) != 0 {
			t.Errorf("server saw %v, want nothing", got)
		}
	}
	t.Run("a retry", func(t *testing.T) {
		f, c, d := contradicted(t)
		check(t, f, c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out), "/api/v3/tag")
	})
	t.Run("a POST", func(t *testing.T) {
		f, c, d := contradicted(t)
		check(t, f, c.Post(t.Context(), d, "/api/v3/command", map[string]any{}), "/api/v3/tag")
	})
	t.Run("an anchored probe", func(t *testing.T) {
		f, c, d := contradicted(t)
		_, res := c.Probe(t.Context(), transport.AnchoredRun(d), "/api/v3/system/status", &out)
		check(t, f, res, "/api/v3/tag")
	})
	t.Run("a hop", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(routes(map[string]http.HandlerFunc{"/api/v3/series": redirectTo(302, "/hop", v5), "/hop": reply(200, v5, `[]`)}))
		res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		if res.Verdict != transport.VerdictRefused || !res.Sent {
			t.Errorf("result = (%v, sent %v), want the hop refused after the first request left", res.Verdict, res.Sent)
		}
		if slices.Contains(f.requests(), "GET /hop") {
			t.Errorf("the hop was sent: %v", f.requests())
		}
	})
	t.Run("a current epoch after Install replaced the pointer", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		r := transport.NewProbeRun()
		f.set(reply(200, v4, `{}`))
		c.Probe(t.Context(), r, "/api", &out)
		epoch, _ := r.Anchor()
		d := &transport.Detection{Identity: transport.Identity{App: "Sonarr", AppVersion: v4}, Epoch: epoch}
		before := c.State()
		c.Install(d)
		if c.State() == before {
			t.Fatal("Install did not replace the State pointer")
		}
		if res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out); res.Err != nil {
			t.Errorf("read after Install = %v, want it sent", res.Err)
		}
	})
}

func TestSendCheckDuringPendingRedirectHop(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hopV    string
		verdict transport.Verdict
	}{
		{"hop answered by another version", v5, transport.VerdictChanged},
		{"hop answered header-less", "", transport.VerdictUnidentified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := make(chan struct{})
			release, unblock := releaser(t)
			var tagSeen bool
			rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				resp := &http.Response{Body: http.NoBody, Request: req}
				switch req.URL.Path {
				case "/api":
					resp.StatusCode, resp.Header = 200, hdr(v4)
				case "/api/v3/series":
					resp.StatusCode, resp.Header = 302, hdr("")
					resp.Header.Set("Location", "/hop")
				case "/hop":
					close(held)
					<-release
					resp.StatusCode, resp.Header = 500, hdr(tc.hopV)
				case "/api/v3/tag":
					tagSeen = true
					resp.StatusCode, resp.Header = 200, hdr(v5)
				}
				return resp, nil
			})
			c := newClient(t, "http://sonarr:8989", transport.Config{HTTPClient: &http.Client{Transport: rt}})
			r := transport.NewProbeRun()
			var out any
			c.Probe(t.Context(), r, "/api", &out)
			epoch, _ := r.Anchor()
			d := &transport.Detection{Identity: transport.Identity{App: "Sonarr", AppVersion: v4}, Epoch: epoch}
			c.Install(d)
			var a transport.Result
			var wg sync.WaitGroup
			wg.Go(func() { a = c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out) })
			<-held
			if s := c.State(); s.Det != d {
				t.Errorf("State = %+v during the held hop, want the detection still installed", s)
			}
			b := c.GetJSON(t.Context(), d, "/api/v3/tag", transport.MaxListBytes, &out)
			if !tagSeen || b.Verdict != transport.VerdictChanged || b.Next != transport.NextFresh {
				t.Errorf("read B sent %v, result (%v, %v); want it sent and judged row 8", tagSeen, b.Verdict, b.Next)
			}
			after := c.State()
			unblock()
			wg.Wait()
			if a.Verdict != tc.verdict {
				t.Errorf("read A verdict = %v, want %v", a.Verdict, tc.verdict)
			}
			if tc.hopV == v5 && c.State() != after {
				t.Errorf("A's hop answer naming %s stored %+v, want nothing", v5, c.State())
			}
		})
	}
}

func TestLaterProbeNotSentAfterContradiction(t *testing.T) {
	var out any
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
	}{
		{"a stale read answered by another version", reply(200, v5, `[]`)},
		{"a stale read answered header-less", reply(404, "", ``)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, reply(200, v4, `{"current":"v3"}`))
			c := newClient(t, f.srv.URL, transport.Config{})
			old := detect(t, c, f, v4)
			f.set(reply(200, v4, `{"current":"v3"}`))
			r := transport.NewProbeRun()
			c.Probe(t.Context(), r, "/api", &out)
			f.set(tc.h)
			c.GetJSON(t.Context(), old, "/api/v3/series", transport.MaxListBytes, &out)
			f.reset()
			_, res := c.Probe(t.Context(), r, "/api/v3/system/status", &out)
			if res.Verdict != transport.VerdictRefused || res.Next != transport.NextDone || len(f.requests()) != 0 {
				t.Errorf("status probe = (%v, %v), server saw %v; want refused and unsent", res.Verdict, res.Next, f.requests())
			}
		})
	}
	t.Run("the status probe's own header-less 500 is not retried", func(t *testing.T) {
		f := newFake(t, reply(200, v4, `{"current":"v3"}`))
		c := newClient(t, f.srv.URL, transport.Config{})
		r := transport.NewProbeRun()
		c.Probe(t.Context(), r, "/api", &out)
		f.set(reply(500, "", ``))
		f.reset()
		_, res := c.Probe(t.Context(), r, "/api/v3/system/status", &out)
		if res.Verdict != transport.VerdictUnidentified || res.Next != transport.NextDone || len(f.requests()) != 1 {
			t.Errorf("status probe = (%v, %v), server saw %v; want one request and NextDone", res.Verdict, res.Next, f.requests())
		}
	})
}

func TestEpochNotRevalidated(t *testing.T) {
	var out any
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(reply(404, "", ``))
	c.GetJSON(t.Context(), d, "/api/v3/episodefile", transport.MaxListBytes, &out)
	f.set(reply(200, v4, `{}`))
	c.Probe(t.Context(), transport.NewProbeRun(), "/api", &out)
	if s := c.State(); s.AppVersion != v4 || s.Epoch != d.Epoch+2 {
		t.Fatalf("State = %+v, want epoch %d naming %s again", s, d.Epoch+2, v4)
	}
	f.reset()
	assertRefusedUnconfirmed(t, f, c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out))
}

func TestInstallKeepsUsableNeverStale(t *testing.T) {
	var out any
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	epoch := c.State().Epoch
	second := *d
	c.Install(&second)
	if c.Current() != d || c.State().Epoch != epoch {
		t.Errorf("a second Install in one epoch replaced the first or moved the epoch")
	}
	c.MarkUnknown("/x")
	c.Install(d)
	if c.Current() != nil {
		t.Error("Install stored a detection from an older epoch")
	}
	f.set(reply(200, v4, `[]`))
	if res := c.GetJSON(t.Context(), &second, "/api/v3/series", transport.MaxListBytes, &out); res.Verdict != transport.VerdictRefused {
		t.Errorf("a detection two epochs old was sent (verdict %v)", res.Verdict)
	}
}

func TestCommandNeverRedirected(t *testing.T) {
	for _, client := range []struct {
		name string
		hc   *http.Client
	}{
		{"default client", nil},
		{"follow-everything caller client", &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}},
	} {
		for _, code := range []int{301, 302, 303, 307, 308} {
			for lname, loc := range map[string]string{"followable Location": "/target", "malformed Location": "http://[::1"} {
				for _, v := range []string{v4, v5} {
					t.Run(fmt.Sprintf("%s %d %s %s", client.name, code, lname, v), func(t *testing.T) {
						f := newFake(t, nil)
						c := newClient(t, f.srv.URL, transport.Config{HTTPClient: client.hc})
						d := detect(t, c, f, v4)
						f.set(routes(map[string]http.HandlerFunc{
							"/api/v3/command": redirectTo(code, loc, v),
							"/target":         reply(200, v4, `{}`),
						}))
						res := c.Post(t.Context(), d, "/api/v3/command", map[string]any{})
						for _, r := range f.requests() {
							if strings.Contains(r, "/target") {
								t.Errorf("the redirect target received %q", r)
							}
						}
						if res.Err == nil || errors.Is(res.Err, arrapi.ErrCommandOutcomeUnknown) {
							t.Fatalf("Err = %v, want a failure that is not an unknown outcome", res.Err)
						}
						se, ok := errors.AsType[*arrapi.StatusError](res.Err)
						if !ok || se.Code != code {
							t.Errorf("Err = %v, want a *StatusError %d", res.Err, code)
						}
						if _, changed := errors.AsType[*arrapi.ServerChangedError](res.Err); changed != (v == v5) {
							t.Errorf("Err = %v, want a *ServerChangedError exactly when another version answered", res.Err)
						}
					})
				}
			}
		}
	}
}

func TestReadRedirectKeepsCallerPolicy(t *testing.T) {
	f := newFake(t, nil)
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
	c := newClient(t, f.srv.URL, transport.Config{HTTPClient: hc})
	d := detect(t, c, f, v4)
	f.set(routes(map[string]http.HandlerFunc{
		"/api/v3/series": redirectTo(307, "/target", v4),
		"/target":        reply(200, v4, `[1,2]`),
	}))
	var out []int
	if res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out); res.Err != nil || len(out) != 2 {
		t.Errorf("read through the caller's policy = %v (out %v), want the redirect followed and decoded", res.Err, out)
	}
}
