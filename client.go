package arrapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cplieger/httpx/v2"
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
	// safetyTimeout is the hard ceiling on the underlying http.Client. It sits
	// above defaultTimeout so the per-request context deadline fires first in
	// normal operation, but catches any path that forgets to set one.
	safetyTimeout = 150 * time.Second
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
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("arrapi: invalid baseURL %q: %w", baseURL, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("arrapi: baseURL must be an absolute http(s) URL with a host and no query or fragment, got %q", baseURL)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("arrapi: apiKey must not be empty")
	}

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

	hc := cfg.httpClient
	if hc == nil {
		hc = &http.Client{
			Timeout:       safetyTimeout,
			Transport:     newTransport(),
			CheckRedirect: sameHostRedirect,
		}
	}

	return &client{
		httpClient:  hc,
		baseURL:     strings.TrimRight(baseURL, "/"),
		apiKey:      apiKey,
		baseDelay:   cfg.baseDelay,
		timeout:     cfg.timeout,
		maxAttempts: cfg.maxAttempts,
	}, nil
}

// newTransport returns a pooled transport sized for a single arr instance.
func newTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
	}
}

// sameHostRedirect refuses to follow a redirect to a different host. Go strips
// only Authorization/Cookie-class headers across a cross-host redirect, not a
// custom header, so following one would forward the X-Api-Key to another
// origin. Same-host redirects are allowed up to maxRedirects.
func sameHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("arrapi: refusing redirect to a different host %q", req.URL.Host)
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("arrapi: stopped after %d redirects", maxRedirects)
	}
	return nil
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
	req.Header.Set(headerAPIKey, c.apiKey)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("arrapi: request %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, path)
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
// when the caller's context carries no deadline. The returned cancel must be
// called only after the response body has been fully read, so the deadline
// spans both the request and its decode.
func (c *client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); !ok && c.timeout > 0 {
		return context.WithTimeout(ctx, c.timeout)
	}
	return ctx, func() {}
}

// doRetry calls fn up to c.maxAttempts times, retrying transient failures (429,
// any 5xx, transient transport errors) with jittered exponential backoff, or
// the server's Retry-After hint when a *StatusError carries one. Each attempt
// runs under a per-attempt context that spans the whole request and decode, so
// the deadline is never cancelled mid-body. GETs are idempotent, so retry is
// safe. It composes httpx's retry primitives rather than httpx.RetryWithBackoff
// so it can honor Retry-After.
func doRetry[T any](ctx context.Context, c *client, fn func(context.Context) (T, error)) (T, error) {
	maxAttempts := max(c.maxAttempts, 1)
	backoff := c.baseDelay
	var zero T
	var lastErr error
	for attempt := range maxAttempts {
		result, err := func() (T, error) {
			rctx, cancel := c.requestContext(ctx)
			defer cancel()
			return fn(rctx)
		}()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		if !httpx.IsTransient(err) {
			return zero, err
		}
		if attempt == maxAttempts-1 {
			break
		}
		wait := httpx.JitteredBackoff(backoff)
		if ra := retryAfter(err); ra > 0 {
			wait = ra
		}
		if err := httpx.SleepCtx(ctx, wait); err != nil {
			return zero, err
		}
		backoff = httpx.SafeDouble(backoff)
	}
	return zero, lastErr
}

// retryAfter returns the (capped) Retry-After hint from a *StatusError, or 0.
func retryAfter(err error) time.Duration {
	var se *StatusError
	if errors.As(err, &se) {
		return se.RetryAfter
	}
	return 0
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
