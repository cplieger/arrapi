package transport_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

func TestErrorClass(t *testing.T) {
	status := &arrapi.StatusError{Code: 503, Path: "/api/v3/series", Message: "server text"}
	changed := &arrapi.ServerChangedError{Status: status, From: "4.0.1", To: "5.0.0.1"}
	tests := map[string]struct {
		err  error
		want string
	}{
		"nil":                         {nil, ""},
		"a server change over status": {changed, "server_changed"},
		"a status":                    {status, "status"},
		"a refused grab":              {&arrapi.GrabError{Err: status, Reason: arrapi.GrabNotCached}, "status"},
		"an answer over its cap":      {&arrapi.ResponseTooLargeError{Path: "/api/v3/series", Limit: 1}, "too_large"},
		"a decode failure":            {&arrapi.DecodeError{Path: "/api/v3/series"}, "decode"},
		"any other error":             {errors.New("dial tcp: connection refused"), "transport"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := transport.ErrorClass(tc.err); got != tc.want {
				t.Errorf("ErrorClass = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLogError(t *testing.T) {
	err := &arrapi.StatusError{Code: 503, Path: "/api/v3/series", Message: "server text"}
	if got := transport.LogError(err, false); got != "status" {
		t.Errorf("LogError(err, false) = %v, want the class", got)
	}
	got, ok := transport.LogError(err, true).(error)
	if !ok || !strings.Contains(got.Error(), "server text") {
		t.Errorf("LogError(err, true) = %v, want the error quoting its message", got)
	}
}

func TestStatusCode(t *testing.T) {
	status := &arrapi.StatusError{Code: 429}
	if got := transport.StatusCode(&arrapi.ServerChangedError{Status: status}); got != 429 {
		t.Errorf("StatusCode(a wrapped status) = %d, want 429", got)
	}
	if got := transport.StatusCode(errors.New("x")); got != 0 {
		t.Errorf("StatusCode(no status) = %d, want 0", got)
	}
}
