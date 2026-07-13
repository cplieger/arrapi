package arrapi

import (
	"net/http"
	"testing"
	"time"
)

// TestConfiguredHTTPClient_noClientTimeout verifies the default client sets no
// http.Client.Timeout, so a caller-supplied context deadline is never undercut
// by a static client-level ceiling; every request is bounded by its context via
// requestContext. The pooled transport and the same-host redirect guard are
// still set.
func TestConfiguredHTTPClient_noClientTimeout(t *testing.T) {
	hc := configuredHTTPClient(nil)
	if hc.Timeout != 0 {
		t.Errorf("default client Timeout = %v, want 0 (context governs, no static ceiling)", hc.Timeout)
	}
	if hc.Transport == nil {
		t.Error("default client Transport is nil, want the pooled transport")
	}
	if hc.CheckRedirect == nil {
		t.Error("default client CheckRedirect is nil, want the same-host redirect guard")
	}
}

// TestConfiguredHTTPClient_callerClientUnchanged confirms a caller-supplied
// client is returned as-is, its own Timeout untouched.
func TestConfiguredHTTPClient_callerClientUnchanged(t *testing.T) {
	own := &http.Client{Timeout: 5 * time.Second}
	if got := configuredHTTPClient(own); got != own {
		t.Errorf("configuredHTTPClient returned a different client than the caller-provided one")
	}
	if own.Timeout != 5*time.Second {
		t.Errorf("caller client Timeout mutated to %v", own.Timeout)
	}
}
