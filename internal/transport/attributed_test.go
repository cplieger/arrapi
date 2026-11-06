package transport_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

func wantAttributed(t *testing.T, err error, code int, want bool) {
	t.Helper()
	e, ok := errors.AsType[*arrapi.StatusError](err)
	if !ok {
		t.Fatalf("err = %v, want a *StatusError", err)
	}
	if e.Code != code || e.Attributed != want {
		t.Errorf("StatusError code %d, Attributed %v; want %d, %v", e.Code, e.Attributed, code, want)
	}
}

func TestStatusErrorAttributedFromTheVerdict(t *testing.T) {
	var out any
	calls := map[string]func(*transport.Client, *transport.Detection) transport.Result{
		"a list read": func(c *transport.Client, d *transport.Detection) transport.Result {
			return c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
		},
		"a by-id read": func(c *transport.Client, d *transport.Detection) transport.Result {
			return c.GetByID(t.Context(), d, "/api/v3/series/5", &out)
		},
		"a command": func(c *transport.Client, d *transport.Detection) transport.Result {
			return c.Post(t.Context(), d, "/api/v3/command", map[string]string{"name": "RescanSeries"})
		},
	}
	for _, tc := range []struct {
		call   string
		code   int
		header bool
	}{
		{"a list read", 500, true},
		{"a list read", 500, false},
		{"a by-id read", 500, true},
		{"a by-id read", 500, false},
		{"a command", 500, true},
		{"a command", 500, false},
		{"a list read", 404, true},
		{"a command", 404, true},
	} {
		name := tc.call
		if tc.header {
			name += " with the header"
		} else {
			name += " without the header"
		}
		t.Run(name+" "+http.StatusText(tc.code), func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			v := ""
			if tc.header {
				v = v4
			}
			f.set(reply(tc.code, v, `{"message":"failed"}`))
			wantAttributed(t, calls[tc.call](c, d).Err, tc.code, tc.header)
		})
	}
}

func TestProbeStatusErrorNeverAttributed(t *testing.T) {
	var out any
	t.Run("an unanchored probe answered with a header", func(t *testing.T) {
		f := newFake(t, reply(500, v4, ``))
		c := newClient(t, f.srv.URL, transport.Config{})
		_, res := c.Probe(t.Context(), transport.NewProbeRun(), "/api", &out)
		if res.Verdict != transport.VerdictStatus {
			t.Errorf("verdict %v, want VerdictStatus", res.Verdict)
		}
		wantAttributed(t, res.Err, 500, false)
	})
	t.Run("an anchored probe answered with the run's header", func(t *testing.T) {
		f := newFake(t, routes(map[string]http.HandlerFunc{
			"/api":                  reply(200, v4, `{"current":"v3"}`),
			"/api/v3/system/status": reply(500, v4, ``),
		}))
		c := newClient(t, f.srv.URL, transport.Config{})
		r := transport.NewProbeRun()
		c.Probe(t.Context(), r, "/api", &out)
		if _, ok := r.Anchor(); !ok {
			t.Fatal("the /api answer did not anchor the run")
		}
		_, res := c.Probe(t.Context(), r, "/api/v3/system/status", &out)
		if res.Verdict != transport.VerdictStatus {
			t.Errorf("verdict %v, want VerdictStatus", res.Verdict)
		}
		wantAttributed(t, res.Err, 500, false)
	})
}

func TestServerChangedStatusNeverAttributed(t *testing.T) {
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(reply(500, v5, `{"message":"failed"}`))
	var out any
	res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
	sc, ok := errors.AsType[*arrapi.ServerChangedError](res.Err)
	if !ok || sc.Status == nil {
		t.Fatalf("Err = %v, want a *ServerChangedError carrying a Status", res.Err)
	}
	if sc.Status.Attributed {
		t.Error("ServerChangedError.Status.Attributed = true, want false")
	}
}
