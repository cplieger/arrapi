package arrapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/arrapi"
)

const testKey = "test-key" // low-entropy placeholder; not a real credential

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
		key     string
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

	series, err := s.GetSeries(t.Context())
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
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
	if got := deref(rs.lastKey.Load()); got != testKey {
		t.Errorf("api key header = %q, want %q", got, testKey)
	}
}

func TestGetEpisodes_pathIncludesSeriesAndFileFlag(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":10,"seriesId":7,"seasonNumber":1,"episodeNumber":1,"hasFile":true,
	   "episodeFile":{"id":99,"relativePath":"S01E01.mkv","releaseGroup":"CRUCiBLE","size":734003200}}]`)
	s := fastSonarr(t, rs.srv.URL)

	eps, err := s.GetEpisodes(t.Context(), 7)
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
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

func TestGetMovies_success(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":1,"title":"A Silent Voice","tmdbId":378064,"imdbId":"tt5323662","year":2016,"hasFile":true}]`)
	r, err := arrapi.NewRadarr(rs.srv.URL, testKey, arrapi.WithBaseDelay(time.Millisecond))
	if err != nil {
		t.Fatalf("NewRadarr: %v", err)
	}
	t.Cleanup(r.Close)

	movies, err := r.GetMovies(t.Context())
	if err != nil {
		t.Fatalf("GetMovies: %v", err)
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

	_, err := s.GetSeries(t.Context())
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if !arrapi.IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false, want true", err)
	}
	var se *arrapi.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusNotFound {
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

	if _, err := s.GetSeries(t.Context()); err == nil {
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

	series, err := s.GetSeries(t.Context())
	if err != nil {
		t.Fatalf("GetSeries after retries: %v", err)
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

	_, err := s.GetSeries(t.Context())
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	var se *arrapi.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadGateway {
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

	if _, err := s.GetSeries(t.Context()); err == nil {
		t.Fatal("expected error")
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("attempts = %d, want 1 (0 clamps to 1)", n)
	}
}

func TestGet_malformedJSON(t *testing.T) {
	rs := newServer(t, http.StatusOK, `{not valid json`)
	s := fastSonarr(t, rs.srv.URL)

	if _, err := s.GetSeries(t.Context()); err == nil {
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
				var se *arrapi.StatusError
				if !errors.As(err, &se) || se.Code != http.StatusUnauthorized {
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

	st, err := s.GetSystemStatus(t.Context())
	if err != nil {
		t.Fatalf("GetSystemStatus: %v", err)
	}
	if st.Version != "4.0.14" || st.AppName != "Sonarr" {
		t.Errorf("status = %+v, want version 4.0.14 appName Sonarr", st)
	}
}

func TestWithTimeout_cancelsSlowRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL, arrapi.WithTimeout(20*time.Millisecond), arrapi.WithMaxAttempts(1))

	if _, err := s.GetSeries(t.Context()); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestWithTimeout_doesNotOverrideCallerDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		_, _ = w.Write([]byte(`[{"id":1,"title":"ok"}]`))
	}))
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL, arrapi.WithTimeout(20*time.Millisecond), arrapi.WithMaxAttempts(1))

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	series, err := s.GetSeries(ctx)
	if err != nil {
		t.Fatalf("GetSeries with caller deadline and shorter WithTimeout: %v", err)
	}
	if len(series) != 1 || series[0].Title != "ok" {
		t.Fatalf("series = %+v, want one titled ok", series)
	}
}

func TestContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.GetSeries(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestWithHTTPClient_usesProvidedClient(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[]`)
	var used atomic.Bool
	hc := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		used.Store(true)
		return http.DefaultTransport.RoundTrip(req)
	})}
	s := fastSonarr(t, rs.srv.URL, arrapi.WithHTTPClient(hc))

	if _, err := s.GetSeries(t.Context()); err != nil {
		t.Fatalf("GetSeries: %v", err)
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
