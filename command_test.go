package arrapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cplieger/arrapi"
)

// commandCapture records the method, path, content-type, and decoded JSON body
// of the last request, replying with a scripted status.
type commandCapture struct {
	srv    *httptest.Server
	method string
	path   string
	ctype  string
	body   map[string]any
}

func newCommandServer(t *testing.T, status int) *commandCapture {
	t.Helper()
	cc := &commandCapture{}
	cc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cc.method = r.Method
		cc.path = r.URL.Path
		cc.ctype = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		_ = json.Unmarshal(raw, &cc.body)
		w.WriteHeader(status)
	}))
	t.Cleanup(cc.srv.Close)
	return cc
}

func TestSonarrCommands(t *testing.T) {
	tests := []struct {
		name     string
		call     func(*arrapi.Sonarr, context.Context) error
		wantName string
	}{
		{"rescan", func(s *arrapi.Sonarr, ctx context.Context) error { return s.RescanSeries(ctx, 7) }, "RescanSeries"},
		{"refresh", func(s *arrapi.Sonarr, ctx context.Context) error { return s.RefreshSeries(ctx, 7) }, "RefreshSeries"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cc := newCommandServer(t, http.StatusCreated)
			s := fastSonarr(t, cc.srv.URL)
			if err := tc.call(s, t.Context()); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if cc.method != http.MethodPost || cc.path != "/api/v3/command" {
				t.Errorf("method/path = %s %s, want POST /api/v3/command", cc.method, cc.path)
			}
			if cc.ctype != "application/json" {
				t.Errorf("content-type = %q, want application/json", cc.ctype)
			}
			if cc.body["name"] != tc.wantName {
				t.Errorf("body name = %v, want %s", cc.body["name"], tc.wantName)
			}
			if cc.body["seriesId"] != float64(7) {
				t.Errorf("body seriesId = %v, want 7", cc.body["seriesId"])
			}
			if _, ok := cc.body["movieId"]; ok {
				t.Errorf("sonarr command must not carry movieId: %v", cc.body)
			}
		})
	}
}

func TestRadarrCommands(t *testing.T) {
	tests := []struct {
		name     string
		call     func(*arrapi.Radarr, context.Context) error
		wantName string
	}{
		{"rescan", func(r *arrapi.Radarr, ctx context.Context) error { return r.RescanMovie(ctx, 100) }, "RescanMovie"},
		{"refresh", func(r *arrapi.Radarr, ctx context.Context) error { return r.RefreshMovie(ctx, 100) }, "RefreshMovie"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cc := newCommandServer(t, http.StatusCreated)
			r, err := arrapi.NewRadarr(cc.srv.URL, testKey, arrapi.WithBaseDelay(time.Millisecond))
			if err != nil {
				t.Fatalf("NewRadarr: %v", err)
			}
			t.Cleanup(r.Close)
			if err := tc.call(r, t.Context()); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if cc.body["name"] != tc.wantName || cc.body["movieId"] != float64(100) {
				t.Errorf("body = %v, want name %s movieId 100", cc.body, tc.wantName)
			}
		})
	}
}

func TestCommand_errorStatus(t *testing.T) {
	cc := newCommandServer(t, http.StatusBadRequest)
	s := fastSonarr(t, cc.srv.URL)

	err := s.RescanSeries(t.Context(), 7)
	if err == nil {
		t.Fatal("expected error on 400 command response")
	}
	var se *arrapi.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadRequest {
		t.Errorf("want *StatusError 400, got %v", err)
	}
}
