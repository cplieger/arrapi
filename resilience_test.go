package arrapi_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/arrapi"
)

// TestGetSeries_largeStreamedBody guards the context-cancel fix: a large,
// slowly-streamed list must decode fully. The per-request timeout has to span
// the body read, not be cancelled when the request helper returns.
func TestGetSeries_largeStreamedBody(t *testing.T) {
	const n = 100000
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fl, _ := w.(http.Flusher)
		bw := bufio.NewWriter(w)
		_, _ = bw.WriteString("[")
		for i := range n {
			if i > 0 {
				_, _ = bw.WriteString(",")
			}
			_, _ = fmt.Fprintf(bw, `{"id":%d,"title":"%s"}`, i, strings.Repeat("x", 60))
			if i%2000 == 0 {
				_ = bw.Flush()
				if fl != nil {
					fl.Flush()
				}
			}
		}
		_, _ = bw.WriteString("]")
		_ = bw.Flush()
		if fl != nil {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL)

	series, err := s.GetSeries(context.Background()) // no caller deadline -> per-request timeout applies
	if err != nil {
		t.Fatalf("GetSeries on a large streamed body: %v", err)
	}
	if len(series) != n {
		t.Errorf("got %d series, want %d", len(series), n)
	}
}

// TestCrossHostRedirect_doesNotForwardAPIKey guards the same-host redirect
// policy: a redirect to another host must be refused so X-Api-Key never leaks.
func TestCrossHostRedirect_doesNotForwardAPIKey(t *testing.T) {
	var leaked atomic.Pointer[string]
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := r.Header.Get("X-Api-Key")
		leaked.Store(&k)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(other.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	s := fastSonarr(t, origin.URL)
	if _, err := s.GetSeries(t.Context()); err == nil {
		t.Fatal("expected an error refusing the cross-host redirect")
	}
	if got := leaked.Load(); got != nil && *got != "" {
		t.Errorf("X-Api-Key leaked to another host: %q", *got)
	}
}

// TestSameHostRedirect_followed confirms a redirect within the same host is
// still followed.
func TestSameHostRedirect_followed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/series", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirected", http.StatusFound)
	})
	mux.HandleFunc("/redirected", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"title":"ok"}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL)

	series, err := s.GetSeries(t.Context())
	if err != nil {
		t.Fatalf("same-host redirect should be followed: %v", err)
	}
	if len(series) != 1 || series[0].Title != "ok" {
		t.Errorf("series = %+v, want one titled ok", series)
	}
}

// TestResponseTooLarge_objectRejected rejects an over-cap body rather than
// silently truncating it.
func TestResponseTooLarge_objectRejected(t *testing.T) {
	huge := strings.Repeat("x", (1<<20)+1024) // exceed maxObjectBytes (1 MiB)
	rs := newServer(t, http.StatusOK, `{"version":"4.0.0","appName":"`+huge+`"}`)
	s := fastSonarr(t, rs.srv.URL)

	_, err := s.GetSystemStatus(t.Context())
	if err == nil {
		t.Fatal("expected ResponseTooLargeError for an over-cap body")
	}
	var tooLarge *arrapi.ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Errorf("want *ResponseTooLargeError, got %v", err)
	}
}

// TestRetryAfter_honored confirms a 429's Retry-After hint drives the wait. The
// base delay is set very high, so a fast retry proves the ~1s hint was used
// instead of the 10s backoff.
func TestRetryAfter_honored(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`[{"id":1,"title":"ok"}]`))
	}))
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL, arrapi.WithBaseDelay(10*time.Second), arrapi.WithMaxAttempts(2))

	start := time.Now()
	series, err := s.GetSeries(t.Context())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("got %d series, want 1", len(series))
	}
	if elapsed > 5*time.Second {
		t.Errorf("retry waited %v; Retry-After (1s) not honored over the 10s base delay", elapsed)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("retry waited %v; expected to honor the ~1s Retry-After hint", elapsed)
	}
}

// TestStatusError_rateLimitFields checks the 429 hint is parsed onto the error
// and IsRateLimited detects it (no retry, so the error surfaces directly).
func TestStatusError_rateLimitFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL, arrapi.WithMaxAttempts(1))

	_, err := s.GetSeries(t.Context())
	if !arrapi.IsRateLimited(err) {
		t.Fatalf("IsRateLimited(%v) = false, want true", err)
	}
	var se *arrapi.StatusError
	if !errors.As(err, &se) || se.RetryAfter != 30*time.Second {
		t.Errorf("StatusError.RetryAfter = %v, want 30s (err %v)", se.RetryAfter, err)
	}
}

// TestUserAgentHeaderSet confirms the client identifies itself.
func TestUserAgentHeaderSet(t *testing.T) {
	var ua atomic.Pointer[string]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := r.Header.Get("User-Agent")
		ua.Store(&u)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL)

	if _, err := s.GetSeries(t.Context()); err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got := ua.Load(); got == nil || !strings.Contains(*got, "arrapi") {
		t.Errorf("User-Agent header did not contain arrapi: %v", got)
	}
}
