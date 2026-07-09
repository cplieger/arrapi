package arrapi

import (
	"net/http"
	"testing"
	"time"
)

// TestConfiguredHTTPClient_timeoutCeiling verifies the default client's hard
// Timeout ceiling always sits above the resolved per-request timeout, so the
// per-request context deadline (not the client Timeout) fires first even when a
// caller raises WithTimeout above safetyTimeout.
func TestConfiguredHTTPClient_timeoutCeiling(t *testing.T) {
	tests := []struct {
		name           string
		perRequest     time.Duration
		wantAtLeast    time.Duration
		wantExactFloor bool
	}{
		{"default stays at floor", defaultTimeout, safetyTimeout, true},
		{"small timeout stays at floor", time.Second, safetyTimeout, true},
		{"large timeout lifts ceiling above it", 200 * time.Second, 200 * time.Second, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hc := configuredHTTPClient(nil, tc.perRequest)
			if tc.wantExactFloor {
				if hc.Timeout != safetyTimeout {
					t.Errorf("Timeout = %v, want floor %v", hc.Timeout, safetyTimeout)
				}
				return
			}
			if hc.Timeout <= tc.wantAtLeast {
				t.Errorf("Timeout = %v, want strictly greater than per-request %v", hc.Timeout, tc.wantAtLeast)
			}
		})
	}
}

// TestConfiguredHTTPClient_callerClientUnchanged confirms a caller-supplied
// client is returned as-is, its own Timeout untouched.
func TestConfiguredHTTPClient_callerClientUnchanged(t *testing.T) {
	own := &http.Client{Timeout: 5 * time.Second}
	if got := configuredHTTPClient(own, 200*time.Second); got != own {
		t.Errorf("configuredHTTPClient returned a different client than the caller-provided one")
	}
	if own.Timeout != 5*time.Second {
		t.Errorf("caller client Timeout mutated to %v", own.Timeout)
	}
}
