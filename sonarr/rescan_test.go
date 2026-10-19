package sonarr

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v3"
)

const (
	v4Command = "POST /api/v3/command"
	v5Command = "POST /api/v5/command"
)

func TestRescanBodyAndFreshDetection(t *testing.T) {
	for name, tc := range map[string]struct {
		serve   func(*fake) *fake
		version string
		detect  []string
		command string
		code    int
	}{
		"Sonarr v4 (API v3)": {func(f *fake) *fake { return f.sonarr4(v4) }, v4, detectV4, v4Command, 201},
		"Sonarr v5 (API v5)": {func(f *fake) *fake { return f.sonarr5(v5) }, v5, detectV5, v5Command, 201},
	} {
		t.Run(name, func(t *testing.T) {
			f := tc.serve(newFake())
			c := newClient(t, f)
			cached(t, c, f)
			var got string
			f.on(tc.command, body(&got, ans(tc.code, tc.version, "not a command resource")))
			if err := c.RescanSeries(t.Context(), 41); err != nil {
				t.Fatalf("RescanSeries: %v", err)
			}
			if want := `{"name":"RescanSeries","seriesId":41}`; got != want {
				t.Errorf("command body = %s, want %s", got, want)
			}
			wantRequests(t, f, join(tc.detect, []string{tc.command})...)
		})
	}
}

func TestRescanSentOnceOn503(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	f.on(v4Command, ans(503, v4, ""))
	wantStatus(t, c.RescanSeries(t.Context(), 1), 503)
	if n := count(f, v4Command); n != 1 {
		t.Errorf("sent %d commands, want 1", n)
	}
}

func TestRescanAnswered200IsSuccess(t *testing.T) {
	f := newFake().sonarr4(v4)
	f.on(v4Command, ans(200, v4, "{}"))
	if err := newClient(t, f).RescanSeries(t.Context(), 1); err != nil {
		t.Errorf("RescanSeries on a 200 carrying the sent version: %v", err)
	}
}

func TestRescanRedirectIsStatusAndNeverFollowed(t *testing.T) {
	codes := []int{301, 302, 303, 307, 308}
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
			for _, code := range codes {
				for _, location := range []string{"/target", "http://[::1"} {
					t.Run(strconv.Itoa(code)+" to "+location, func(t *testing.T) {
						f := newFake().sonarr4(v4)
						c := newC(t, f)
						f.on(v4Command, redirectCode(code, v4, location))
						err := c.RescanSeries(t.Context(), 1)
						wantStatus(t, err, code)
						if errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
							t.Error("a redirected command reads as outcome unknown")
						}
						if n := count(f, "GET /target") + count(f, "POST /target"); n != 0 {
							t.Errorf("the redirect target received %d requests", n)
						}
						if n := count(f, v4Command); n != 1 {
							t.Errorf("sent %d commands, want 1", n)
						}
					})
				}
			}
		})
	}
}

func TestRescanAcrossUpgradeNeverReachesAPIv3(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	f.sonarr5(v5)
	if err := c.RescanSeries(t.Context(), 1); err != nil {
		t.Fatalf("RescanSeries: %v", err)
	}
	wantRequests(t, f, join(detectV5, []string{v5Command})...)
}

func TestRescanAnswerFromSonarr5IsOutcomeUnknown(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	f.on(v4Command, ans(201, v5, "{}"))
	err := c.RescanSeries(t.Context(), 1)
	if !errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
		t.Fatalf("err = %v, want ErrCommandOutcomeUnknown", err)
	}
	wantChanged(t, err)
	if n := count(f, v4Command); n != 1 {
		t.Errorf("sent %d commands, want 1", n)
	}
}

func TestRescanHeaderless2xxIsOutcomeUnknown(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	f.on(v4Command, ans(201, "", "{}"))
	err := c.RescanSeries(t.Context(), 1)
	if !errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
		t.Fatalf("err = %v, want ErrCommandOutcomeUnknown", err)
	}
	wantUnsupported(t, err, arrapi.ReasonNoVersionHeader)
	if n := count(f, v4Command); n != 1 {
		t.Errorf("sent %d commands, want 1", n)
	}
}

func TestRescanSwitchToSonarr5Headerless2xxIsOutcomeUnknown(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	f.on("GET /api/v3/system/status", then(func() {
		f.sonarr5(v5)
		f.on(v4Command, ans(201, "", "{}"))
	}, ans(200, v4, statusBody("Sonarr", v4))))
	err := c.RescanSeries(t.Context(), 1)
	if !errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
		t.Fatalf("err = %v, want ErrCommandOutcomeUnknown", err)
	}
	wantRequests(t, f, join(detectV4, []string{v4Command})...)
}

func TestRescanNon2xxFromOtherServerKeepsCode(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	f.on(v4Command, ans(404, v5, `{"message":"NotFound"}`))
	err := c.RescanSeries(t.Context(), 1)
	if e := wantChanged(t, err); e.Status == nil || e.Status.Code != 404 {
		t.Errorf("ServerChangedError.Status = %+v, want the 404", e.Status)
	}
	wantStatus(t, err, 404)
	if n := count(f, v4Command); n != 1 {
		t.Errorf("sent %d commands, want 1", n)
	}
}

// failedDetections are detections that fail after an answer, each against a server
// that keeps failing the same way.
var failedDetections = map[string]struct {
	serve func(*fake)
	check func(*testing.T, error)
}{
	"arm F: a Sonarr v5 (API v5) reached only over the /api/v3 refusal path": {
		serve: func(f *fake) {
			const v = "5.0.0.1"
			f.reset(v)
			f.on("GET /api", ans(404, "", ""))
			f.on("GET /api/v5/system/status", ans(404, v, ""))
			f.on("GET /api/v3/system/status", ans(200, v, statusBody("Sonarr", v)))
		},
		check: func(t *testing.T, err error) { wantUnsupported(t, err, arrapi.ReasonV5Unreachable) },
	},
	"arm W: two versions answer the probes": {
		serve: func(f *fake) {
			f.sonarr4(v4)
			f.on("GET /api/v3/system/status", ans(200, v5, statusBody("Sonarr", v5)))
		},
		check: func(t *testing.T, err error) { wantChanged(t, err) },
	},
	"arm J at the installed version": {
		serve: func(f *fake) {
			f.sonarr4(v4)
			f.on("GET /api", ans(200, v4, apiBody("v6")))
			f.on("GET /api/v6/system/status", ans(200, v4, statusBody("Sonarr", v4)))
		},
		check: func(t *testing.T, err error) { wantUnsupported(t, err, arrapi.ReasonUnknownGeneration) },
	},
	"arm V: a status answer without the header": {
		serve: func(f *fake) {
			f.sonarr4(v4)
			f.on("GET /api/v3/system/status", ans(200, "", statusBody("Sonarr", v4)))
		},
		check: func(t *testing.T, err error) { wantUnsupported(t, err, arrapi.ReasonNoVersionHeader) },
	},
}

func TestFailedDetectionAfterAnswerMarksUnknown(t *testing.T) {
	calls := map[string]func(context.Context, *Client) error{
		"Info":         func(ctx context.Context, c *Client) error { _, err := c.Info(ctx); return err },
		"RescanSeries": func(ctx context.Context, c *Client) error { return c.RescanSeries(ctx, 1) },
	}
	for armName, arm := range failedDetections {
		for callName, call := range calls {
			t.Run(armName+" through "+callName, func(t *testing.T) {
				f := newFake().sonarr4(v4)
				c := newClient(t, f)
				cached(t, c, f)
				arm.serve(f)
				arm.check(t, call(t.Context(), c))
				f.clearLog()
				_, _ = c.Series(t.Context())
				got := f.requests()
				if len(got) == 0 || got[0] != "GET /api" {
					t.Errorf("the next Series sent %q, want a detection first", got)
				}
				for _, r := range got {
					if r == v4Series || r == v4Command || r == v5Command {
						t.Errorf("a request went out on the failed server: %q", got)
					}
				}
			})
		}
	}
	for callName, call := range calls {
		t.Run("a dial error before any answer through "+callName, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			c := newClient(t, f)
			cached(t, c, f)
			f.on("GET /api", dialFails)
			if err := call(t.Context(), c); err == nil {
				t.Fatal("the call succeeded with /api failing at dial")
			}
			if n := count(f, v4Command); n != 0 {
				t.Errorf("sent %d commands after a failed detection", n)
			}
			f.clearLog()
			if _, err := c.Series(t.Context()); err != nil {
				t.Fatalf("next Series: %v", err)
			}
			wantRequests(t, f, v4Series)
		})
	}
}

// onDetected is a slog handler that runs fn at the next "arrapi: detected" record once
// armed, on the detecting goroutine.
type onDetected struct {
	fn    func()
	armed atomic.Bool
}

func (*onDetected) Enabled(context.Context, slog.Level) bool { return true }
func (h *onDetected) Handle(_ context.Context, r slog.Record) error {
	if r.Message == "arrapi: detected" && h.armed.CompareAndSwap(true, false) {
		h.fn()
	}
	return nil
}
func (h *onDetected) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *onDetected) WithGroup(string) slog.Handler      { return h }

func TestMutateNotSentAfterContradiction(t *testing.T) {
	noCommand := func(t *testing.T, f *fake) {
		t.Helper()
		if n := count(f, v4Command) + count(f, v5Command); n != 0 {
			t.Errorf("sent %d commands after the contradiction", n)
		}
	}
	t.Run("a stale read's header-less 404 before the end check", func(t *testing.T) {
		f := newFake().sonarr4(v4)
		c := newClient(t, f)
		cached(t, c, f)
		hold, release := releaser(t)
		atStatus := make(chan struct{})
		f.on("GET /api/v3/system/status", then(func() { close(atStatus) }, held(hold, ans(200, v4, statusBody("Sonarr", v4)))))
		done := make(chan error, 1)
		go func() { done <- c.RescanSeries(t.Context(), 1) }()
		<-atStatus
		f.on(v4Series, ans(404, "", ""))
		_, err := c.Series(t.Context())
		wantStatus(t, err, 404)
		release()
		wantChanged(t, <-done)
		noCommand(t, f)
	})
	t.Run("a stale read's header-less 404 after the end check, before the POST", func(t *testing.T) {
		f := newFake().sonarr4(v4)
		h := &onDetected{}
		c := newClient(t, f, WithLogger(slog.New(h)))
		cached(t, c, f)
		f.on(v4Series, ans(404, "", ""))
		h.fn = func() {
			if _, err := c.Series(t.Context()); err == nil {
				t.Error("the stale read succeeded")
			}
		}
		h.armed.Store(true)
		wantChanged(t, c.RescanSeries(t.Context(), 1))
		noCommand(t, f)
		wantRequests(t, f, join(detectV4, []string{v4Series})...)
	})
	for name, answer := range map[string]step{
		"header-less":         ans(404, "", ""),
		"carrying 5.0.0.2635": ans(404, v5, ""),
	} {
		t.Run("a header-less 404 from Sonarr v4 (API v3), the rescan's anchor, then Sonarr v5 (API v5) "+name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			c := newClient(t, f, WithMaxAttempts(1))
			cached(t, c, f)
			holdB, releaseB := releaser(t)
			atB := make(chan struct{})
			f.on("GET /api/v3/tag", then(func() { close(atB) }, held(holdB, answer)))
			doneB := make(chan error, 1)
			go func() { _, err := c.Tags(t.Context()); doneB <- err }()
			<-atB
			f.on(v4Series, ans(404, "", ""))
			_, err := c.Series(t.Context())
			wantStatus(t, err, 404)
			holdS, releaseS := releaser(t)
			atStatus := make(chan struct{})
			f.on("GET /api/v3/system/status", then(func() { close(atStatus) }, held(holdS, ans(200, v4, statusBody("Sonarr", v4)))))
			done := make(chan error, 1)
			go func() { done <- c.RescanSeries(t.Context(), 1) }()
			<-atStatus
			releaseB()
			if err := <-doneB; err == nil {
				t.Error("the stale Tags read succeeded")
			}
			releaseS()
			wantChanged(t, <-done)
			noCommand(t, f)
		})
	}
	t.Run("two runs, the lazy leader answered by Sonarr v5 (API v5)", func(t *testing.T) {
		f := newFake().sonarr4(v4)
		c := newClient(t, f)
		f.on("GET /api", ans(200, v4, apiBody("v3")), ans(200, v5, apiBody("v5", "v3")))
		f.on("GET /api/v5/system/status", ans(200, v5, statusBody("Sonarr", v5)))
		f.on(v5Series, ans(200, v5, v5SeriesList))
		hold, release := releaser(t)
		atStatus := make(chan struct{})
		f.on("GET /api/v3/system/status", then(func() { close(atStatus) }, held(hold, ans(200, v4, statusBody("Sonarr", v4)))))
		done := make(chan error, 1)
		go func() { done <- c.RescanSeries(t.Context(), 1) }()
		<-atStatus
		got, err := c.Series(t.Context())
		if err != nil {
			t.Fatalf("Series: %v", err)
		}
		wantTitle(t, got, "Sonarr Five Show")
		release()
		wantChanged(t, <-done)
		noCommand(t, f)
	})
}

func TestUnanchoredRunPendingWindow(t *testing.T) {
	t.Run("the run identifies Sonarr v5 (API v5)", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			c := newClient(t, f)
			cached(t, c, f)
			f.sonarr5(v5)
			f.on(v4Series, ans(200, v5, notAList))
			f.on(v5Series, ans(200, v5, v5SeriesList))
			hold, release := releaser(t)
			second := make(chan struct{})
			f.on("GET /api",
				ans(503, "", ""),
				then(func() { close(second) }, held(hold, ans(503, "", ""))),
				ans(200, v5, apiBody("v5", "v3")))
			done := make(chan error, 1)
			go func() { done <- c.RescanSeries(t.Context(), 1) }()
			<-second
			got, err := c.Series(t.Context())
			if err != nil {
				t.Fatalf("Series during the run: %v", err)
			}
			wantTitle(t, got, "Sonarr Five Show")
			release()
			if err := <-done; err != nil {
				t.Fatalf("RescanSeries: %v", err)
			}
			if _, err := c.Series(t.Context()); err != nil {
				t.Fatalf("Series after the run: %v", err)
			}
			wantRequests(t, f, join(
				[]string{"GET /api", "GET /api", v4Series}, detectV5, []string{v5Series},
				detectV5, []string{v5Command, v5Series},
			)...)
		})
	})
	t.Run("the run fails on header-less answers alone", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			c := newClient(t, f)
			cached(t, c, f)
			f.on("GET /api", ans(503, "", ""), ans(503, "", ""), ans(503, "", ""), ans(200, v4, apiBody("v3")))
			wantStatus(t, c.RescanSeries(t.Context(), 1), 503)
			if _, err := c.Series(t.Context()); err != nil {
				t.Fatalf("Series after the failed run: %v", err)
			}
			wantRequests(t, f, join([]string{"GET /api", "GET /api", "GET /api"}, detectV4, []string{v4Series})...)
		})
	})
	t.Run("an answer after the run anchored refuses its redirect hop", func(t *testing.T) {
		f := newFake().sonarr4(v4)
		hold, release := releaser(t)
		atSeries := make(chan struct{})
		seriesDone := make(chan error, 1)
		var seriesErr error
		once := sync.OnceFunc(func() {
			release()
			seriesErr = <-seriesDone
		})
		// The hook runs after the observer saw the run's anchoring 302 and before the
		// hop's send check.
		hook := func(*http.Request, []*http.Request) error {
			once()
			return nil
		}
		c := newClient(t, f, WithHTTPClient(&http.Client{Transport: f, CheckRedirect: hook}))
		cached(t, c, f)
		f.sonarr5(v5)
		f.on(v4Series, then(func() { close(atSeries) }, held(hold, ans(404, "", ""))))
		f.on("GET /api", redirect(v5, "/api/"))
		f.on("GET /api/", ans(200, v5, apiBody("v5", "v3")))
		go func() { _, err := c.Series(t.Context()); seriesDone <- err }()
		<-atSeries
		wantChanged(t, c.RescanSeries(t.Context(), 1))
		wantStatus(t, seriesErr, 404)
		wantRequests(t, f, v4Series, "GET /api")
	})
}

func TestRescanDuringWalkDoesNotPauseReads(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	hold, release := releaser(t)
	atStatus := make(chan struct{})
	f.on("GET /api/v3/system/status", then(func() { close(atStatus) }, held(hold, ans(200, v4, statusBody("Sonarr", v4)))))
	f.on(v4Series, ans(200, v4, v4SeriesList))
	done := make(chan error, 1)
	go func() { done <- c.RescanSeries(t.Context(), 1) }()
	<-atStatus
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for range 3 {
		got, err := c.Series(ctx)
		if err != nil {
			t.Fatalf("Series while the rescan detects: %v", err)
		}
		wantTitle(t, got, "Sonarr Four Show")
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("RescanSeries: %v", err)
	}
	wantRequests(t, f, join([]string{"GET /api", "GET /api/v3/system/status", v4Series, v4Series, v4Series, v4Command})...)
}
