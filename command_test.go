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
// of the last request, replying with a scripted status and a command resource.
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
		if status < http.StatusBadRequest {
			_, _ = w.Write([]byte(`{"id":42,"name":"cmd","status":"queued"}`))
		}
	}))
	t.Cleanup(cc.srv.Close)
	return cc
}

func TestSonarrCommands(t *testing.T) {
	tests := []struct {
		name     string
		call     func(*arrapi.Sonarr, context.Context) (arrapi.Command, error)
		wantName string
	}{
		{"rescan", func(s *arrapi.Sonarr, ctx context.Context) (arrapi.Command, error) { return s.RescanSeries(ctx, 7) }, "RescanSeries"},
		{"refresh", func(s *arrapi.Sonarr, ctx context.Context) (arrapi.Command, error) { return s.RefreshSeries(ctx, 7) }, "RefreshSeries"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cc := newCommandServer(t, http.StatusCreated)
			s := fastSonarr(t, cc.srv.URL)
			cmd, err := tc.call(s, t.Context())
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if cmd.ID != 42 || cmd.Status != "queued" {
				t.Errorf("returned command = %+v, want id 42 status queued", cmd)
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
		call     func(*arrapi.Radarr, context.Context) (arrapi.Command, error)
		wantName string
	}{
		{"rescan", func(r *arrapi.Radarr, ctx context.Context) (arrapi.Command, error) { return r.RescanMovie(ctx, 100) }, "RescanMovie"},
		{"refresh", func(r *arrapi.Radarr, ctx context.Context) (arrapi.Command, error) { return r.RefreshMovie(ctx, 100) }, "RefreshMovie"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cc := newCommandServer(t, http.StatusCreated)
			r, err := arrapi.NewRadarr(cc.srv.URL, testKey, arrapi.WithBaseDelay(time.Millisecond))
			if err != nil {
				t.Fatalf("NewRadarr: %v", err)
			}
			t.Cleanup(r.Close)
			cmd, err := tc.call(r, t.Context())
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if cmd.ID != 42 {
				t.Errorf("returned command = %+v, want id 42", cmd)
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

	_, err := s.RescanSeries(t.Context(), 7)
	if err == nil {
		t.Fatal("expected error on 400 command response")
	}
	var se *arrapi.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadRequest {
		t.Errorf("want *StatusError 400, got %v", err)
	}
}

func TestCommand_redirectStatusRejected(t *testing.T) {
	cc := newCommandServer(t, http.StatusMultipleChoices)
	s := fastSonarr(t, cc.srv.URL)

	_, err := s.RescanSeries(t.Context(), 7)
	if err == nil {
		t.Fatal("expected error on 300 command response")
	}
	var se *arrapi.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusMultipleChoices {
		t.Errorf("want *StatusError 300, got %v", err)
	}
}

func TestGetCommandByID(t *testing.T) {
	rs := newServer(t, http.StatusOK, `{"id":7,"name":"RescanSeries","status":"completed"}`)
	s := fastSonarr(t, rs.srv.URL)

	cmd, err := s.GetCommandByID(t.Context(), 7)
	if err != nil {
		t.Fatalf("GetCommandByID: %v", err)
	}
	if cmd.ID != 7 || cmd.Status != "completed" {
		t.Errorf("command = %+v, want id 7 status completed", cmd)
	}
	if got := deref(rs.lastPath.Load()); got != "/api/v3/command/7?" {
		t.Errorf("path = %q, want /api/v3/command/7?", got)
	}
}
