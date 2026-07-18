package arrapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cplieger/httpx/v3"
)

const (
	// apiPrefix is the versioned path prefix shared by Sonarr and Radarr v3.
	apiPrefix = "/api/v3"
	// headerAPIKey is the header both services authenticate with.
	headerAPIKey = "X-Api-Key" //nolint:gosec // G101 false positive: HTTP header name, not a credential
	// userAgent identifies this client to the arr instance.
	userAgent = "arrapi (+https://github.com/cplieger/arrapi)"

	defaultMaxAttempts = 3
	defaultBaseDelay   = time.Second
	// defaultTimeout bounds a single request (including the body decode) when
	// the caller's context has no deadline; generous enough for a full library.
	defaultTimeout = 120 * time.Second
	// pingTimeout bounds a connectivity check so config validation fails fast.
	pingTimeout = 5 * time.Second
	// maxRedirects caps redirect hops (matching net/http's default).
	maxRedirects = 10

	// maxListBytes caps a bulk list response (series/movies) before decoding.
	maxListBytes = 64 << 20
	// maxObjectBytes caps a single-object response (system status) before decoding.
	maxObjectBytes = 1 << 20
	// maxErrorBodyBytes caps how much of a non-2xx body is captured for the error.
	maxErrorBodyBytes = 64 << 10
)

// client is the shared Sonarr/Radarr HTTP core. It is embedded by the exported
// Sonarr and Radarr types, which add the resource-specific methods; the
// endpoints common to both services (GetTags, GetSystemStatus, Ping, Close) are
// promoted from here.
type client struct {
	httpClient  *http.Client
	baseURL     string
	apiKey      string
	baseDelay   time.Duration
	timeout     time.Duration
	maxAttempts int
}

// newClient validates the connection parameters, applies the options, and
// returns a ready client. baseURL must be an absolute http(s) URL with a host
// and no query or fragment (a path is allowed, for reverse-proxy sub-paths);
// apiKey must be non-empty.
func newClient(baseURL, apiKey string, opts ...Option) (*client, error) {
	if err := validateClientParams(baseURL, apiKey); err != nil {
		return nil, err
	}
	cfg := resolveConfig(opts)
	return &client{
		httpClient:  configuredHTTPClient(cfg.httpClient),
		baseURL:     strings.TrimRight(baseURL, "/"),
		apiKey:      apiKey,
		baseDelay:   cfg.baseDelay,
		timeout:     cfg.timeout,
		maxAttempts: cfg.maxAttempts,
	}, nil
}

// validateClientParams enforces the connection-parameter contract: baseURL must
// be an absolute http(s) URL with a host and no query or fragment (a path is
// allowed for reverse-proxy sub-paths), and apiKey must be non-empty.
func validateClientParams(baseURL, apiKey string) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("arrapi: invalid baseURL %q: %w", baseURL, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("arrapi: baseURL must be an absolute http(s) URL with a host and no query or fragment, got %q", baseURL)
	}
	if strings.TrimSpace(apiKey) == "" {
		return errors.New("arrapi: apiKey must not be empty")
	}
	return nil
}

// resolveConfig applies the options over the defaults and clamps them to their
// valid ranges (maxAttempts >= 1; positive baseDelay and timeout).
func resolveConfig(opts []Option) config {
	cfg := config{
		maxAttempts: defaultMaxAttempts,
		baseDelay:   defaultBaseDelay,
		timeout:     defaultTimeout,
	}
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	if cfg.maxAttempts < 1 {
		cfg.maxAttempts = 1
	}
	if cfg.baseDelay <= 0 {
		cfg.baseDelay = defaultBaseDelay
	}
	if cfg.timeout <= 0 {
		cfg.timeout = defaultTimeout
	}
	return cfg
}

// configuredHTTPClient returns the caller-provided client unchanged, or a
// default client whose redirect policy follows only same-host redirects (up to
// maxRedirects hops) and refuses a cross-host hop or an https->http downgrade,
// so the X-Api-Key is never forwarded to another origin or onto a cleartext
// hop. A same-host http->https upgrade is followed (a reverse proxy that
// force-redirects to TLS is a common, safe setup). httpx.RedirectPolicyFunc
// with httpx.WithSameHost is the shared implementation of that policy.
//
// The default client sets no http.Client.Timeout: every request is bounded by
// its context (a caller-supplied deadline, or the per-request timeout applied
// by requestContext when the caller has none), which is the single bounding
// choke point. A static client-level ceiling would only undercut a caller
// deadline larger than it, so it is deliberately omitted.
func configuredHTTPClient(hc *http.Client) *http.Client {
	if hc != nil {
		return hc
	}
	return &http.Client{
		Transport:     newTransport(),
		CheckRedirect: httpx.RedirectPolicyFunc(httpx.WithSameHost(), httpx.WithMaxHops(maxRedirects)),
	}
}

// newTransport returns a pooled transport sized for a single arr instance.
func newTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
	}
}

// setStandardHeaders sets the authentication and identification headers that
// every arrapi request carries.
func (c *client) setStandardHeaders(req *http.Request) {
	req.Header.Set(headerAPIKey, c.apiKey)
	req.Header.Set("User-Agent", userAgent)
}

// get performs an authenticated GET and returns the response on a 200 status;
// the caller must close the response body. A non-200 status is returned as a
// *StatusError (its body drained and closed here). The context bounds the whole
// request; the caller must not cancel it before the body is read.
func (c *client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("arrapi: build request %s: %w", path, err)
	}
	c.setStandardHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("arrapi: request %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, path, c.apiKey)
	}
	return resp, nil
}

// Ping verifies connectivity and credentials against the instance's
// system-status endpoint. It returns nil on success, a *StatusError (a 401 for
// an invalid API key), or a transport error. A short timeout bounds it so
// config validation fails fast; it does not retry.
func (c *client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	resp, err := c.get(ctx, apiPrefix+"/system/status") //nolint:bodyclose // drained and closed via httpx.DrainClose below
	if err != nil {
		return err
	}
	httpx.DrainClose(resp.Body)
	return nil
}

// GetSystemStatus returns the instance's system status (version, app name).
func (c *client) GetSystemStatus(ctx context.Context) (SystemStatus, error) {
	return fetchOne[SystemStatus](ctx, c, apiPrefix+"/system/status")
}

// Close releases idle connections held by the client's HTTP transport. It is
// safe to call more than once.
func (c *client) Close() { c.httpClient.CloseIdleConnections() }

// requestContext derives a context bounded by the client's per-request timeout
// when the caller's context carries no deadline (httpx owns the rule). The
// returned cancel must be called only after the response body has been fully
// read, so the deadline spans both the request and its decode.
func (c *client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return httpx.ContextWithDefaultTimeout(ctx, c.timeout)
}

// doRetry calls fn up to c.maxAttempts times via httpx.Do, retrying transient
// failures (429, any 5xx, transient transport errors) with jittered
// exponential backoff, or the server's capped Retry-After hint when a
// *StatusError carries one — *StatusError implements httpx.RetryAfterHint,
// which Do honors. Each attempt runs under a per-attempt context (spanning
// the whole request and its decode, so the deadline is never cancelled
// mid-body); GETs are idempotent, so retry is safe.
func doRetry[T any](ctx context.Context, c *client, fn func(context.Context) (T, error)) (T, error) {
	return httpx.Do(ctx,
		func(rctx context.Context) (T, error) {
			rctx, cancel := c.requestContext(rctx)
			defer cancel()
			return fn(rctx)
		},
		httpx.WithMaxAttempts(c.maxAttempts),
		httpx.WithBaseDelay(c.baseDelay),
		httpx.WithLabel("arrapi"))
}

// fetchAll performs an authenticated GET and decodes the JSON array response,
// with retry (see doRetry).
func fetchAll[T any](ctx context.Context, c *client, path string) ([]T, error) {
	return doRetry(ctx, c, func(ctx context.Context) ([]T, error) {
		resp, err := c.get(ctx, path) //nolint:bodyclose // closed by decodeSlice
		if err != nil {
			return nil, err
		}
		return decodeSlice[T](resp, path)
	})
}

// fetchOne performs an authenticated GET and decodes a single JSON object, with
// the same retry policy as fetchAll.
func fetchOne[T any](ctx context.Context, c *client, path string) (T, error) {
	return doRetry(ctx, c, func(ctx context.Context) (T, error) {
		var zero T
		resp, err := c.get(ctx, path) //nolint:bodyclose // closed by decodeObject
		if err != nil {
			return zero, err
		}
		return decodeObject[T](resp, path)
	})
}

// fetchPage performs an authenticated GET and decodes a paged-collection JSON object
// bounded by the list cap, with the same retry policy as fetchAll.
func fetchPage[T any](ctx context.Context, c *client, path string) (T, error) {
	return doRetry(ctx, c, func(ctx context.Context) (T, error) {
		var zero T
		resp, err := c.get(ctx, path) //nolint:bodyclose // closed by decodePage
		if err != nil {
			return zero, err
		}
		return decodePage[T](resp, path)
	})
}
