package arrapi

import (
	"net/http"
	"time"
)

// config holds resolved client configuration built from the functional options.
type config struct {
	httpClient  *http.Client
	baseDelay   time.Duration
	timeout     time.Duration
	maxAttempts int
}

// Option configures a Sonarr or Radarr client at construction time.
type Option func(*config)

// WithHTTPClient sets the underlying *http.Client. When set, the caller owns
// the client's transport, timeout, and redirect policy; arrapi's defaults are
// not applied. A nil client is ignored. Use this to share a connection pool,
// pin a custom CA (see github.com/cplieger/httpx.CATransport), or inject a test
// server client.
func WithHTTPClient(c *http.Client) Option {
	return func(cfg *config) {
		if c != nil {
			cfg.httpClient = c
		}
	}
}

// WithMaxAttempts sets the total number of HTTP attempts (including the first)
// for a transient failure. Values below 1 are clamped to 1 (try exactly once).
// Default: 3.
func WithMaxAttempts(n int) Option {
	return func(cfg *config) { cfg.maxAttempts = n }
}

// WithBaseDelay sets the base delay for the exponential backoff between retries.
// Default: 1s.
func WithBaseDelay(d time.Duration) Option {
	return func(cfg *config) { cfg.baseDelay = d }
}

// WithTimeout sets the per-request timeout applied when the caller's context
// carries no deadline of its own. Default: 120s.
func WithTimeout(d time.Duration) Option {
	return func(cfg *config) { cfg.timeout = d }
}
