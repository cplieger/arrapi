package arrapi_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v2"
)

// TestWithLogger_receivesRetryDiagnostics pins the injected-logger contract: the
// retry narration arrapi produces while supervising a request must land in the
// caller's logger rather than unconditionally in slog.Default(). The server
// fails twice and then succeeds, so the run emits per-retry Debug records and no
// exhaustion Warn.
func TestWithLogger_receivesRetryDiagnostics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"title":"ok"}]`))
		}))

		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		s, err := arrapi.NewSonarr(srv.URL, testKey,
			arrapi.WithHTTPClient(srv.Client()),
			arrapi.WithLogger(logger),
			arrapi.WithMaxAttempts(3))
		if err != nil {
			t.Fatalf("NewSonarr: %v", err)
		}
		if _, err := s.Series(t.Context()); err != nil {
			t.Fatalf("Series: %v", err)
		}

		got := buf.String()
		if got == "" {
			t.Fatal("injected logger received nothing; retry diagnostics still go to slog.Default()")
		}
		if !strings.Contains(got, "arrapi") {
			t.Errorf("injected logger output %q does not carry the arrapi label", got)
		}
		if n := strings.Count(got, "arrapi failed, retrying"); n != 2 {
			t.Errorf("got %d retry records, want 2 (one per 503); output: %q", n, got)
		}
		if !strings.Contains(got, "arrapi succeeded after retry") {
			t.Errorf("no success-after-retry record; output: %q", got)
		}
		if strings.Contains(got, "level=WARN") {
			t.Errorf("a run that ultimately succeeded logged a WARN: %q", got)
		}
	})
}

// TestWithLogger_exhaustionWarnsOnTheInjectedLogger confirms the exhaustion
// record is routed too, not just the per-retry Debug lines.
func TestWithLogger_exhaustionWarnsOnTheInjectedLogger(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))

		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		s, err := arrapi.NewSonarr(srv.URL, testKey,
			arrapi.WithHTTPClient(srv.Client()),
			arrapi.WithLogger(logger),
			arrapi.WithMaxAttempts(2))
		if err != nil {
			t.Fatalf("NewSonarr: %v", err)
		}
		if _, err := s.Series(t.Context()); err == nil {
			t.Fatal("expected an error after exhausting attempts")
		}
		if got := buf.String(); !strings.Contains(got, "level=WARN") {
			t.Errorf("exhaustion Warn did not reach the injected logger; output: %q", got)
		}
	})
}

// TestWithLogger_nilIgnored confirms a nil logger is ignored rather than
// installed, so the slog.Default() fallback stays intact.
func TestWithLogger_nilIgnored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[]`))
		}))
		s, err := arrapi.NewSonarr(srv.URL, testKey,
			arrapi.WithHTTPClient(srv.Client()),
			arrapi.WithLogger(nil),
			arrapi.WithBaseDelay(time.Second))
		if err != nil {
			t.Fatalf("NewSonarr with a nil logger: %v", err)
		}
		if _, err := s.Series(t.Context()); err != nil {
			t.Fatalf("Series: %v", err)
		}
	})
}
