package sonarr

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestTwoClientsKeepTheirOwnDetection runs a client of a Sonarr v4 (API v3) server and
// a client of a Sonarr v5 (API v5) server side by side over real sockets: each detects
// once, reads only its own API's paths, and an upgrade behind one URL re-detects only
// that client.
func TestTwoClientsKeepTheirOwnDetection(t *testing.T) {
	f4, f5 := newFake().sonarr4(v4), newFake().sonarr5(v5)
	f4.on(v4Series, ans(200, v4, v4SeriesList))
	f5.on(v5Series, ans(200, v5, v5SeriesList))
	c4, c5 := liveClient(t, f4), liveClient(t, f5)

	var wg sync.WaitGroup
	errs := make(chan string, 40)
	for range 10 {
		for _, cc := range []struct {
			c     *Client
			title string
		}{{c4, "Sonarr Four Show"}, {c5, "Sonarr Five Show"}} {
			wg.Go(func() {
				got, err := cc.c.Series(t.Context())
				if err != nil || len(got) != 1 || got[0].Title != cc.title {
					errs <- "Series = " + show(got) + ", want " + cc.title
				}
			})
		}
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	for _, s := range []struct {
		name  string
		f     *fake
		own   string
		other string
	}{{"Sonarr v4 (API v3)", f4, "/api/v3/", "/api/v5/"}, {"Sonarr v5 (API v5)", f5, "/api/v5/", "/api/v3/"}} {
		if n := count(s.f, "GET /api"); n != 1 {
			t.Errorf("%s: %d detections, want 1", s.name, n)
		}
		for _, r := range s.f.requests() {
			if strings.Contains(r, s.other) || (r != "GET /api" && !strings.Contains(r, s.own)) {
				t.Errorf("%s server got %q", s.name, r)
			}
		}
	}

	f4.clearLog()
	f5.clearLog()
	const upgraded = "5.0.0.2700"
	f4.sonarr5(upgraded)
	f4.on(v5Series, ans(200, upgraded, v5SeriesList))
	got, err := c4.Series(t.Context())
	if err != nil {
		t.Fatalf("Series after the upgrade: %v", err)
	}
	wantTitle(t, got, "Sonarr Five Show")
	wantRequests(t, f4, join([]string{v4Series}, detectV5, []string{v5Series})...)
	if _, err := c5.Series(t.Context()); err != nil {
		t.Fatalf("other client's Series: %v", err)
	}
	wantRequests(t, f5, v5Series)
}

// liveClient returns a Client of f served by an httptest.Server.
func liveClient(t *testing.T, f *fake) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, testKey, WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}
