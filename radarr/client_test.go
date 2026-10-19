package radarr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v3"
)

func TestNewMakesNoRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	before := runtime.NumGoroutine()
	c, err := New(srv.URL, testKey, WithMaxAttempts(2), WithTimeout(time.Second))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("New started %d goroutines", after-before)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("New sent %d requests, want none", n)
	}
}

// TestNewRefusesAKeyAnEncoderRewrites: a key outside letters, digits, '.', '_' and
// '-' is refused with a sentence naming the rule, never the key; Sonarr's and Radarr's
// own keys are 32 hex digits (ConfigFileProvider.cs, Guid.NewGuid without dashes).
func TestNewRefusesAKeyAnEncoderRewrites(t *testing.T) {
	const refusal = "arrapi: key may hold only letters, digits, '.', '_' and '-'"
	for _, key := range []arrapi.APIKey{"bad key", "bad+key", "bad%2Fkey", "bad~key", "bad\"key"} {
		c, err := New("http://radarr.test", key)
		if err == nil {
			c.Close()
			t.Errorf("New(%q) accepted the key", key)
			continue
		}
		if err.Error() != refusal {
			t.Errorf("New(%q) = %q, want %q", key, err.Error(), refusal)
		}
	}
	c, err := New("http://radarr.test", "0123456789abcdef0123456789abcdef") // gitleaks:allow (fake key)
	if err != nil {
		t.Fatalf("New refused a provider-format key: %v", err)
	}
	c.Close()
}

func TestNewErrorNeverRendersURLOrKey(t *testing.T) {
	const secret = "pw-3c8b"
	for name, tc := range map[string]struct{ url, hidden string }{
		"a key in the query":            {"http://radarr.test/?apikey=" + string(testKey), string(testKey)},
		"a password in a malformed URL": {"https://u:" + secret + "@exa mple", secret},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := New(tc.url, testKey)
			if err == nil {
				c.Close()
				t.Fatal("New accepted the URL")
			}
			for e := err; e != nil; e = errors.Unwrap(e) {
				if s := e.Error(); strings.Contains(s, tc.hidden) || strings.Contains(s, tc.url) {
					t.Errorf("error %T renders the input: %q", e, s)
				}
			}
			if s := fmt.Sprintf("%+v", err); strings.Contains(s, tc.hidden) {
				t.Errorf("%%+v renders the input: %q", s)
			}
		})
	}
}

func TestWithHTTPClientCarriesDetectionProbes(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	if _, err := c.Movies(t.Context()); err != nil {
		t.Fatalf("Movies: %v", err)
	}
	wantRequests(t, f, join(detectRadarr, []string{movies})...)
}

func TestInfo(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	for range 2 {
		got, err := c.Info(t.Context())
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		assertFields(t, got, Info{Version: vRadarr, API: arrapi.APIv3})
	}
	if n := count(f, "GET /api"); n != 2 {
		t.Errorf("two Info calls sent %d GET /api, want a fresh detection each", n)
	}
	f.clearLog()
	if _, err := c.Movies(t.Context()); err != nil {
		t.Fatalf("Movies: %v", err)
	}
	wantRequests(t, f, movies)
}

func TestInfoReportsDeprecated(t *testing.T) {
	const v7 = "7.0.0.1"
	f := newFake().radarr(v7)
	f.on("GET /api", ans(200, v7, apiBody("v4", "v3")))
	f.on("GET /api/v4/system/status", ans(200, v7, statusBody("Radarr", v7)))
	got, err := newClient(t, f).Info(t.Context())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	assertFields(t, got, Info{Version: v7, API: arrapi.APIv3, Deprecated: true})
}

func TestInfoBoundedFiveSeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake().radarr(vRadarr)
		never := make(chan struct{})
		f.on("GET /api", held(never, ans(200, vRadarr, apiBody("v3"))))
		c := newClient(t, f, WithTimeout(time.Hour))
		start := time.Now()
		_, err := c.Info(context.Background())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Info err = %v, want context.DeadlineExceeded", err)
		}
		if took := time.Since(start); took != 5*time.Second {
			t.Errorf("Info took %v, want 5s", took)
		}
	})
}

// TestDetectionArms drives each detection arm a Radarr client can reach through Info,
// whose probes send one attempt each.
func TestDetectionArms(t *testing.T) {
	const v7 = "7.0.0.1"
	for name, tc := range map[string]struct {
		serve    func(*fake)
		check    func(*testing.T, Info, error)
		requests []string
	}{
		"arm A: a Radarr /api announcing API v3": {
			serve:    func(f *fake) { f.radarr(vRadarr) },
			requests: detectRadarr,
			check:    detected,
		},
		"arms B, C and E: /api hidden behind a 404": {
			serve:    func(f *fake) { f.radarr(vRadarr).on("GET /api", ans(404, "", "")) },
			requests: detectRadarr,
			check:    detected,
		},
		"arms B, C and E: a web page at /api": {
			serve:    func(f *fake) { f.radarr(vRadarr).on("GET /api", ans(200, "", "<!doctype html><html></html>")) },
			requests: detectRadarr,
			check:    detected,
		},
		"arm G: a 401 on /api stays a 401": {
			serve:    func(f *fake) { f.radarr(vRadarr).on("GET /api", ans(401, "", "")) },
			requests: []string{"GET /api"},
			check:    func(t *testing.T, _ Info, err error) { wantStatus(t, err, 401) },
		},
		"arm G: a 503 on the status path": {
			serve:    func(f *fake) { f.radarr(vRadarr).on("GET /api/v3/system/status", ans(503, vRadarr, "")) },
			requests: detectRadarr,
			check:    func(t *testing.T, _ Info, err error) { wantStatus(t, err, 503) },
		},
		"arm H: nothing answers": {
			serve: func(f *fake) {
				f.radarr(vRadarr)
				f.on("GET /api", ans(404, "", ""))
				f.on("GET /api/v3/system/status", ans(404, "", ""))
			},
			requests: detectRadarr,
			check: func(t *testing.T, _ Info, err error) {
				wantStatus(t, err, 404)
				if errors.As(err, new(*arrapi.UnsupportedAPIError)) {
					t.Errorf("err = %v, want only the 404, not UnsupportedAPIError", err)
				}
			},
		},
		"arm I: a Sonarr v4 (API v3) answers": {
			serve:    func(f *fake) { f.sonarr4(v4Sonarr) },
			requests: detectRadarr,
			check:    func(t *testing.T, _ Info, err error) { wantUnsupported(t, err, arrapi.ReasonWrongApp) },
		},
		"arm I: a Sonarr v5 (API v5) is probed on /api/v5, never on /api/v3": {
			serve:    func(f *fake) { f.sonarr5(v5Sonarr) },
			requests: []string{"GET /api", "GET /api/v5/system/status"},
			check:    func(t *testing.T, _ Info, err error) { wantUnsupported(t, err, arrapi.ReasonWrongApp) },
		},
		"arm I': a web page on the status path": {
			serve: func(f *fake) {
				f.radarr(vRadarr).on("GET /api/v3/system/status", ans(200, vRadarr, "<!doctype html><html></html>"))
			},
			requests: detectRadarr,
			check:    func(t *testing.T, _ Info, err error) { wantUnsupported(t, err, arrapi.ReasonNotAPI) },
		},
		"arm I': a status without appName": {
			serve: func(f *fake) {
				f.radarr(vRadarr).on("GET /api/v3/system/status", ans(200, vRadarr, `{"version":"`+vRadarr+`"}`))
			},
			requests: detectRadarr,
			check:    func(t *testing.T, _ Info, err error) { wantUnsupported(t, err, arrapi.ReasonNotAPI) },
		},
		"arm J: a Radarr announcing only API v4": {
			serve: func(f *fake) {
				f.radarr(v7)
				f.on("GET /api", ans(200, v7, apiBody("v4")))
				f.on("GET /api/v4/system/status", ans(200, v7, statusBody("Radarr", v7)))
			},
			requests: []string{"GET /api", "GET /api/v4/system/status"},
			check:    func(t *testing.T, _ Info, err error) { wantUnsupported(t, err, arrapi.ReasonUnknownGeneration) },
		},
		"arm V: a status answer without the header": {
			serve: func(f *fake) {
				f.radarr(vRadarr).on("GET /api/v3/system/status", ans(200, "", statusBody("Radarr", vRadarr)))
			},
			requests: detectRadarr,
			check:    func(t *testing.T, _ Info, err error) { wantUnsupported(t, err, arrapi.ReasonNoVersionHeader) },
		},
		"arm V: a malformed header": {
			serve: func(f *fake) {
				f.radarr(vRadarr).on("GET /api/v3/system/status", ans(200, "abc", statusBody("Radarr", vRadarr)))
			},
			requests: detectRadarr,
			check:    func(t *testing.T, _ Info, err error) { wantUnsupported(t, err, arrapi.ReasonNoVersionHeader) },
		},
		"arm W: two versions answer the probes": {
			serve: func(f *fake) {
				f.radarr(vRadarr).on("GET /api/v3/system/status", ans(200, v7, statusBody("Radarr", v7)))
			},
			requests: detectRadarr,
			check:    func(t *testing.T, _ Info, err error) { wantChanged(t, err) },
		},
		"arm K: /api fails at dial": {
			serve:    func(f *fake) { f.radarr(vRadarr).on("GET /api", dialFails) },
			requests: []string{"GET /api"},
			check: func(t *testing.T, _ Info, err error) {
				if err == nil || errors.As(err, new(*arrapi.StatusError)) {
					t.Errorf("err = %v, want the transport error", err)
				}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			tc.serve(f)
			info, err := newClient(t, f).Info(t.Context())
			tc.check(t, info, err)
			wantRequests(t, f, tc.requests...)
		})
	}
}

func detected(t *testing.T, got Info, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	assertFields(t, got, Info{Version: vRadarr, API: arrapi.APIv3})
}

func TestDetectConcurrentFirstCallsShareOneDetection(t *testing.T) {
	f := newFake().radarr(vRadarr)
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, testKey)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := c.Movies(t.Context()); err != nil {
				t.Errorf("Movies: %v", err)
			}
		})
	}
	wg.Wait()
	if n := count(f, "GET /api"); n != 1 {
		t.Errorf("16 first calls sent %d GET /api, want 1", n)
	}
	if n := count(f, movies); n != 16 {
		t.Errorf("16 first calls sent %d movie reads, want 16", n)
	}
}

func TestMovieByIDAbsent(t *testing.T) {
	for name, body := range map[string]string{
		"an empty body":         "",
		"a ProblemDetails body": `{"type":"https://tools.ietf.org/html/rfc9110#section-15.5.5","title":"Not Found","status":404}`,
		"a Radarr message body": `{"message":"NotFound"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			cached(t, c, f)
			f.on("GET /api/v3/movie/9", ans(404, vRadarr, body))
			m, found, err := c.MovieByID(t.Context(), 9)
			if err != nil || found {
				t.Fatalf("MovieByID = (%+v, %v, %v), want absent", m, found, err)
			}
			assertFields(t, m, Movie{})
			wantRequests(t, f, "GET /api/v3/movie/9")
		})
	}
}

func TestHeaderless404IsStatusAndNextCallDetects(t *testing.T) {
	for name, tc := range map[string]struct {
		call  func(*Client) error
		route string
	}{
		"a list path": {route: "GET /api/v3/history/since", call: func(c *Client) error {
			_, err := c.HistorySince(t.Context(), time.Unix(0, 0))
			return err
		}},
		"a by-id read": {route: "GET /api/v3/movie/5", call: func(c *Client) error {
			_, found, err := c.MovieByID(t.Context(), 5)
			if found {
				t.Error("MovieByID found a movie in a header-less 404")
			}
			return err
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			cached(t, c, f)
			f.on(tc.route, ans(404, "", `{"message":"Not Found"}`))
			wantStatus(t, tc.call(c), 404)
			wantRequests(t, f, tc.route)
			f.clearLog()
			if _, err := c.Movies(t.Context()); err != nil {
				t.Fatalf("next Movies: %v", err)
			}
			wantRequests(t, f, join(detectRadarr, []string{movies})...)
			f.clearLog()
			if _, err := c.Movies(t.Context()); err != nil {
				t.Fatalf("Movies after that: %v", err)
			}
			wantRequests(t, f, movies)
		})
	}
}

func TestHeaderless200IsRefused(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	cached(t, c, f)
	f.on(movies, ans(200, "", movieList))
	got, err := c.Movies(t.Context())
	wantUnsupported(t, err, arrapi.ReasonNoVersionHeader)
	if got != nil {
		t.Errorf("Movies decoded a header-less answer: %+v", got)
	}
	wantRequests(t, f, movies)
}

func TestRetriedOnItsDetection(t *testing.T) {
	for name, first := range map[string]step{
		"a headered 503": ans(503, vRadarr, ""),
		"a dial error":   dialFails,
		"a headered 429": ans(429, vRadarr, ""),
		"a headered 500": ans(500, vRadarr, `{"message":"database is locked"}`),
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newFake().radarr(vRadarr)
				c := newClient(t, f)
				cached(t, c, f)
				f.on(movies, first, ans(200, vRadarr, movieList))
				got, err := c.Movies(t.Context())
				if err != nil {
					t.Fatalf("Movies: %v", err)
				}
				wantTitle(t, got, "Radarr Movie")
				wantRequests(t, f, movies, movies)
			})
		})
	}
}

func TestHeaderlessFiveXXGoesFresh(t *testing.T) {
	for name, tc := range map[string]struct {
		answers  []step
		attempts int
		code     int // 0: success
		data     int
	}{
		"502 then 200":                  {[]step{ans(502, "", ""), ans(200, vRadarr, movieList)}, 3, 0, 2},
		"502 then 502 under 2 attempts": {[]step{ans(502, "", "")}, 2, 502, 2},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newFake().radarr(vRadarr)
				c := newClient(t, f, WithMaxAttempts(tc.attempts))
				cached(t, c, f)
				f.on(movies, tc.answers...)
				got, err := c.Movies(t.Context())
				if tc.code != 0 {
					wantStatus(t, err, tc.code)
				} else {
					if err != nil {
						t.Fatalf("Movies: %v", err)
					}
					wantTitle(t, got, "Radarr Movie")
				}
				if n := count(f, "GET /api"); n != 1 {
					t.Errorf("Movies made %d detections, want 1", n)
				}
				if n := count(f, movies); n != tc.data {
					t.Errorf("Movies sent %d data requests, want %d", n, tc.data)
				}
			})
		})
	}
}

func TestFirstCallHeaderlessFiveXXRetriesOnItsDetection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake().radarr(vRadarr)
		c := newClient(t, f)
		f.on(movies, ans(502, "", ""), ans(200, vRadarr, movieList))
		got, err := c.Movies(t.Context())
		if err != nil {
			t.Fatalf("Movies: %v", err)
		}
		wantTitle(t, got, "Radarr Movie")
		wantRequests(t, f, join(detectRadarr, []string{movies, movies})...)
		f.clearLog()
		if _, err := c.Movies(t.Context()); err != nil {
			t.Fatalf("second Movies: %v", err)
		}
		wantRequests(t, f, join(detectRadarr, []string{movies})...)
	})
}

func TestMinorUpgradeRedetectsAndReissues(t *testing.T) {
	const upgraded = "6.4.5.10700"
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	cached(t, c, f)
	f.radarr(upgraded)
	f.on(movies, ans(200, upgraded, movieList))
	got, err := c.Movies(t.Context())
	if err != nil {
		t.Fatalf("Movies: %v", err)
	}
	wantTitle(t, got, "Radarr Movie")
	wantRequests(t, f, join([]string{movies}, detectRadarr, []string{movies})...)
}

func TestRepointToSonarrIsWrongApp(t *testing.T) {
	for name, tc := range map[string]struct {
		serve  func(*fake)
		detect []string
	}{
		"a Sonarr v4 (API v3) on the shared /api/v3/tag route": {
			serve: func(f *fake) {
				f.sonarr4(v4Sonarr).on("GET /api/v3/tag", ans(200, v4Sonarr, `[{"id":1,"label":"sonarr-tag"}]`))
			},
			detect: detectRadarr,
		},
		"a Sonarr v5 (API v5), probed on /api/v5": {
			serve: func(f *fake) {
				f.sonarr5(v5Sonarr).on("GET /api/v3/tag", ans(200, v5Sonarr, `[{"id":1,"label":"sonarr-tag"}]`))
			},
			detect: []string{"GET /api", "GET /api/v5/system/status"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			cached(t, c, f)
			tc.serve(f)
			tags, err := c.Tags(t.Context())
			wantUnsupported(t, err, arrapi.ReasonWrongApp)
			if tags != nil {
				t.Errorf("Tags = %v, want none", tags)
			}
			wantRequests(t, f, join([]string{"GET /api/v3/tag"}, tc.detect)...)
			f.clearLog()
			_, err = c.Tags(t.Context())
			wantUnsupported(t, err, arrapi.ReasonWrongApp)
			wantRequests(t, f, tc.detect...)
		})
	}
}

func TestRedirectAnswerEndsDetection(t *testing.T) {
	const upgraded = "6.4.5.10700"
	for name, location := range map[string]string{
		"a malformed Location": "http://[::1",
		"a same-origin target": "/hop/movie",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			srv := httptest.NewServer(f)
			t.Cleanup(srv.Close)
			c, err := New(srv.URL, testKey, WithBaseDelay(time.Millisecond), WithLogger(slog.New(slog.DiscardHandler)))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			t.Cleanup(c.Close)
			cached(t, c, f)
			f.radarr(upgraded)
			f.on(movies, redirect(http.StatusFound, upgraded, location), ans(200, upgraded, movieList))
			got, err := c.Movies(t.Context())
			if err != nil {
				t.Fatalf("Movies: %v", err)
			}
			wantTitle(t, got, "Radarr Movie")
			wantRequests(t, f, join([]string{movies}, detectRadarr, []string{movies})...)
		})
	}
}

func TestHeaderlessRedirectHopFailureDetectsNext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake().radarr(vRadarr)
		c := newClient(t, f)
		cached(t, c, f)
		f.on(movies, redirect(http.StatusFound, "", "/hop/movie"), ans(200, vRadarr, movieList))
		f.on("GET /hop/movie", dialFails)
		got, err := c.Movies(t.Context())
		if err != nil {
			t.Fatalf("Movies: %v", err)
		}
		wantTitle(t, got, "Radarr Movie")
		wantRequests(t, f, join([]string{movies, "GET /hop/movie"}, detectRadarr, []string{movies})...)
	})
}

func TestEngineRequests(t *testing.T) {
	setup := func(t *testing.T) (*Client, *fake) {
		f := newFake().radarr(vRadarr)
		c := newClient(t, f)
		if _, err := c.Tags(t.Context()); err != nil {
			t.Fatalf("detect: %v", err)
		}
		f.clearLog()
		return c, f
	}
	t.Run("Movies", func(t *testing.T) {
		c, f := setup(t)
		f.on(movies, ans(200, vRadarr, "["+radarrMovieBody+"]"))
		got, err := c.Movies(t.Context())
		if err != nil || len(got) != 1 || got[0].ID != 41 || got[0].File == nil {
			t.Fatalf("Movies = (%+v, %v), want the one movie with its file", got, err)
		}
		assertFields(t, *got[0].File, radarrMovieFileWant)
		wantURIs(t, f, movies)
	})
	t.Run("MovieByID", func(t *testing.T) {
		c, f := setup(t)
		f.on("GET /api/v3/movie/41", ans(200, vRadarr, radarrMovieBody))
		got, found, err := c.MovieByID(t.Context(), 41)
		if err != nil || !found || got.ID != 41 || got.Title != "Example Movie" {
			t.Fatalf("MovieByID = (%+v, %v, %v), want movie 41", got, found, err)
		}
		wantURIs(t, f, "GET /api/v3/movie/41")
	})
	t.Run("HistorySince", func(t *testing.T) {
		c, f := setup(t)
		f.on("GET /api/v3/history/since", ans(200, vRadarr,
			`[{"id":1,"movieId":41,"date":"2001-02-03T04:05:06Z","eventType":"grabbed"}]`))
		since := time.Date(2001, 2, 3, 5, 5, 6, 999_000_000, time.FixedZone("plus1", 3600))
		got, err := c.HistorySince(t.Context(), since)
		if err != nil || len(got) != 1 || got[0].EventType != EventGrabbed || got[0].MovieID != 41 {
			t.Fatalf("HistorySince = (%+v, %v), want the one record", got, err)
		}
		wantURIs(t, f, "GET /api/v3/history/since?date=2001-02-03T04:05:06Z&includeMovie=false")
	})
	t.Run("Tags", func(t *testing.T) {
		c, f := setup(t)
		f.on("GET /api/v3/tag", ans(200, vRadarr, `[{"id":2,"label":"anime"}]`))
		got, err := c.Tags(t.Context())
		if err != nil || !slices.Equal(got, []arrapi.Tag{{ID: 2, Label: "anime"}}) {
			t.Fatalf("Tags = (%v, %v), want the one tag", got, err)
		}
		wantURIs(t, f, "GET /api/v3/tag")
	})
	t.Run("RootFolders", func(t *testing.T) {
		c, f := setup(t)
		f.on("GET /api/v3/rootfolder", ans(200, vRadarr, `[{"id":1,"path":"/movies/","freeSpace":5}]`))
		got, err := c.RootFolders(t.Context())
		if err != nil || !slices.Equal(got, []string{"/movies/"}) {
			t.Fatalf("RootFolders = (%v, %v), want the one path", got, err)
		}
		wantURIs(t, f, "GET /api/v3/rootfolder")
	})
}

func TestHistorySinceFiltersClientSide(t *testing.T) {
	const records = `[
		{"id":1,"movieId":1,"date":"2001-01-01T00:00:01Z","eventType":"grabbed"},
		{"id":2,"movieId":1,"date":"2001-01-01T00:00:02Z","eventType":"somethingNew"},
		{"id":3,"movieId":1,"date":"2001-01-01T00:00:03Z","eventType":"movieFileDeleted"},
		{"id":4,"movieId":1,"date":"2001-01-01T00:00:04Z","eventType":"grabbed"},
		{"id":5,"movieId":1,"date":"2001-01-01T00:00:05Z","eventType":5}
	]`
	ids := func(recs []HistoryRecord) []int {
		out := make([]int, len(recs))
		for i, r := range recs {
			out[i] = r.ID
		}
		return out
	}
	for name, tc := range map[string]struct {
		types []EventType
		want  []int
	}{
		"no types keeps every record, unknown kinds included": {nil, []int{1, 2, 3, 4, 5}},
		"listed types in the server's order":                  {[]EventType{EventFileDeleted, EventGrabbed}, []int{1, 3, 4}},
		"an unlisted type keeps nothing":                      {[]EventType{EventDownloadFailed}, []int{}},
		"EventType(0) matches nothing":                        {[]EventType{0}, []int{}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			cached(t, c, f)
			f.on("GET /api/v3/history/since", ans(200, vRadarr, records))
			got, err := c.HistorySince(t.Context(), time.Unix(0, 0), tc.types...)
			if err != nil {
				t.Fatalf("HistorySince: %v", err)
			}
			if !slices.Equal(ids(got), tc.want) {
				t.Errorf("HistorySince ids = %v, want %v", ids(got), tc.want)
			}
			if n := len(f.requestURIs()); n != 1 {
				t.Errorf("HistorySince sent %d requests, want 1", n)
			}
		})
	}
}

// TestHistorySinceDecodeErrorNamesTheField pins the decode error a caller sees for a
// record of a kind no server writes: the field and the type, none of the value.
func TestHistorySinceDecodeErrorNamesTheField(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	cached(t, c, f)
	f.on("GET /api/v3/history/since", ans(200, vRadarr,
		`[{"id":1,"movieId":1,"date":"2001-01-01T00:00:01Z","eventType":{"token":"grabbed-7f3a"}}]`))
	_, err := c.HistorySince(t.Context(), time.Unix(0, 0))
	const want = "arrapi: decode /api/v3/history/since: field eventType: a JSON object cannot decode into radarr.EventType"
	if err == nil || err.Error() != want {
		t.Errorf("HistorySince = %v, want %q", err, want)
	}
}

func TestReadErrorReturnsNoValue(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	cached(t, c, f)
	f.on(movies, ans(200, vRadarr, `{"not":"a list"}`))
	got, err := c.Movies(t.Context())
	if err == nil || got != nil {
		t.Errorf("Movies on an undecodable answer = (%+v, %v), want an error and no movies", got, err)
	}
	f.on("GET /api/v3/movie/3", ans(200, vRadarr, `[]`))
	m, found, err := c.MovieByID(t.Context(), 3)
	if err == nil || found || m.ID != 0 {
		t.Errorf("MovieByID on an undecodable answer = (%+v, %v, %v), want an error, not found", m, found, err)
	}
}

// TestNullAnswersAreDecodeErrors pins that no list or by-id read turns a null answer,
// or a null list item, into an empty library or a zero-value resource.
func TestNullAnswersAreDecodeErrors(t *testing.T) {
	lists := map[string]struct {
		route string
		read  func(*Client) (gotValue bool, err error)
	}{
		"Movies": {movies, func(c *Client) (bool, error) {
			got, err := c.Movies(t.Context())
			return got != nil, err
		}},
		"HistorySince": {"GET /api/v3/history/since", func(c *Client) (bool, error) {
			got, err := c.HistorySince(t.Context(), time.Unix(0, 0))
			return got != nil, err
		}},
		"Tags": {"GET /api/v3/tag", func(c *Client) (bool, error) {
			got, err := c.Tags(t.Context())
			return got != nil, err
		}},
		"RootFolders": {"GET /api/v3/rootfolder", func(c *Client) (bool, error) {
			got, err := c.RootFolders(t.Context())
			return got != nil, err
		}},
	}
	for name, l := range lists {
		path := strings.TrimPrefix(l.route, "GET ")
		for body, want := range map[string]string{
			`null`:            "arrapi: decode " + path + ": the answer is null",
			`[null]`:          "arrapi: decode " + path + ": list item 0 is null",
			`[{"id":1},null]`: "arrapi: decode " + path + ": list item 1 is null",
		} {
			t.Run(name+" "+body, func(t *testing.T) {
				f := newFake().radarr(vRadarr)
				c := newClient(t, f)
				cached(t, c, f)
				f.on(l.route, ans(200, vRadarr, body))
				gotValue, err := l.read(c)
				if err == nil || err.Error() != want || gotValue {
					t.Errorf("%s = (value %v, %v), want no value and %q", name, gotValue, err, want)
				}
			})
		}
	}
	t.Run("MovieByID null", func(t *testing.T) {
		f := newFake().radarr(vRadarr)
		c := newClient(t, f)
		cached(t, c, f)
		f.on("GET /api/v3/movie/9", ans(200, vRadarr, `null`))
		m, found, err := c.MovieByID(t.Context(), 9)
		const want = "arrapi: decode /api/v3/movie/9: the answer is null"
		if err == nil || err.Error() != want || found || m.ID != 0 {
			t.Errorf("MovieByID = (%+v, %v, %v), want not found and %q", m, found, err, want)
		}
	})
}

// idleCounter counts CloseIdleConnections calls on the transport it wraps.
type idleCounter struct {
	http.RoundTripper
	closed atomic.Int32
}

func (i *idleCounter) CloseIdleConnections() { i.closed.Add(1) }

func TestCloseReleasesTheCallerTransportsIdleConnections(t *testing.T) {
	tr := &idleCounter{RoundTripper: newFake().radarr(vRadarr)}
	c, err := New(fakeBase, testKey, WithHTTPClient(&http.Client{Transport: tr}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.Close()
	c.Close()
	if n := tr.closed.Load(); n != 2 {
		t.Errorf("CloseIdleConnections ran %d times, want 2", n)
	}
}

// TestNestedNullItemIsRejected answers each nested array the Radarr engine decodes with
// [null] and requires a decode error naming the wire field (MovieResource's OpenAPI
// property names), never a zero-value item.
func TestNestedNullItemIsRejected(t *testing.T) {
	for _, n := range []struct{ body, want string }{
		{`[{"id":1,"alternateTitles":[null]}]`, "field alternateTitles: item 0 is null"},
		{`[{"id":1,"tags":[3,null]}]`, "field tags: item 1 is null"},
	} {
		t.Run(n.want, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			cached(t, c, f)
			f.on("GET /api/v3/movie", ans(200, vRadarr, n.body))
			_, err := c.Movies(t.Context())
			if want := "arrapi: decode /api/v3/movie: " + n.want; err == nil || err.Error() != want {
				t.Errorf("err = %v, want %q", err, want)
			}
		})
	}
}
