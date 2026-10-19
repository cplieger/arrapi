package radarr

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

func TestRescanBodyAndFreshDetection(t *testing.T) {
	for _, code := range []int{200, 201, 202} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			cached(t, c, f)
			var got string
			f.on(command, body(&got, ans(code, vRadarr, "not a command resource")))
			if err := c.RescanMovie(t.Context(), 41); err != nil {
				t.Fatalf("RescanMovie: %v", err)
			}
			if want := `{"name":"RescanMovie","movieId":41}`; got != want {
				t.Errorf("command body = %s, want %s", got, want)
			}
			wantRequests(t, f, join(detectRadarr, []string{command})...)
		})
	}
}

func TestRescanSentOnce(t *testing.T) {
	for name, tc := range map[string]struct {
		answer step
		code   int
	}{
		"a headered 503":    {ans(503, vRadarr, ""), 503},
		"a header-less 502": {ans(502, "", ""), 502},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			f.on(command, tc.answer)
			wantStatus(t, c.RescanMovie(t.Context(), 1), tc.code)
			if n := count(f, command); n != 1 {
				t.Errorf("sent %d commands, want 1", n)
			}
		})
	}
}

func TestRescanRedirectIsStatusAndNeverFollowed(t *testing.T) {
	clients := map[string]func(t *testing.T, f *fake) *Client{
		"the default client": func(t *testing.T, f *fake) *Client {
			srv := httptest.NewServer(f)
			t.Cleanup(srv.Close)
			c, err := New(srv.URL, testKey, WithLogger(slog.New(slog.DiscardHandler)))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			t.Cleanup(c.Close)
			return c
		},
		"a client following every redirect": func(t *testing.T, f *fake) *Client {
			follow := &http.Client{Transport: f, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
			return newClient(t, f, WithHTTPClient(follow))
		},
	}
	for clientName, newC := range clients {
		t.Run(clientName, func(t *testing.T) {
			for _, code := range []int{301, 302, 303, 307, 308} {
				for _, location := range []string{"/target", "http://[::1"} {
					t.Run(strconv.Itoa(code)+" to "+location, func(t *testing.T) {
						f := newFake().radarr(vRadarr)
						c := newC(t, f)
						f.on(command, redirect(code, vRadarr, location))
						err := c.RescanMovie(t.Context(), 1)
						wantStatus(t, err, code)
						if errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
							t.Error("a redirected command reads as outcome unknown")
						}
						if n := count(f, "GET /target") + count(f, "POST /target"); n != 0 {
							t.Errorf("the redirect target received %d requests", n)
						}
						if n := count(f, command); n != 1 {
							t.Errorf("sent %d commands, want 1", n)
						}
					})
				}
			}
		})
	}
}

func TestRescanUnattributableSuccessIsOutcomeUnknown(t *testing.T) {
	for name, tc := range map[string]struct {
		check  func(*testing.T, error)
		answer step
	}{
		"a 2xx from another version": {
			answer: ans(201, "6.4.5.10700", "{}"),
			check:  func(t *testing.T, err error) { wantChanged(t, err) },
		},
		"a header-less 2xx": {
			answer: ans(201, "", "{}"),
			check:  func(t *testing.T, err error) { wantUnsupported(t, err, arrapi.ReasonNoVersionHeader) },
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			f.on(command, tc.answer)
			err := c.RescanMovie(t.Context(), 1)
			if !errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
				t.Fatalf("err = %v, want ErrCommandOutcomeUnknown", err)
			}
			tc.check(t, err)
			if n := count(f, command); n != 1 {
				t.Errorf("sent %d commands, want 1", n)
			}
		})
	}
}

func TestRescanNon2xxFromOtherServerKeepsCode(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	f.on(command, ans(404, "6.4.5.10700", `{"message":"NotFound"}`))
	err := c.RescanMovie(t.Context(), 1)
	if e := wantChanged(t, err); e.Status == nil || e.Status.Code != 404 {
		t.Errorf("ServerChangedError.Status = %+v, want the 404", e.Status)
	}
	wantStatus(t, err, 404)
	if errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
		t.Error("a failed command from another server reads as outcome unknown")
	}
	if n := count(f, command); n != 1 {
		t.Errorf("sent %d commands, want 1", n)
	}
}

func TestRescanAcrossRepointNeverSent(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	cached(t, c, f)
	f.sonarr4(v4Sonarr)
	wantUnsupported(t, c.RescanMovie(t.Context(), 1), arrapi.ReasonWrongApp)
	wantRequests(t, f, detectRadarr...)
}

// failedDetections are detections that fail after an answer, each against a server
// that keeps failing the same way.
var failedDetections = map[string]struct {
	serve func(*fake)
	check func(*testing.T, error)
}{
	"arm J from a Radarr 7.0.0.1": {
		serve: func(f *fake) {
			const v = "7.0.0.1"
			f.radarr(v)
			f.on("GET /api", ans(200, v, apiBody("v4")))
			f.on("GET /api/v4/system/status", ans(200, v, statusBody("Radarr", v)))
		},
		check: func(t *testing.T, err error) { wantUnsupported(t, err, arrapi.ReasonUnknownGeneration) },
	},
	"arm J at the installed version": {
		serve: func(f *fake) {
			f.radarr(vRadarr)
			f.on("GET /api", ans(200, vRadarr, apiBody("v4")))
			f.on("GET /api/v4/system/status", ans(200, vRadarr, statusBody("Radarr", vRadarr)))
		},
		check: func(t *testing.T, err error) { wantUnsupported(t, err, arrapi.ReasonUnknownGeneration) },
	},
	"arm I from a Sonarr v5 (API v5) at 5.0.0.1": {
		serve: func(f *fake) { f.sonarr5("5.0.0.1") },
		check: func(t *testing.T, err error) { wantUnsupported(t, err, arrapi.ReasonWrongApp) },
	},
	"arm V: a status answer without the header": {
		serve: func(f *fake) {
			f.radarr(vRadarr)
			f.on("GET /api/v3/system/status", ans(200, "", statusBody("Radarr", vRadarr)))
		},
		check: func(t *testing.T, err error) { wantUnsupported(t, err, arrapi.ReasonNoVersionHeader) },
	},
}

func TestFailedDetectionAfterAnswerMarksUnknown(t *testing.T) {
	calls := map[string]func(context.Context, *Client) error{
		"Info":        func(ctx context.Context, c *Client) error { _, err := c.Info(ctx); return err },
		"RescanMovie": func(ctx context.Context, c *Client) error { return c.RescanMovie(ctx, 1) },
	}
	for armName, arm := range failedDetections {
		for callName, call := range calls {
			t.Run(armName+" through "+callName, func(t *testing.T) {
				f := newFake().radarr(vRadarr)
				c := newClient(t, f)
				cached(t, c, f)
				arm.serve(f)
				arm.check(t, call(t.Context(), c))
				f.clearLog()
				_, _ = c.Movies(t.Context())
				got := f.requests()
				if len(got) == 0 || got[0] != "GET /api" {
					t.Errorf("the next Movies sent %q, want a detection first", got)
				}
				for _, r := range got {
					if r == movies || r == command {
						t.Errorf("a request went out on the failed server: %q", got)
					}
				}
			})
		}
	}
	for callName, call := range calls {
		t.Run("a dial error before any answer through "+callName, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			cached(t, c, f)
			f.on("GET /api", dialFails)
			if err := call(t.Context(), c); err == nil {
				t.Fatal("the call succeeded with /api failing at dial")
			}
			if n := count(f, command); n != 0 {
				t.Errorf("sent %d commands after a failed detection", n)
			}
			f.clearLog()
			if _, err := c.Movies(t.Context()); err != nil {
				t.Fatalf("next Movies: %v", err)
			}
			wantRequests(t, f, movies)
		})
	}
}

func TestRescanNotSentAfterContradiction(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	cached(t, c, f)
	hold, release := releaser(t)
	atStatus := make(chan struct{})
	f.on("GET /api/v3/system/status", then(func() { close(atStatus) }, held(hold, ans(200, vRadarr, statusBody("Radarr", vRadarr)))))
	done := make(chan error, 1)
	go func() { done <- c.RescanMovie(t.Context(), 1) }()
	<-atStatus
	f.on(movies, ans(404, "", ""))
	_, err := c.Movies(t.Context())
	wantStatus(t, err, 404)
	release()
	if e := wantChanged(t, <-done); e.To != "" {
		t.Errorf("ServerChangedError.To = %q, want \"\" after a header-less answer", e.To)
	}
	if n := count(f, command); n != 0 {
		t.Errorf("sent %d commands after the contradiction", n)
	}
}
