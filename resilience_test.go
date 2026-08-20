package arrapi_test

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v2"
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

	series, err := s.GetSeries(t.Context()) // no caller deadline -> per-request timeout applies
	if err != nil {
		t.Fatalf("GetSeries on a large streamed body: %v", err)
	}
	if len(series) != n {
		t.Errorf("got %d series, want %d", len(series), n)
	}
}

// TestCrossHostRedirect_doesNotForwardAPIKey guards the same-host redirect
// policy: a redirect to another host must be refused so X-Api-Key never leaks.
// The origin binds 127.0.0.1; the redirect targets the SAME server under the
// distinct hostname "localhost", so the host-based policy sees a cross-host hop
// (127.0.0.1 != localhost) and refuses it — without the refusal the key would
// reach the handler.
func TestCrossHostRedirect_doesNotForwardAPIKey(t *testing.T) {
	var leaked atomic.Pointer[string]
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := r.Header.Get("X-Api-Key")
		leaked.Store(&k)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(other.Close)
	crossHost := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	if crossHost == other.URL {
		t.Skipf("test server URL %q is not 127.0.0.1-based; cannot synthesize a cross-host target", other.URL)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, crossHost+r.URL.Path, http.StatusFound)
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
	if _, ok := errors.AsType[*arrapi.ResponseTooLargeError](err); !ok {
		t.Errorf("want *ResponseTooLargeError, got %v", err)
	}
}

// TestRetryAfter_honored confirms a 429's Retry-After hint drives the wait
// instead of the jittered backoff. The base delay is set far above the hint, so
// the elapsed time distinguishes the two: honoring the hint waits exactly the
// 1s the server asked for, ignoring it would wait the 10s base delay.
//
// It runs in a synctest bubble over httptest's in-memory network, so the wait
// happens in synthetic time at the PRODUCTION delays rather than at a
// millisecond override. That makes the assertion an exact equality — the
// Retry-After path applies the hint verbatim, with no jitter — where a
// real-clock test could only bound it loosely, and it costs no wall time.
func TestRetryAfter_honored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"title":"ok"}]`))
		}))
		s, err := arrapi.NewSonarr(srv.URL, testKey,
			arrapi.WithHTTPClient(srv.Client()),
			arrapi.WithBaseDelay(10*time.Second),
			arrapi.WithMaxAttempts(2))
		if err != nil {
			t.Fatalf("NewSonarr: %v", err)
		}

		start := time.Now()
		series, err := s.GetSeries(t.Context())
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("GetSeries: %v", err)
		}
		if len(series) != 1 {
			t.Fatalf("got %d series, want 1", len(series))
		}
		if elapsed != time.Second {
			t.Errorf("retry waited %v, want exactly 1s (the Retry-After hint, not the 10s base delay)", elapsed)
		}
		if n := calls.Load(); n != 2 {
			t.Errorf("attempts = %d, want 2 (the 429 then the success)", n)
		}
	})
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
	se, ok := errors.AsType[*arrapi.StatusError](err)
	if !ok {
		t.Fatalf("GetSeries error = %v, want *StatusError", err)
	}
	if se.RetryAfter != 30*time.Second {
		t.Errorf("StatusError.RetryAfter = %v, want 30s", se.RetryAfter)
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

// TestStatusError_errorBodyIsCapped confirms a non-2xx error body is capped at
// maxErrorBodyBytes before being stored, so a regression to an unbounded read
// cannot consume or expose an attacker-controlled error body. A capture cut by
// the cap ends in the "..." truncation marker (outside the cap), so the total
// is cap+3 bytes.
func TestStatusError_errorBodyIsCapped(t *testing.T) {
	body := strings.Repeat("x", 64<<10) + "after-limit"
	rs := newServer(t, http.StatusInternalServerError, body)
	s := fastSonarr(t, rs.srv.URL, arrapi.WithMaxAttempts(1))

	_, err := s.GetSeries(t.Context())
	se, ok := errors.AsType[*arrapi.StatusError](err)
	if !ok {
		t.Fatalf("GetSeries error = %v, want *StatusError", err)
	}
	if len(se.Body) != 64<<10+len("...") {
		t.Errorf("StatusError.Body length = %d, want %d (cap + truncation marker)", len(se.Body), 64<<10+len("..."))
	}
	if !strings.HasSuffix(se.Body, "...") {
		t.Error("truncated StatusError.Body does not end in the \"...\" marker")
	}
	if strings.Contains(se.Body, "after-limit") {
		t.Errorf("StatusError.Body included bytes beyond the 64 KiB cap")
	}
}
