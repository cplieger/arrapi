package radarr

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

const unreadAnswer = "arrapi: command accepted; its answer was not read"

// commandCall is one public method sent at most once, sent for id 41, the resource it
// posts to under /api/v3, and the exact body Radarr reads (MoviesSearch's command class
// in NzbDrone.Core/IndexerSearch/MoviesSearchCommand.cs, the grab's ReleaseResource).
type commandCall struct {
	send     func(context.Context, *Client) (int, error)
	resource string
	body     string
	withID   bool // the method returns the command's id
}

// route is the request key of cmd.
func (cmd commandCall) route() string { return "POST " + radarrPrefix + cmd.resource }

var commands = map[string]commandCall{
	"RescanMovie": {
		resource: "/command",
		send:     func(ctx context.Context, c *Client) (int, error) { return 0, c.RescanMovie(ctx, 41) },
		body:     `{"name":"RescanMovie","movieId":41}`,
	},
	"MoviesSearch": {
		resource: "/command",
		send:     func(ctx context.Context, c *Client) (int, error) { return c.MoviesSearch(ctx, []int{41}) },
		body:     `{"name":"MoviesSearch","movieIds":[41]}`,
		withID:   true,
	},
	"Grab": {
		resource: "/release",
		send:     func(ctx context.Context, c *Client) (int, error) { return 0, c.Grab(ctx, "g-1", 41) },
		body:     `{"guid":"g-1","indexerId":41}`,
	},
}

// records captures log records.
type records struct {
	recs []slog.Record
	mu   sync.Mutex
}

func (*records) Enabled(context.Context, slog.Level) bool { return true }
func (r *records) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec.Clone())
	return nil
}
func (r *records) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *records) WithGroup(string) slog.Handler      { return r }

// paths returns the path attr of each record with message msg.
func (r *records) paths(msg string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, rec := range r.recs {
		if rec.Message != msg {
			continue
		}
		path := ""
		rec.Attrs(func(a slog.Attr) bool {
			if a.Key == "path" {
				path = a.Value.String()
			}
			return true
		})
		out = append(out, path)
	}
	return out
}

func TestCommandBodyAndFreshDetection(t *testing.T) {
	for name, cmd := range commands {
		for _, code := range []int{200, 201, 202} {
			t.Run(name+" "+strconv.Itoa(code), func(t *testing.T) {
				f := newFake().radarr(vRadarr)
				c := newClient(t, f)
				cached(t, c, f)
				var got string
				f.on(cmd.route(), body(&got, ans(code, vRadarr, "not a command resource")))
				if _, err := cmd.send(t.Context(), c); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if got != cmd.body {
					t.Errorf("command body = %s, want %s", got, cmd.body)
				}
				wantRequests(t, f, join(detectRadarr, []string{cmd.route()})...)
			})
		}
	}
}

func TestMoviesSearchReturnsTheID(t *testing.T) {
	rec := &records{}
	f := newFake().radarr(vRadarr)
	c := newClient(t, f, WithLogger(slog.New(rec)))
	f.on(command, ans(201, vRadarr, `{"id":77,"name":"MoviesSearch","status":"queued"}`))
	id, err := c.MoviesSearch(t.Context(), []int{41, 42})
	if id != 77 || err != nil {
		t.Errorf("MoviesSearch = (%d, %v), want (77, nil)", id, err)
	}
	if got := rec.paths(unreadAnswer); len(got) != 0 {
		t.Errorf("logged unread answers on %v", got)
	}
}

func TestMoviesSearchUnreadableAnswerIsSuccessWithIDZero(t *testing.T) {
	for name, answer := range map[string]string{
		"not a command resource": "not a command resource",
		"empty":                  "",
		"id 0":                   `{"id":0}`,
		"a negative id":          `{"id":-3}`,
		"a string id":            `{"id":"77"}`,
		"over the cap":           `{"id":77,"x":"` + strings.Repeat("a", transport.MaxObjectBytes) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := &records{}
			f := newFake().radarr(vRadarr)
			c := newClient(t, f, WithLogger(slog.New(rec)))
			f.on(command, ans(201, vRadarr, answer))
			id, err := c.MoviesSearch(t.Context(), []int{41})
			if id != 0 || err != nil {
				t.Errorf("MoviesSearch = (%d, %v), want (0, nil)", id, err)
			}
			if got := rec.paths(unreadAnswer); len(got) != 1 || got[0] != "/api/v3/command" {
				t.Errorf("unread-answer records name %q, want one naming /api/v3/command", got)
			}
			if n := count(f, command); n != 1 {
				t.Errorf("sent %d commands, want 1", n)
			}
		})
	}
}

func TestMoviesSearchRefusesBadInputBeforeAnyRequest(t *testing.T) {
	for name, ids := range map[string][]int{
		"nil":      nil,
		"empty":    {},
		"an id 0":  {3, 0},
		"an id -1": {-1},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			id, err := newClient(t, f).MoviesSearch(t.Context(), ids)
			if id != 0 || !errors.Is(err, errSearchMovies) {
				t.Errorf("MoviesSearch = (%d, %v), want (0, %v)", id, err, errSearchMovies)
			}
			wantRequests(t, f)
		})
	}
}

func TestCommandSentOnce(t *testing.T) {
	for name, cmd := range commands {
		for answerName, tc := range map[string]struct {
			answer     step
			code       int
			attributed bool
		}{
			"a headered 503":    {ans(503, vRadarr, `{"id":77}`), 503, true},
			"a header-less 502": {ans(502, "", `{"id":77}`), 502, false},
		} {
			t.Run(name+" "+answerName, func(t *testing.T) {
				f := newFake().radarr(vRadarr)
				c := newClient(t, f)
				f.on(cmd.route(), tc.answer)
				id, err := cmd.send(t.Context(), c)
				if e := wantStatus(t, err, tc.code); e.Attributed != tc.attributed {
					t.Errorf("Attributed = %v, want %v", e.Attributed, tc.attributed)
				}
				if id != 0 {
					t.Errorf("id = %d on a failed command, want 0", id)
				}
				if n := count(f, cmd.route()); n != 1 {
					t.Errorf("sent %d commands, want 1", n)
				}
			})
		}
	}
}

func TestCommandRedirectIsStatusAndNeverFollowed(t *testing.T) {
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
		for name, cmd := range commands {
			t.Run(clientName+" "+name, func(t *testing.T) {
				for _, code := range []int{301, 302, 303, 307, 308} {
					for _, location := range []string{"/target", "http://[::1"} {
						t.Run(strconv.Itoa(code)+" to "+location, func(t *testing.T) {
							f := newFake().radarr(vRadarr)
							c := newC(t, f)
							f.on(cmd.route(), redirect(code, vRadarr, location))
							_, err := cmd.send(t.Context(), c)
							wantStatus(t, err, code)
							if errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
								t.Error("a redirected command reads as outcome unknown")
							}
							if n := count(f, "GET /target") + count(f, "POST /target"); n != 0 {
								t.Errorf("the redirect target received %d requests", n)
							}
							if n := count(f, cmd.route()); n != 1 {
								t.Errorf("sent %d commands, want 1", n)
							}
						})
					}
				}
			})
		}
	}
}

func TestCommandUnattributableSuccessIsOutcomeUnknown(t *testing.T) {
	for name, cmd := range commands {
		for answerName, tc := range map[string]struct {
			check  func(*testing.T, error)
			answer step
		}{
			"a 2xx from another version": {
				answer: ans(201, "6.4.5.10700", `{"id":77}`),
				check:  func(t *testing.T, err error) { wantChanged(t, err) },
			},
			"a header-less 2xx": {
				answer: ans(201, "", `{"id":77}`),
				check:  func(t *testing.T, err error) { wantUnsupported(t, err, arrapi.ReasonNoVersionHeader) },
			},
		} {
			t.Run(name+" "+answerName, func(t *testing.T) {
				rec := &records{}
				f := newFake().radarr(vRadarr)
				c := newClient(t, f, WithLogger(slog.New(rec)))
				f.on(cmd.route(), tc.answer)
				id, err := cmd.send(t.Context(), c)
				if !errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
					t.Fatalf("err = %v, want ErrCommandOutcomeUnknown", err)
				}
				tc.check(t, err)
				if id != 0 {
					t.Errorf("id = %d on an unknown outcome, want 0", id)
				}
				if got := rec.paths(unreadAnswer); len(got) != 0 {
					t.Errorf("logged unread answers on %v", got)
				}
				if n := count(f, cmd.route()); n != 1 {
					t.Errorf("sent %d commands, want 1", n)
				}
			})
		}
	}
}

func TestCommandNon2xxFromOtherServerKeepsCode(t *testing.T) {
	for name, cmd := range commands {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			f.on(cmd.route(), ans(404, "6.4.5.10700", `{"message":"NotFound"}`))
			_, err := cmd.send(t.Context(), c)
			if e := wantChanged(t, err); e.Status == nil || e.Status.Code != 404 {
				t.Errorf("ServerChangedError.Status = %+v, want the 404", e.Status)
			}
			wantStatus(t, err, 404)
			if errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
				t.Error("a failed command from another server reads as outcome unknown")
			}
			if n := count(f, cmd.route()); n != 1 {
				t.Errorf("sent %d commands, want 1", n)
			}
		})
	}
}

func TestCommandAcrossRepointNeverSent(t *testing.T) {
	for name, cmd := range commands {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			cached(t, c, f)
			f.sonarr4(v4Sonarr)
			_, err := cmd.send(t.Context(), c)
			wantUnsupported(t, err, arrapi.ReasonWrongApp)
			wantRequests(t, f, detectRadarr...)
		})
	}
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
		"Info": func(ctx context.Context, c *Client) error { _, err := c.Info(ctx); return err },
	}
	for name, cmd := range commands {
		calls[name] = func(ctx context.Context, c *Client) error { _, err := cmd.send(ctx, c); return err }
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
					if r == movies || strings.HasPrefix(r, "POST ") {
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
			for _, r := range f.requests() {
				if strings.HasPrefix(r, "POST ") {
					t.Errorf("sent %q after a failed detection", r)
				}
			}
			f.clearLog()
			if _, err := c.Movies(t.Context()); err != nil {
				t.Fatalf("next Movies: %v", err)
			}
			wantRequests(t, f, movies)
		})
	}
}

func TestCommandNotSentAfterContradiction(t *testing.T) {
	for name, cmd := range commands {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			cached(t, c, f)
			hold, release := releaser(t)
			atStatus := make(chan struct{})
			f.on("GET /api/v3/system/status", then(func() { close(atStatus) }, held(hold, ans(200, vRadarr, statusBody("Radarr", vRadarr)))))
			done := make(chan error, 1)
			go func() { _, err := cmd.send(t.Context(), c); done <- err }()
			<-atStatus
			f.on(movies, ans(404, "", ""))
			_, err := c.Movies(t.Context())
			wantStatus(t, err, 404)
			release()
			if e := wantChanged(t, <-done); e.To != "" {
				t.Errorf("ServerChangedError.To = %q, want \"\" after a header-less answer", e.To)
			}
			if n := count(f, cmd.route()); n != 0 {
				t.Errorf("sent %d commands after the contradiction", n)
			}
		})
	}
}
