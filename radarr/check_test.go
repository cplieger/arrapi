package radarr

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

func checkVia(t *testing.T, f *fake, base string, key arrapi.APIKey) error {
	t.Helper()
	_, err := Check(t.Context(), base, key, WithHTTPClient(&http.Client{Transport: f}), WithLogger(slog.New(slog.DiscardHandler)))
	return err
}

func wantCheck(t *testing.T, err error, reason arrapi.CheckReason) {
	t.Helper()
	ce, ok := errors.AsType[*arrapi.CheckError](err)
	if !ok || ce.Reason != reason {
		t.Fatalf("err = %v, want an *arrapi.CheckError with reason %d", err, reason)
	}
}

func TestCheckReasons(t *testing.T) {
	t.Run("api answers 401", func(t *testing.T) {
		f := newFake().radarr(vRadarr)
		f.on("GET /api", ans(401, "", ""))
		err := checkVia(t, f, fakeBase, testKey)
		wantCheck(t, err, arrapi.CheckKey)
		wantStatus(t, err, 401)
	})
	t.Run("a key outside the alphabet", func(t *testing.T) {
		f := newFake().radarr(vRadarr)
		err := checkVia(t, f, fakeBase, "a+b")
		wantCheck(t, err, arrapi.CheckKey)
		if got := f.requests(); len(got) != 0 {
			t.Errorf("requests = %q, want none", got)
		}
	})
	t.Run("every path 404", func(t *testing.T) {
		f := newFake().radarr(vRadarr)
		f.on("GET /api", ans(404, "", ""))
		f.on("GET /api/v3/system/status", ans(404, "", ""))
		err := checkVia(t, f, fakeBase, testKey)
		wantCheck(t, err, arrapi.CheckBasePath)
		wantStatus(t, err, 404)
	})
	t.Run("a Sonarr answers", func(t *testing.T) {
		err := checkVia(t, newFake().sonarr4("4.0.20.3014"), fakeBase, testKey)
		wantCheck(t, err, arrapi.CheckWrongApp)
		wantUnsupported(t, err, arrapi.ReasonWrongApp)
	})
	t.Run("a 502", func(t *testing.T) {
		f := newFake().radarr(vRadarr)
		f.on("GET /api/v3/system/status", ans(502, "", ""))
		err := checkVia(t, f, fakeBase, testKey)
		wantCheck(t, err, arrapi.CheckServer)
		wantStatus(t, err, 502)
	})
}

func TestCheckReturnsInfo(t *testing.T) {
	c, p := replayRadarr(t)
	want, err := c.Info(t.Context())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	got, err := Check(t.Context(), c.t.BaseURL(), testKey, WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got != want || got.Version == "" {
		t.Errorf("Check = %+v, want Info's %+v", got, want)
	}
	if n := len(p.Requests()); n != 2*len(detectRadarr) {
		t.Errorf("requests = %q, want two detections", p.Requests())
	}
}

func TestCheckEndedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Check(ctx, "ftp://x", testKey)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestCheckLeavesTheCallerPool(t *testing.T) {
	tr := &idleCounter{RoundTripper: newFake().radarr(vRadarr)}
	if _, err := Check(t.Context(), fakeBase, testKey, WithHTTPClient(&http.Client{Transport: tr})); err != nil {
		t.Fatal(err)
	}
	if n := tr.closed.Load(); n != 0 {
		t.Errorf("CloseIdleConnections ran %d times on the caller's transport, want 0", n)
	}
}
