package sonarr

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

const (
	v4Command = "POST /api/v3/command"
	v5Command = "POST /api/v5/command"

	unreadAnswer = "arrapi: command accepted; its answer was not read"
)

// commandCall is one public method sent at most once, sent for id 41, the resource it
// posts to under the API prefix, and the exact body Sonarr reads (the searches' command
// classes in NzbDrone.Core/IndexerSearch/*Command.cs, the grab's ReleaseResource).
type commandCall struct {
	send     func(context.Context, *Client) (int, error)
	resource string
	body     string
	withID   bool // the method returns the command's id
}

// route is the request key of cmd on the engine at prefix.
func (cmd commandCall) route(prefix string) string { return "POST " + prefix + cmd.resource }

var commands = map[string]commandCall{
	"RescanSeries": {
		resource: "/command",
		send:     func(ctx context.Context, c *Client) (int, error) { return 0, c.RescanSeries(ctx, 41) },
		body:     `{"name":"RescanSeries","seriesId":41}`,
	},
	"SeriesSearch": {
		resource: "/command",
		send:     func(ctx context.Context, c *Client) (int, error) { return c.SeriesSearch(ctx, 41) },
		body:     `{"name":"SeriesSearch","seriesId":41}`,
		withID:   true,
	},
	"SeasonSearch": {
		resource: "/command",
		send:     func(ctx context.Context, c *Client) (int, error) { return c.SeasonSearch(ctx, SeasonRef{SeriesID: 41}) },
		body:     `{"name":"SeasonSearch","seasonNumber":0,"seriesId":41}`,
		withID:   true,
	},
	"EpisodeSearch": {
		resource: "/command",
		send:     func(ctx context.Context, c *Client) (int, error) { return c.EpisodeSearch(ctx, []int{41, 42}) },
		body:     `{"name":"EpisodeSearch","episodeIds":[41,42]}`,
		withID:   true,
	},
	"Grab": {
		resource: "/release",
		send:     func(ctx context.Context, c *Client) (int, error) { return 0, c.Grab(ctx, "g-1", 41) },
		body:     `{"guid":"g-1","indexerId":41}`,
	},
}

func sendErr(cmd commandCall) func(context.Context, *Client) error {
	return func(ctx context.Context, c *Client) error {
		_, err := cmd.send(ctx, c)
		return err
	}
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

var sonarrEngines = map[string]struct {
	serve   func(*fake) *fake
	version string
	prefix  string
	detect  []string
}{
	"Sonarr v4 (API v3)": {func(f *fake) *fake { return f.sonarr4(v4) }, v4, sonarrV4Prefix, detectV4},
	"Sonarr v5 (API v5)": {func(f *fake) *fake { return f.sonarr5(v5) }, v5, sonarrV5Prefix, detectV5},
}

func TestCommandBodyAndFreshDetection(t *testing.T) {
	for engineName, tc := range sonarrEngines {
		for name, cmd := range commands {
			t.Run(engineName+" "+name, func(t *testing.T) {
				f := tc.serve(newFake())
				c := newClient(t, f)
				cached(t, c, f)
				var got string
				f.on(cmd.route(tc.prefix), body(&got, ans(201, tc.version, "not a command resource")))
				if _, err := cmd.send(t.Context(), c); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if got != cmd.body {
					t.Errorf("command body = %s, want %s", got, cmd.body)
				}
				wantRequests(t, f, join(tc.detect, []string{cmd.route(tc.prefix)})...)
			})
		}
	}
}

func TestCommandReturnsTheID(t *testing.T) {
	for engineName, tc := range sonarrEngines {
		for name, cmd := range commands {
			if !cmd.withID {
				continue
			}
			t.Run(engineName+" "+name, func(t *testing.T) {
				rec := &records{}
				f := tc.serve(newFake())
				c := newClient(t, f, WithLogger(slog.New(rec)))
				f.on(cmd.route(tc.prefix), ans(201, tc.version, `{"id":77,"name":"`+name+`","status":"queued"}`))
				id, err := cmd.send(t.Context(), c)
				if id != 77 || err != nil {
					t.Errorf("%s = (%d, %v), want (77, nil)", name, id, err)
				}
				if got := rec.paths(unreadAnswer); len(got) != 0 {
					t.Errorf("logged unread answers on %v", got)
				}
			})
		}
	}
}

func TestCommandUnreadableAnswerIsSuccessWithIDZero(t *testing.T) {
	answers := map[string]string{
		"not a command resource": "not a command resource",
		"empty":                  "",
		"id 0":                   `{"id":0}`,
		"a negative id":          `{"id":-3}`,
		"a string id":            `{"id":"77"}`,
		"over the cap":           `{"id":77,"x":"` + strings.Repeat("a", transport.MaxObjectBytes) + `"}`,
	}
	for engineName, tc := range sonarrEngines {
		for name, cmd := range commands {
			if !cmd.withID {
				continue
			}
			for answerName, answer := range answers {
				t.Run(engineName+" "+name+" "+answerName, func(t *testing.T) {
					rec := &records{}
					f := tc.serve(newFake())
					c := newClient(t, f, WithLogger(slog.New(rec)))
					f.on(cmd.route(tc.prefix), ans(201, tc.version, answer))
					id, err := cmd.send(t.Context(), c)
					if id != 0 || err != nil {
						t.Errorf("%s = (%d, %v), want (0, nil)", name, id, err)
					}
					path := strings.TrimPrefix(cmd.route(tc.prefix), "POST ")
					if got := rec.paths(unreadAnswer); len(got) != 1 || got[0] != path {
						t.Errorf("unread-answer records name %q, want one naming %s", got, path)
					}
					if n := count(f, cmd.route(tc.prefix)); n != 1 {
						t.Errorf("sent %d commands, want 1", n)
					}
				})
			}
		}
	}
}

func TestCommandSentOnceOn503(t *testing.T) {
	for name, cmd := range commands {
		for headerName, tc := range map[string]struct {
			header     string
			attributed bool
		}{
			"with the detected version": {v4, true},
			"without the header":        {"", false},
		} {
			t.Run(name+" "+headerName, func(t *testing.T) {
				f := newFake().sonarr4(v4)
				c := newClient(t, f)
				f.on(cmd.route(sonarrV4Prefix), ans(503, tc.header, `{"id":77}`))
				id, err := cmd.send(t.Context(), c)
				if e := wantStatus(t, err, 503); e.Attributed != tc.attributed {
					t.Errorf("Attributed = %v, want %v", e.Attributed, tc.attributed)
				}
				if id != 0 {
					t.Errorf("id = %d on a failed command, want 0", id)
				}
				if n := count(f, cmd.route(sonarrV4Prefix)); n != 1 {
					t.Errorf("sent %d commands, want 1", n)
				}
			})
		}
	}
}

func TestCommandAnswered200IsSuccess(t *testing.T) {
	for name, cmd := range commands {
		t.Run(name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			f.on(cmd.route(sonarrV4Prefix), ans(200, v4, `{"id":5}`))
			id, err := cmd.send(t.Context(), newClient(t, f))
			if err != nil {
				t.Fatalf("%s on a 200 carrying the sent version: %v", name, err)
			}
			if cmd.withID && id != 5 {
				t.Errorf("id = %d, want 5", id)
			}
		})
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
							f := newFake().sonarr4(v4)
							c := newC(t, f)
							f.on(cmd.route(sonarrV4Prefix), redirectCode(code, v4, location))
							_, err := cmd.send(t.Context(), c)
							wantStatus(t, err, code)
							if errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
								t.Error("a redirected command reads as outcome unknown")
							}
							if n := count(f, "GET /target") + count(f, "POST /target"); n != 0 {
								t.Errorf("the redirect target received %d requests", n)
							}
							if n := count(f, cmd.route(sonarrV4Prefix)); n != 1 {
								t.Errorf("sent %d commands, want 1", n)
							}
						})
					}
				}
			})
		}
	}
}

func TestCommandAcrossUpgradeNeverReachesAPIv3(t *testing.T) {
	for name, cmd := range commands {
		t.Run(name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			c := newClient(t, f)
			cached(t, c, f)
			f.sonarr5(v5)
			if _, err := cmd.send(t.Context(), c); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			wantRequests(t, f, join(detectV5, []string{cmd.route(sonarrV5Prefix)})...)
		})
	}
}

// outcomeUnknown checks that err wraps ErrCommandOutcomeUnknown, that the id is 0,
// that the answer was not read, and that the request went out once on route.
func outcomeUnknown(t *testing.T, f *fake, rec *records, route string, id int, err error) {
	t.Helper()
	if !errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
		t.Fatalf("err = %v, want ErrCommandOutcomeUnknown", err)
	}
	if id != 0 {
		t.Errorf("id = %d on an unknown outcome, want 0", id)
	}
	if got := rec.paths(unreadAnswer); len(got) != 0 {
		t.Errorf("logged unread answers on %v", got)
	}
	if n := count(f, route); n != 1 {
		t.Errorf("sent %d commands, want 1", n)
	}
}

func TestCommandAnswerFromSonarr5IsOutcomeUnknown(t *testing.T) {
	for name, cmd := range commands {
		t.Run(name, func(t *testing.T) {
			rec := &records{}
			f := newFake().sonarr4(v4)
			c := newClient(t, f, WithLogger(slog.New(rec)))
			f.on(cmd.route(sonarrV4Prefix), ans(201, v5, `{"id":77}`))
			id, err := cmd.send(t.Context(), c)
			outcomeUnknown(t, f, rec, cmd.route(sonarrV4Prefix), id, err)
			wantChanged(t, err)
		})
	}
}

func TestCommandHeaderless2xxIsOutcomeUnknown(t *testing.T) {
	for name, cmd := range commands {
		t.Run(name, func(t *testing.T) {
			rec := &records{}
			f := newFake().sonarr4(v4)
			c := newClient(t, f, WithLogger(slog.New(rec)))
			f.on(cmd.route(sonarrV4Prefix), ans(201, "", `{"id":77}`))
			id, err := cmd.send(t.Context(), c)
			outcomeUnknown(t, f, rec, cmd.route(sonarrV4Prefix), id, err)
			wantUnsupported(t, err, arrapi.ReasonNoVersionHeader)
		})
	}
}

func TestCommandSwitchToSonarr5Headerless2xxIsOutcomeUnknown(t *testing.T) {
	for name, cmd := range commands {
		t.Run(name, func(t *testing.T) {
			rec := &records{}
			f := newFake().sonarr4(v4)
			c := newClient(t, f, WithLogger(slog.New(rec)))
			f.on("GET /api/v3/system/status", then(func() {
				f.sonarr5(v5)
				f.on(cmd.route(sonarrV4Prefix), ans(201, "", `{"id":77}`))
			}, ans(200, v4, statusBody("Sonarr", v4))))
			id, err := cmd.send(t.Context(), c)
			outcomeUnknown(t, f, rec, cmd.route(sonarrV4Prefix), id, err)
			wantRequests(t, f, join(detectV4, []string{cmd.route(sonarrV4Prefix)})...)
		})
	}
}

func TestCommandNon2xxFromOtherServerKeepsCode(t *testing.T) {
	for name, cmd := range commands {
		t.Run(name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			c := newClient(t, f)
			f.on(cmd.route(sonarrV4Prefix), ans(404, v5, `{"message":"NotFound"}`))
			_, err := cmd.send(t.Context(), c)
			if e := wantChanged(t, err); e.Status == nil || e.Status.Code != 404 {
				t.Errorf("ServerChangedError.Status = %+v, want the 404", e.Status)
			}
			wantStatus(t, err, 404)
			if n := count(f, cmd.route(sonarrV4Prefix)); n != 1 {
				t.Errorf("sent %d commands, want 1", n)
			}
		})
	}
}

func TestSearchRefusesBadInputBeforeAnyRequest(t *testing.T) {
	for name, tc := range map[string]struct {
		send func(context.Context, *Client) (int, error)
		want error
	}{
		"SeriesSearch id 0": {
			func(ctx context.Context, c *Client) (int, error) { return c.SeriesSearch(ctx, 0) },
			errSearchSeriesID,
		},
		"SeasonSearch season -1": {
			func(ctx context.Context, c *Client) (int, error) {
				return c.SeasonSearch(ctx, SeasonRef{SeriesID: 1, Number: -1})
			},
			errSearchSeason,
		},
		"SeasonSearch series id 0": {
			func(ctx context.Context, c *Client) (int, error) {
				return c.SeasonSearch(ctx, SeasonRef{SeriesID: 0, Number: 1})
			},
			errSearchSeriesID,
		},
		"EpisodeSearch nil": {
			func(ctx context.Context, c *Client) (int, error) { return c.EpisodeSearch(ctx, nil) },
			errSearchEpisodes,
		},
		"EpisodeSearch an id 0": {
			func(ctx context.Context, c *Client) (int, error) { return c.EpisodeSearch(ctx, []int{3, 0}) },
			errSearchEpisodes,
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			id, err := tc.send(t.Context(), newClient(t, f))
			if id != 0 || !errors.Is(err, tc.want) {
				t.Errorf("= (%d, %v), want (0, %v)", id, err, tc.want)
			}
			wantRequests(t, f)
		})
	}
}
