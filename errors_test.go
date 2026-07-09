package arrapi_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/arrapi"
)

func TestStatusError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  *arrapi.StatusError
		want string
	}{
		{
			"with body", &arrapi.StatusError{Code: 404, Path: "/api/v3/series", Body: "not found"},
			"arrapi: /api/v3/series: HTTP 404: not found",
		},
		{
			"without body", &arrapi.StatusError{Code: 500, Path: "/api/v3/movie"},
			"arrapi: /api/v3/movie: HTTP 500",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusError_IsTransient(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{400, false},
		{401, false},
		{403, false},
		{404, false},
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{504, true},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("code_%d", tc.code), func(t *testing.T) {
			e := &arrapi.StatusError{Code: tc.code}
			if got := e.IsTransient(); got != tc.want {
				t.Errorf("StatusError{%d}.IsTransient() = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"404 status error", &arrapi.StatusError{Code: 404}, true},
		{"wrapped 404", fmt.Errorf("fetch: %w", &arrapi.StatusError{Code: 404}), true},
		{"500 status error", &arrapi.StatusError{Code: 500}, false},
		{"plain error", fmt.Errorf("boom"), false},
		{"nil error", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := arrapi.IsNotFound(tc.err); got != tc.want {
				t.Errorf("IsNotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestResponseTooLargeError_Error(t *testing.T) {
	e := &arrapi.ResponseTooLargeError{Path: "/api/v3/series", Limit: 64 << 20}
	want := "arrapi: /api/v3/series: response exceeds 67108864-byte limit"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestStatusError_redactsAPIKey(t *testing.T) {
	// A hostile or misconfigured arr endpoint (or a reverse proxy) may reflect
	// the request's X-Api-Key back in its error body. The captured StatusError
	// must redact the exact key so it never reaches a caller's logs.
	body := "unauthorized: key " + testKey + " is not recognized"
	rs := newServer(t, http.StatusUnauthorized, body)
	s := fastSonarr(t, rs.srv.URL, arrapi.WithMaxAttempts(1))

	_, err := s.GetSeries(t.Context())
	if err == nil {
		t.Fatal("expected error on 401")
	}
	var se *arrapi.StatusError
	if !errors.As(err, &se) {
		t.Fatalf("want *StatusError, got %v", err)
	}
	if strings.Contains(se.Body, testKey) {
		t.Errorf("StatusError.Body leaks the API key: %q", se.Body)
	}
	if !strings.Contains(se.Body, "[REDACTED]") {
		t.Errorf("StatusError.Body = %q, want it to contain [REDACTED]", se.Body)
	}
	if strings.Contains(se.Error(), testKey) {
		t.Errorf("Error() leaks the API key: %q", se.Error())
	}
	if !strings.Contains(se.Error(), "[REDACTED]") {
		t.Errorf("Error() = %q, want it to contain [REDACTED]", se.Error())
	}
}
