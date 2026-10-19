package radarr

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

// Option configures a Client.
type Option func(*options)

type options struct{ cfg transport.Config }

// WithHTTPClient sends every request, detection probes included, through copies of c whose
// Transport wraps c's, so arrapi sees every answer; nil is ignored. The caller owns c's
// transport. A read follows a redirect only where the default client would, to the exact
// origin under httpx's default hop cap, and then only if c.CheckRedirect, when set, allows
// it too; a command follows none. c's Transport must not follow redirects itself (the
// http.RoundTripper contract): one that does hides a redirect from arrapi and can turn a
// command into a GET that reads as success. The context deadline and WithTimeout still
// bound each attempt.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) {
		if c != nil {
			o.cfg.HTTPClient = c
		}
	}
}

// WithMaxAttempts bounds the attempts one read sends, wherever they go, and the attempts
// each detection probe sends; n < 1 means the default 3. A command is sent once.
func WithMaxAttempts(n int) Option { return func(o *options) { o.cfg.MaxAttempts = n } }

// WithBaseDelay sets the retry backoff base; d <= 0 means the default 1s.
func WithBaseDelay(d time.Duration) Option { return func(o *options) { o.cfg.BaseDelay = d } }

// WithTimeout bounds each attempt whose context has no deadline; d <= 0 means the
// default 120s.
func WithTimeout(d time.Duration) Option { return func(o *options) { o.cfg.Timeout = d } }

// WithLogger sets the logger of the retry and detection records; nil is ignored, and
// the default is slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.cfg.Logger = l
		}
	}
}
