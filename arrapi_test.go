package arrapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v2"
)

// testKey is a low-entropy placeholder, not a real credential. Typed as an
// arrapi.APIKey because that is what the constructors take.
const testKey arrapi.APIKey = "test-key"

// recordingServer is an httptest server that records the last request's path
// and API-key header and replies with a scripted status + body.
type recordingServer struct {
	srv      *httptest.Server
	lastPath atomic.Pointer[string]
	lastKey  atomic.Pointer[string]
}

func newServer(t *testing.T, status int, body string) *recordingServer {
	t.Helper()
	rs := &recordingServer{}
	rs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path + "?" + r.URL.RawQuery
		k := r.Header.Get("X-Api-Key")
		rs.lastPath.Store(&p)
		rs.lastKey.Store(&k)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(rs.srv.Close)
	return rs
}

func fastSonarr(t *testing.T, url string, opts ...arrapi.Option) *arrapi.Sonarr {
	t.Helper()
	all := append([]arrapi.Option{arrapi.WithBaseDelay(time.Millisecond)}, opts...)
	s, err := arrapi.NewSonarr(url, testKey, all...)
	if err != nil {
		t.Fatalf("NewSonarr: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestNewClient_validation(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		key     arrapi.APIKey
		wantErr bool
	}{
		{"valid http", "http://sonarr:8989", testKey, false},
		{"valid https", "https://radarr.example.com", testKey, false},
		{"trailing slash trimmed", "http://sonarr:8989/", testKey, false},
		{"reverse-proxy subpath allowed", "https://host.example.com/sonarr", testKey, false},
		{"missing scheme", "sonarr:8989", testKey, true},
		{"malformed percent escape", "http://exa%mple.com", testKey, true},
		{"ftp scheme", "ftp://sonarr:8989", testKey, true},
		{"empty url", "", testKey, true},
		{"no host", "http:///series", testKey, true},
		{"query rejected", "http://sonarr:8989/api?x=1", testKey, true},
		{"fragment rejected", "http://sonarr:8989/#frag", testKey, true},
		{"empty key", "http://sonarr:8989", "", true},
		{"whitespace key", "http://sonarr:8989", "   ", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, sErr := arrapi.NewSonarr(tc.url, tc.key)
			_, rErr := arrapi.NewRadarr(tc.url, tc.key)
			if (sErr != nil) != tc.wantErr {
				t.Errorf("NewSonarr(%q, %q) err = %v, wantErr %v", tc.url, tc.key, sErr, tc.wantErr)
			}
			if (rErr != nil) != tc.wantErr {
				t.Errorf("NewRadarr(%q, %q) err = %v, wantErr %v", tc.url, tc.key, rErr, tc.wantErr)
			}
		})
	}
}

func TestGetSeries_success(t *testing.T) {
	body := `[{"id":1,"title":"86 EIGHTY-SIX","tvdbId":364877,"imdbId":"tt13636846","year":2021,"tags":[3],"monitored":true},
	          {"id":2,"title":"Frieren","tvdbId":424536,"year":2023,"tags":[]}]`
	rs := newServer(t, http.StatusOK, body)
	s := fastSonarr(t, rs.srv.URL)

	series, err := s.Series(t.Context())
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("got %d series, want 2", len(series))
	}
	if series[0].Title != "86 EIGHTY-SIX" || series[0].TvdbID != 364877 {
		t.Errorf("series[0] = %+v, want title/tvdbId 86 EIGHTY-SIX/364877", series[0])
	}
	if !series[0].Monitored || len(series[0].Tags) != 1 || series[0].Tags[0] != 3 {
		t.Errorf("series[0] monitored/tags wrong: %+v", series[0])
	}
	if got := deref(rs.lastPath.Load()); got != "/api/v3/series?" {
		t.Errorf("request path = %q, want /api/v3/series?", got)
	}
	if got := deref(rs.lastKey.Load()); arrapi.APIKey(got) != testKey {
		t.Errorf("api key header = %q, want %q", got, testKey)
	}
}

func TestGetEpisodes_pathIncludesSeriesAndFileFlag(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":10,"seriesId":7,"seasonNumber":1,"episodeNumber":1,"hasFile":true,
	   "episodeFile":{"id":99,"relativePath":"S01E01.mkv","releaseGroup":"CRUCiBLE","size":734003200}}]`)
	s := fastSonarr(t, rs.srv.URL)

	eps, err := s.Episodes(t.Context(), 7)
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if len(eps) != 1 || eps[0].EpisodeFile == nil {
		t.Fatalf("got %d episodes (file nil=%v), want 1 with file", len(eps), len(eps) == 0 || eps[0].EpisodeFile == nil)
	}
	if eps[0].EpisodeFile.ReleaseGroup != "CRUCiBLE" || eps[0].EpisodeFile.Size != 734003200 {
		t.Errorf("episodeFile = %+v, want CRUCiBLE/734003200", eps[0].EpisodeFile)
	}
	if got := deref(rs.lastPath.Load()); got != "/api/v3/episode?seriesId=7&includeEpisodeFile=true" {
		t.Errorf("request path = %q", got)
	}
}

func TestGetEpisodeFiles_success(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":99,"seriesId":7,"seasonNumber":1,"relativePath":"Season 01/S01E01.mkv",
	   "sceneName":"Show.S01E01.1080p","releaseGroup":"CRUCiBLE","size":734003200,
	   "mediaInfo":{"videoCodec":"x265","audioLanguages":"jpn/eng"}},
	  {"id":100,"seriesId":7,"seasonNumber":2,"relativePath":"Season 02/S02E01.mkv","releaseGroup":"LostYears","size":1073741824}]`)
	s := fastSonarr(t, rs.srv.URL)

	files, err := s.EpisodeFiles(t.Context(), 7)
	if err != nil {
		t.Fatalf("EpisodeFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	if files[0].ID != 99 || files[0].SeriesID != 7 || files[0].SeasonNumber != 1 {
		t.Errorf("files[0] identity = %+v, want id 99 series 7 season 1", files[0])
	}
	if files[0].ReleaseGroup != "CRUCiBLE" || files[0].RelativePath != "Season 01/S01E01.mkv" || files[0].Size != 734003200 {
		t.Errorf("files[0] = %+v, want CRUCiBLE/Season 01/S01E01.mkv/734003200", files[0])
	}
	if files[0].MediaInfo == nil || files[0].MediaInfo.VideoCodec != "x265" || files[0].MediaInfo.AudioLanguages != "jpn/eng" {
		t.Errorf("files[0].MediaInfo = %+v, want x265/jpn\\/eng", files[0].MediaInfo)
	}
	if files[1].SeasonNumber != 2 || files[1].MediaInfo != nil {
		t.Errorf("files[1] = %+v, want season 2 with nil MediaInfo", files[1])
	}
	if got := deref(rs.lastPath.Load()); got != "/api/v3/episodefile?seriesId=7" {
		t.Errorf("request path = %q, want /api/v3/episodefile?seriesId=7", got)
	}
}

func TestGetEpisodeFiles_empty(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[]`)
	s := fastSonarr(t, rs.srv.URL)

	files, err := s.EpisodeFiles(t.Context(), 7)
	if err != nil {
		t.Fatalf("EpisodeFiles on empty list: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("got %d files, want 0", len(files))
	}
}

func TestGetEpisodeFiles_malformedJSON(t *testing.T) {
	rs := newServer(t, http.StatusOK, `{not valid json`)
	s := fastSonarr(t, rs.srv.URL)

	if _, err := s.EpisodeFiles(t.Context(), 7); err == nil {
		t.Fatal("expected decode error on malformed JSON")
	}
}

func TestGetEpisodeFiles_httpError(t *testing.T) {
	rs := newServer(t, http.StatusNotFound, `{"message":"NotFound"}`)
	s := fastSonarr(t, rs.srv.URL)

	_, err := s.EpisodeFiles(t.Context(), 999)
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if !arrapi.IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false, want true", err)
	}
	se, ok := errors.AsType[*arrapi.StatusError](err)
	if !ok || se.Code != http.StatusNotFound {
		t.Errorf("want *StatusError code 404, got %v", err)
	}
}

func TestGetMovies_success(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":1,"title":"A Silent Voice","tmdbId":378064,"imdbId":"tt5323662","year":2016,"hasFile":true}]`)
	r, err := arrapi.NewRadarr(rs.srv.URL, testKey, arrapi.WithBaseDelay(time.Millisecond))
	if err != nil {
		t.Fatalf("NewRadarr: %v", err)
	}
	t.Cleanup(r.Close)

	movies, err := r.Movies(t.Context())
	if err != nil {
		t.Fatalf("Movies: %v", err)
	}
	if len(movies) != 1 || movies[0].TmdbID != 378064 || !movies[0].HasFile {
		t.Fatalf("movies = %+v, want one tmdb 378064 with file", movies)
	}
	if got := deref(rs.lastPath.Load()); got != "/api/v3/movie?" {
		t.Errorf("request path = %q, want /api/v3/movie?", got)
	}
}

func TestGet_notFoundIsStatusError(t *testing.T) {
	rs := newServer(t, http.StatusNotFound, "not found")
	s := fastSonarr(t, rs.srv.URL)

	_, err := s.Series(t.Context())
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if !arrapi.IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false, want true", err)
	}
	se, ok := errors.AsType[*arrapi.StatusError](err)
	if !ok || se.Code != http.StatusNotFound {
		t.Errorf("want *StatusError code 404, got %v", err)
	}
	if se.IsTransient() {
		t.Error("404 must not be transient")
	}
}

func TestGet_clientErrorNotRetried(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL, arrapi.WithMaxAttempts(3))

	if _, err := s.Series(t.Context()); err == nil {
		t.Fatal("expected error on 400")
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("400 was attempted %d times, want 1 (not retried)", n)
	}
}

func TestGet_retriesTransientThenSucceeds(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`[{"id":1,"title":"ok"}]`))
	}))
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL, arrapi.WithMaxAttempts(3))

	series, err := s.Series(t.Context())
	if err != nil {
		t.Fatalf("Series after retries: %v", err)
	}
	if len(series) != 1 || series[0].Title != "ok" {
		t.Errorf("series = %+v, want one titled ok", series)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("attempts = %d, want 3 (two 503s then success)", n)
	}
}

func TestGet_retriesExhausted(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL, arrapi.WithMaxAttempts(3))

	_, err := s.Series(t.Context())
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	se, ok := errors.AsType[*arrapi.StatusError](err)
	if !ok || se.Code != http.StatusBadGateway {
		t.Errorf("want *StatusError 502, got %v", err)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("attempts = %d, want 3", n)
	}
}

func TestWithMaxAttempts_clampedToOne(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL, arrapi.WithMaxAttempts(0))

	if _, err := s.Series(t.Context()); err == nil {
		t.Fatal("expected error")
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("attempts = %d, want 1 (0 clamps to 1)", n)
	}
}

func TestGet_malformedJSON(t *testing.T) {
	rs := newServer(t, http.StatusOK, `{not valid json`)
	s := fastSonarr(t, rs.srv.URL)

	if _, err := s.Series(t.Context()); err == nil {
		t.Fatal("expected decode error on malformed JSON")
	}
}

func TestPing(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
		want401 bool
	}{
		{"ok", http.StatusOK, false, false},
		{"unauthorized", http.StatusUnauthorized, true, true},
		{"server error", http.StatusInternalServerError, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rs := newServer(t, tc.status, `{"version":"4.0.0"}`)
			s := fastSonarr(t, rs.srv.URL)
			err := s.Ping(t.Context())
			if (err != nil) != tc.wantErr {
				t.Fatalf("Ping err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.want401 {
				se, ok := errors.AsType[*arrapi.StatusError](err)
				if !ok || se.Code != http.StatusUnauthorized {
					t.Errorf("want *StatusError 401, got %v", err)
				}
			}
			if got := deref(rs.lastPath.Load()); tc.status == http.StatusOK && got != "/api/v3/system/status?" {
				t.Errorf("ping path = %q", got)
			}
		})
	}
}

func TestPing_unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // close immediately so the address refuses connections

	s := fastSonarr(t, url)
	err := s.Ping(t.Context())
	if err == nil {
		t.Fatal("expected transport error against a closed server")
	}
	if arrapi.IsNotFound(err) {
		t.Error("transport error must not be classified as not-found")
	}
}

func TestGetSystemStatus(t *testing.T) {
	rs := newServer(t, http.StatusOK, `{"version":"4.0.14","appName":"Sonarr","instanceName":"Main"}`)
	s := fastSonarr(t, rs.srv.URL)

	st, err := s.SystemStatus(t.Context())
	if err != nil {
		t.Fatalf("SystemStatus: %v", err)
	}
	if st.Version != "4.0.14" || st.AppName != "Sonarr" {
		t.Errorf("status = %+v, want version 4.0.14 appName Sonarr", st)
	}
}

// TestWithTimeout_cancelsSlowRequest pins the per-request timeout: with no
// caller deadline, a request that outlasts WithTimeout is cancelled at exactly
// that budget. Runs in synthetic time at production-scale durations, so the
// assertion is an exact equality on the deadline rather than "an error
// eventually arrived".
func TestWithTimeout_cancelsSlowRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(200 * time.Second):
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write([]byte(`[]`))
		}))
		s, err := arrapi.NewSonarr(srv.URL, testKey,
			arrapi.WithHTTPClient(srv.Client()),
			arrapi.WithTimeout(20*time.Second),
			arrapi.WithMaxAttempts(1))
		if err != nil {
			t.Fatalf("NewSonarr: %v", err)
		}

		start := time.Now()
		_, err = s.Series(t.Context())
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("expected timeout error")
		}
		if elapsed != 20*time.Second {
			t.Errorf("request ran for %v, want exactly the 20s WithTimeout budget", elapsed)
		}
	})
}

// TestWithTimeout_doesNotOverrideCallerDeadline pins that a caller deadline is
// authoritative: arrapi must not undercut it with the shorter per-request
// timeout. In synthetic time the elapsed duration proves WHICH deadline
// governed — a request that takes exactly 80s under a 20s WithTimeout and a
// 500s caller deadline can only have run on the caller's.
//
// The exact equality is what earns the rewrite. The previous real-clock version
// asserted only that the call succeeded, which does catch a WithTimeout that
// overrides the deadline outright, but passes for any schedule that still lands
// inside the caller's budget: a spurious 200ms pre-request delay injected into
// doRetry keeps the old assertion green and fails this one (measured).
func TestWithTimeout_doesNotOverrideCallerDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(80 * time.Second)
			_, _ = w.Write([]byte(`[{"id":1,"title":"ok"}]`))
		}))
		s, err := arrapi.NewSonarr(srv.URL, testKey,
			arrapi.WithHTTPClient(srv.Client()),
			arrapi.WithTimeout(20*time.Second),
			arrapi.WithMaxAttempts(1))
		if err != nil {
			t.Fatalf("NewSonarr: %v", err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Second)
		defer cancel()

		start := time.Now()
		series, err := s.Series(ctx)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("Series with caller deadline and shorter WithTimeout: %v", err)
		}
		if len(series) != 1 || series[0].Title != "ok" {
			t.Fatalf("series = %+v, want one titled ok", series)
		}
		if elapsed != 80*time.Second {
			t.Errorf("request ran for %v, want 80s; anything near the 20s WithTimeout means it undercut the caller deadline", elapsed)
		}
	})
}

// TestContextCancellation confirms a caller deadline shorter than the handler's
// work surfaces as context.DeadlineExceeded, at exactly that deadline.
func TestContextCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(200 * time.Second):
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write([]byte(`[]`))
		}))
		s, err := arrapi.NewSonarr(srv.URL, testKey, arrapi.WithHTTPClient(srv.Client()))
		if err != nil {
			t.Fatalf("NewSonarr: %v", err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()

		start := time.Now()
		_, err = s.Series(ctx)
		elapsed := time.Since(start)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want context.DeadlineExceeded", err)
		}
		if elapsed != 20*time.Second {
			t.Errorf("request ran for %v, want exactly the 20s caller deadline", elapsed)
		}
	})
}

func TestWithHTTPClient_usesProvidedClient(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[]`)
	var used atomic.Bool
	hc := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		used.Store(true)
		return http.DefaultTransport.RoundTrip(req)
	})}
	s := fastSonarr(t, rs.srv.URL, arrapi.WithHTTPClient(hc))

	if _, err := s.Series(t.Context()); err != nil {
		t.Fatalf("Series: %v", err)
	}
	if !used.Load() {
		t.Error("WithHTTPClient client was not used for the request")
	}
}

// roundTripFunc adapts a function to http.RoundTripper for WithHTTPClient tests.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
