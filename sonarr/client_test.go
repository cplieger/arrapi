package sonarr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
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
		c, err := New("http://sonarr.test", key)
		if err == nil {
			c.Close()
			t.Errorf("New(%q) accepted the key", key)
			continue
		}
		if err.Error() != refusal {
			t.Errorf("New(%q) = %q, want %q", key, err.Error(), refusal)
		}
	}
	c, err := New("http://sonarr.test", "0123456789abcdef0123456789abcdef") // gitleaks:allow (fake key)
	if err != nil {
		t.Fatalf("New refused a provider-format key: %v", err)
	}
	c.Close()
}

func TestNewErrorNeverRendersURLOrKey(t *testing.T) {
	const secret = "pw-9f3a"
	for name, tc := range map[string]struct{ url, hidden string }{
		"a key in the query":            {"http://sonarr.test/?apikey=" + string(testKey), string(testKey)},
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
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	if _, err := c.Series(t.Context()); err != nil {
		t.Fatalf("Series: %v", err)
	}
	wantRequests(t, f, join(detectV4, []string{v4Series})...)
}

func TestInfo(t *testing.T) {
	for name, tc := range map[string]struct {
		serve    func(*fake) *fake
		want     Info
		requests []string
	}{
		"Sonarr v4 (API v3)": {
			serve: func(f *fake) *fake { return f.sonarr4(v4) },
			want:  Info{Version: v4, API: arrapi.APIv3, Features: Features{}},
		},
		"Sonarr v5 (API v5)": {
			serve: func(f *fake) *fake { return f.sonarr5(v5) },
			want: Info{Version: v5, API: arrapi.APIv5, Features: Features{
				SeriesOrders: true, SeasonTitles: true, Translations: true, AnimeIDs: true,
			}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := tc.serve(newFake())
			c := newClient(t, f)
			for range 2 {
				got, err := c.Info(t.Context())
				if err != nil {
					t.Fatalf("Info: %v", err)
				}
				assertFields(t, got, tc.want)
			}
			if n := count(f, "GET /api"); n != 2 {
				t.Errorf("two Info calls sent %d GET /api, want a fresh detection each", n)
			}
		})
	}
}

func TestInfoDetectionServesTheNextRead(t *testing.T) {
	f := newFake().sonarr5(v5)
	c := newClient(t, f)
	if _, err := c.Info(t.Context()); err != nil {
		t.Fatalf("Info: %v", err)
	}
	f.clearLog()
	if _, err := c.Series(t.Context()); err != nil {
		t.Fatalf("Series: %v", err)
	}
	wantRequests(t, f, v5Series)
}

func TestInfoReportsDeprecated(t *testing.T) {
	const v6 = "6.0.0.1"
	f := newFake().sonarr5(v6)
	f.on("GET /api", ans(200, v6, apiBody("v6", "v5")))
	f.on("GET /api/v6/system/status", ans(200, v6, statusBody("Sonarr", v6)))
	got, err := newClient(t, f).Info(t.Context())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if got.API != arrapi.APIv5 || !got.Deprecated {
		t.Errorf("Info = %+v, want Sonarr v6 (API v5) reported deprecated", got)
	}
}

func TestInfoAfterAContradictionDuringItsRunIsServerChanged(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	hold, release := releaser(t)
	atStatus := make(chan struct{})
	f.on("GET /api/v3/system/status", then(func() { close(atStatus) }, held(hold, ans(200, v4, statusBody("Sonarr", v4)))))
	done := make(chan error, 1)
	go func() { _, err := c.Info(t.Context()); done <- err }()
	<-atStatus
	f.on(v4Series, ans(404, "", ""))
	_, err := c.Series(t.Context())
	wantStatus(t, err, 404)
	release()
	if e := wantChanged(t, <-done); e.To != "" {
		t.Errorf("ServerChangedError.To = %q, want \"\" after a header-less answer", e.To)
	}
}

func TestInfoBoundedFiveSeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake().sonarr4(v4)
		never := make(chan struct{})
		f.on("GET /api", held(never, ans(200, v4, apiBody("v3"))))
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

func TestDetectConcurrentFirstCallsShareOneDetection(t *testing.T) {
	f := newFake().sonarr4(v4)
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
			if _, err := c.Series(t.Context()); err != nil {
				t.Errorf("Series: %v", err)
			}
		})
	}
	wg.Wait()
	if n := count(f, "GET /api"); n != 1 {
		t.Errorf("16 first calls sent %d GET /api, want 1", n)
	}
	if n := count(f, v4Series); n != 16 {
		t.Errorf("16 first calls sent %d series reads, want 16", n)
	}
}

func TestUpgradeMidRunSwitchesToSonarrV5APIv5(t *testing.T) {
	const upgraded = "5.0.0.1"
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	f.sonarr5(upgraded)
	f.on(v4Series, ans(200, upgraded, notAList))
	f.on(v5Series, ans(200, upgraded, v5SeriesList))
	got, err := c.Series(t.Context())
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	wantTitle(t, got, "Sonarr Five Show")
	wantRequests(t, f, join([]string{v4Series}, detectV5, []string{v5Series})...)
}

func TestRestoreToSonarr4SwitchesToSonarrV4APIv3(t *testing.T) {
	f := newFake().sonarr5(v5)
	c := newClient(t, f)
	cached(t, c, f)
	const restored = "4.0.21.1"
	f.sonarr4(restored)
	f.on("GET /api/v5/series/7", ans(404, restored, ""))
	f.on("GET /api/v3/series/7", ans(200, restored, `{"id":7,"title":"Back on Four"}`))
	s, found, err := c.SeriesByID(t.Context(), 7)
	if err != nil || !found || s.Title != "Back on Four" {
		t.Fatalf("SeriesByID = (%+v, %v, %v), want the series found on /api/v3", s, found, err)
	}
	wantRequests(t, f, join([]string{"GET /api/v5/series/7"}, detectV4, []string{"GET /api/v3/series/7"})...)
}

func TestMinorUpgradeRedetectsAndReissues(t *testing.T) {
	const upgraded = "4.0.21.1"
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	f.sonarr4(upgraded)
	f.on(v4Series, ans(200, upgraded, v4SeriesList))
	got, err := c.Series(t.Context())
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	wantTitle(t, got, "Sonarr Four Show")
	wantRequests(t, f, join([]string{v4Series}, detectV4, []string{v4Series})...)
}

func TestRepointToRadarrOnSharedRouteIsWrongApp(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	f.radarr(vRadarr)
	f.on("GET /api/v3/tag", ans(200, vRadarr, `[{"id":1,"label":"radarr-tag"}]`))
	tags, err := c.Tags(t.Context())
	wantUnsupported(t, err, arrapi.ReasonWrongApp)
	if tags != nil {
		t.Errorf("Tags = %v, want none", tags)
	}
	wantRequests(t, f, join([]string{"GET /api/v3/tag"}, detectV4)...)
	f.clearLog()
	_, err = c.Tags(t.Context())
	wantUnsupported(t, err, arrapi.ReasonWrongApp)
	wantRequests(t, f, detectV4...)
}

func TestInfoDuringCallDoesNotMaskUpgrade(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	hold, release := releaser(t)
	arrived := make(chan struct{})
	f.on(v4Series, then(func() { close(arrived) }, held(hold, ans(200, v5, notAList))))
	done := make(chan error, 1)
	var got []Series
	go func() {
		var err error
		got, err = c.Series(t.Context())
		done <- err
	}()
	<-arrived
	f.sonarr5(v5)
	f.on(v5Series, ans(200, v5, v5SeriesList))
	if info, err := c.Info(t.Context()); err != nil || info.API != arrapi.APIv5 {
		t.Fatalf("Info = (%+v, %v), want Sonarr v5 (API v5)", info, err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("Series: %v", err)
	}
	wantTitle(t, got, "Sonarr Five Show")
	wantRequests(t, f, join([]string{v4Series}, detectV5, []string{v5Series})...)
}

// missingParent404 is the header-less 404 Sonarr's exception pipeline writes for an
// unknown series id on a list path, as recorded from both servers: no
// X-Application-Version, a JSON message and a stack trace.
var missingParent404 = ans(404, "", `{"message":"Series with ID 99999999 does not exist","description":"NzbDrone.Core.Datastore.ModelNotFoundException: ..."}`)

func TestHeaderless404IsStatusAndNextCallDetects(t *testing.T) {
	for name, tc := range map[string]struct {
		route string
		call  func(*Client) error
	}{
		"a list path": {"GET /api/v3/episodefile", func(c *Client) error {
			_, err := c.EpisodeFiles(t.Context(), 99999999)
			return err
		}},
		"a by-id read": {"GET /api/v3/series/5", func(c *Client) error {
			_, found, err := c.SeriesByID(t.Context(), 5)
			if found {
				t.Error("SeriesByID found a series in a header-less 404")
			}
			return err
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			c := newClient(t, f)
			cached(t, c, f)
			f.on(tc.route, missingParent404)
			wantStatus(t, tc.call(c), 404)
			wantRequests(t, f, tc.route)
			f.clearLog()
			if _, err := c.Series(t.Context()); err != nil {
				t.Fatalf("next Series: %v", err)
			}
			wantRequests(t, f, join(detectV4, []string{v4Series})...)
			f.clearLog()
			if _, err := c.Series(t.Context()); err != nil {
				t.Fatalf("Series after that: %v", err)
			}
			wantRequests(t, f, v4Series)
		})
	}
}

func TestHeaderlessErrorAfterReplacementDetectsNext(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	f.sonarr5(v5)
	f.on("GET /api/v3/episodefile", missingParent404)
	f.on(v5Series, ans(200, v5, v5SeriesList))
	_, err := c.EpisodeFiles(t.Context(), 99999999)
	wantStatus(t, err, 404)
	got, err := c.Series(t.Context())
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	wantTitle(t, got, "Sonarr Five Show")
	wantRequests(t, f, join([]string{"GET /api/v3/episodefile"}, detectV5, []string{v5Series})...)
}

func TestSeriesByIDAbsent(t *testing.T) {
	for name, body := range map[string]string{
		"an empty body":         "",
		"a ProblemDetails body": `{"type":"https://tools.ietf.org/html/rfc9110#section-15.5.5","title":"Not Found","status":404}`,
		"a Sonarr message body": `{"message":"NotFound"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			c := newClient(t, f)
			cached(t, c, f)
			f.on("GET /api/v3/series/9", ans(404, v4, body))
			s, found, err := c.SeriesByID(t.Context(), 9)
			if err != nil || found {
				t.Fatalf("SeriesByID = (%+v, %v, %v), want absent", s, found, err)
			}
			assertFields(t, s, Series{})
			wantRequests(t, f, "GET /api/v3/series/9")
		})
	}
}

func TestHeaderless200IsRefused(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	f.on(v4Series, ans(200, "", v4SeriesList))
	got, err := c.Series(t.Context())
	wantUnsupported(t, err, arrapi.ReasonNoVersionHeader)
	if got != nil {
		t.Errorf("Series decoded a header-less answer: %+v", got)
	}
	wantRequests(t, f, v4Series)
}

func TestHeaderlessErrorsOneDetectionPerCall(t *testing.T) {
	for name, tc := range map[string]struct {
		code int
		runs []int // detection runs per call; the first call starts cached
		data []int // data requests per call
	}{
		"header-less 404s": {404, []int{0, 1, 1, 1}, []int{1, 1, 1, 1}},
		"header-less 500s": {500, []int{1, 1, 1, 1}, []int{3, 3, 3, 3}},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newFake().sonarr4(v4)
				c := newClient(t, f)
				cached(t, c, f)
				f.on(v4Series, ans(tc.code, "", ""))
				for call := range tc.runs {
					_, err := c.Series(t.Context())
					wantStatus(t, err, tc.code)
					if n := count(f, "GET /api"); n != tc.runs[call] {
						t.Errorf("call %d made %d detections, want %d", call, n, tc.runs[call])
					}
					if n := count(f, v4Series); n != tc.data[call] {
						t.Errorf("call %d sent %d data requests, want %d", call, n, tc.data[call])
					}
					f.clearLog()
				}
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
		"502 then 200":                  {[]step{ans(502, "", ""), ans(200, v4, v4SeriesList)}, 3, 0, 2},
		"502 then 502 under 2 attempts": {[]step{ans(502, "", "")}, 2, 502, 2},
		"502, a headered 503, then 200": {[]step{ans(502, "", ""), ans(503, v4, ""), ans(200, v4, v4SeriesList)}, 3, 0, 3},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newFake().sonarr4(v4)
				c := newClient(t, f, WithMaxAttempts(tc.attempts))
				cached(t, c, f)
				f.on(v4Series, tc.answers...)
				got, err := c.Series(t.Context())
				if tc.code != 0 {
					wantStatus(t, err, tc.code)
				} else {
					if err != nil {
						t.Fatalf("Series: %v", err)
					}
					wantTitle(t, got, "Sonarr Four Show")
				}
				if n := count(f, "GET /api"); n != 1 {
					t.Errorf("Series made %d detections, want 1", n)
				}
				if n := count(f, v4Series); n != tc.data {
					t.Errorf("Series sent %d data requests, want %d", n, tc.data)
				}
			})
		})
	}
}

func TestFirstCallHeaderlessFiveXXRetriesOnItsDetection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake().sonarr4(v4)
		c := newClient(t, f)
		f.on(v4Series, ans(503, "", ""), ans(200, v4, v4SeriesList))
		got, err := c.Series(t.Context())
		if err != nil {
			t.Fatalf("Series: %v", err)
		}
		wantTitle(t, got, "Sonarr Four Show")
		wantRequests(t, f, join(detectV4, []string{v4Series, v4Series})...)
		f.clearLog()
		if _, err := c.Series(t.Context()); err != nil {
			t.Fatalf("second Series: %v", err)
		}
		wantRequests(t, f, join(detectV4, []string{v4Series})...)
	})
}

func TestSelfMarkRetryAfterReplacementNeverDecoded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake().sonarr4(v4)
		c := newClient(t, f)
		replace := func() {
			f.sonarr5(v5)
			f.on(v4Series, ans(500, "", ""), ans(200, v5, notAList))
			f.on(v5Series, ans(200, v5, v5SeriesList))
		}
		f.on("GET /api/v3/system/status", then(replace, ans(200, v4, statusBody("Sonarr", v4))))
		got, err := c.Series(t.Context())
		wantChanged(t, err)
		if got != nil {
			t.Errorf("Series decoded %+v", got)
		}
		wantRequests(t, f, join(detectV4, []string{v4Series, v4Series})...)
		f.clearLog()
		got, err = c.Series(t.Context())
		if err != nil {
			t.Fatalf("next Series: %v", err)
		}
		wantTitle(t, got, "Sonarr Five Show")
		wantRequests(t, f, join(detectV5, []string{v5Series})...)
	})
}

func TestPendingHeaderlessRedirectWindow(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	f.sonarr5(v5)
	hold, release := releaser(t)
	atHop := make(chan struct{})
	f.on(v4Series, redirect("", "/hop/series"))
	f.on("GET /hop/series", then(func() { close(atHop) }, held(hold, ans(200, v5, notAList))))
	f.on("GET /api/v3/tag", ans(200, v5, notAList))
	f.on("GET /api/v5/tag", ans(200, v5, `[{"id":3,"label":"five"}]`))
	f.on(v5Series, ans(200, v5, v5SeriesList))
	done := make(chan error, 1)
	var series []Series
	go func() {
		var err error
		series, err = c.Series(t.Context())
		done <- err
	}()
	<-atHop
	tags, err := c.Tags(t.Context())
	if err != nil || len(tags) != 1 || tags[0].Label != "five" {
		t.Fatalf("Tags = (%v, %v), want the /api/v5/tag answer", tags, err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("Series: %v", err)
	}
	wantTitle(t, series, "Sonarr Five Show")
	wantRequests(t, f, join([]string{v4Series, "GET /hop/series", "GET /api/v3/tag"}, detectV5,
		[]string{"GET /api/v5/tag", v5Series})...)
}

func TestHeaderlessRedirectHopFailureDetectsNext(t *testing.T) {
	setup := func(t *testing.T, opts ...Option) (*Client, *fake) {
		f := newFake().sonarr4(v4)
		c := newClient(t, f, opts...)
		cached(t, c, f)
		f.sonarr5(v5)
		f.on(v4Series, redirect("", "/hop/series"))
		f.on("GET /hop/series", dialFails)
		f.on(v5Series, ans(200, v5, v5SeriesList))
		return c, f
	}
	t.Run("the cached call moves", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			c, f := setup(t)
			got, err := c.Series(t.Context())
			if err != nil {
				t.Fatalf("Series: %v", err)
			}
			wantTitle(t, got, "Sonarr Five Show")
			wantRequests(t, f, join([]string{v4Series, "GET /hop/series"}, detectV5, []string{v5Series})...)
		})
	})
	t.Run("one attempt returns the transport error", func(t *testing.T) {
		c, f := setup(t, WithMaxAttempts(1))
		if _, err := c.Series(t.Context()); err == nil || errors.As(err, new(*arrapi.StatusError)) {
			t.Fatalf("Series err = %v, want the hop's transport error", err)
		}
		f.clearLog()
		if _, err := c.Series(t.Context()); err != nil {
			t.Fatalf("next Series: %v", err)
		}
		wantRequests(t, f, join(detectV5, []string{v5Series})...)
	})
}

func TestRedirectAnswerEndsDetection(t *testing.T) {
	const upgraded = "5.0.0.1"
	setup := func(t *testing.T) (*Client, *fake, string) {
		f := newFake().sonarr4(v4)
		srv := httptest.NewServer(f)
		t.Cleanup(srv.Close)
		c, err := New(srv.URL, testKey, WithBaseDelay(time.Millisecond), WithLogger(slog.New(slog.DiscardHandler)))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t.Cleanup(c.Close)
		cached(t, c, f)
		f.sonarr5(upgraded)
		f.on(v5Series, ans(200, upgraded, v5SeriesList))
		return c, f, srv.URL
	}
	t.Run("another origin is refused and the next call detects", func(t *testing.T) {
		c, f, _ := setup(t)
		f.on(v4Series, redirect(upgraded, "http://other.invalid/api/v3/series"))
		if _, err := c.Series(t.Context()); err == nil {
			t.Fatal("Series followed a redirect to another origin")
		}
		wantRequests(t, f, v4Series)
		f.clearLog()
		if _, err := c.Series(t.Context()); err != nil {
			t.Fatalf("next Series: %v", err)
		}
		wantRequests(t, f, join(detectV5, []string{v5Series})...)
	})
	for name, location := range map[string]string{
		"a malformed Location": "http://[::1",
		"a same-origin target": "/hop/series",
	} {
		t.Run(name, func(t *testing.T) {
			c, f, _ := setup(t)
			f.on(v4Series, redirect(upgraded, location))
			got, err := c.Series(t.Context())
			if err != nil {
				t.Fatalf("Series: %v", err)
			}
			wantTitle(t, got, "Sonarr Five Show")
			wantRequests(t, f, join([]string{v4Series}, detectV5, []string{v5Series})...)
		})
	}
}

// idleCounter counts CloseIdleConnections calls on the transport it wraps.
type idleCounter struct {
	http.RoundTripper
	closed atomic.Int32
}

func (i *idleCounter) CloseIdleConnections() { i.closed.Add(1) }

func TestCloseReleasesTheCallerTransportsIdleConnections(t *testing.T) {
	tr := &idleCounter{RoundTripper: newFake().sonarr4(v4)}
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
