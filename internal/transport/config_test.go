package transport_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

func TestConfigDefaultsAndClamps(t *testing.T) {
	tests := []struct {
		name     string
		in       transport.Config
		attempts int
		delay    time.Duration
		timeout  time.Duration
	}{
		{"zero value", transport.Config{}, 3, time.Second, 120 * time.Second},
		{"zero attempts is the default", transport.Config{MaxAttempts: 0}, 3, time.Second, 120 * time.Second},
		{"negative attempts is the default", transport.Config{MaxAttempts: -1}, 3, time.Second, 120 * time.Second},
		{"one attempt kept", transport.Config{MaxAttempts: 1}, 1, time.Second, 120 * time.Second},
		{"five attempts kept", transport.Config{MaxAttempts: 5}, 5, time.Second, 120 * time.Second},
		{"negative delay is the default", transport.Config{BaseDelay: -time.Second}, 3, time.Second, 120 * time.Second},
		{"delay kept", transport.Config{BaseDelay: 250 * time.Millisecond}, 3, 250 * time.Millisecond, 120 * time.Second},
		{"negative timeout is the default", transport.Config{Timeout: -time.Second}, 3, time.Second, 120 * time.Second},
		{"timeout kept", transport.Config{Timeout: 20 * time.Second}, 3, time.Second, 20 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := transport.New("http://sonarr:8989", testKey, tc.in)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer c.Close()
			got := c.Config()
			if got.MaxAttempts != tc.attempts {
				t.Errorf("MaxAttempts = %d, want %d", got.MaxAttempts, tc.attempts)
			}
			if got.BaseDelay != tc.delay {
				t.Errorf("BaseDelay = %v, want %v", got.BaseDelay, tc.delay)
			}
			if got.Timeout != tc.timeout {
				t.Errorf("Timeout = %v, want %v", got.Timeout, tc.timeout)
			}
		})
	}
}

func TestCallerClientIsNeverMutated(t *testing.T) {
	policy := func(*http.Request, []*http.Request) error { return nil }
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, http.ErrHandlerTimeout })
	own := &http.Client{Timeout: 5 * time.Second, Transport: rt, CheckRedirect: policy}
	c, err := transport.New("http://sonarr:8989", testKey, transport.Config{HTTPClient: own})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	if own.Timeout != 5*time.Second {
		t.Errorf("caller Timeout mutated to %v", own.Timeout)
	}
	if _, ok := own.Transport.(roundTripFunc); !ok {
		t.Errorf("caller Transport replaced with %T", own.Transport)
	}
	if own.CheckRedirect == nil {
		t.Error("caller CheckRedirect cleared")
	}
	if c.Config().HTTPClient != own {
		t.Error("Config().HTTPClient is not the caller's client")
	}
}
