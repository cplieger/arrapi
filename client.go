package arrapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cplieger/httpx/v2"
	"golang.org/x/sync/singleflight"
)

const (
	// apiPrefix is the versioned path prefix shared by Sonarr and Radarr v3.
	apiPrefix = "/api/v3"
	// headerAPIKey is the header both services authenticate with.
	headerAPIKey = "X-Api-Key" //nolint:gosec // G101 false positive: HTTP header name, not a credential

	defaultMaxAttempts = 3
	defaultBaseDelay   = time.Second
	// defaultTimeout bounds a single request when the caller's context has no
	// deadline; it is generous enough for a full library JSON response.
	defaultTimeout = 120 * time.Second
	// safetyTimeout is the hard ceiling on the underlying http.Client. It sits
	// above defaultTimeout so the per-request context deadline fires first in
	// normal operation, but catches any path that forgets to set one.
	safetyTimeout = 150 * time.Second
	// pingTimeout bounds a connectivity check so config validation fails fast.
	pingTimeout = 5 * time.Second

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
	sfGroup     singleflight.Group
	httpClient  *http.Client
	baseURL     string
	apiKey      string
	baseDelay   time.Duration
	timeout     time.Duration
	maxAttempts int
}

// newClient validates the connection parameters, applies the options, and
// returns a ready client. baseURL must be an absolute http(s) URL and apiKey
// must be non-empty.
func newClient(baseURL, apiKey string, opts ...Option) (*client, error) {
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("arrapi: baseURL must start with http:// or https://, got %q", baseURL)
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
		hc = &http.Client{Timeout: safetyTimeout, Transport: newTransport()}
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

// get performs an authenticated GET and returns the response on a 2xx status;
// the caller must close the response body. A non-2xx status is returned as a
// *StatusError (its body drained and closed here). If the context carries no
// deadline, the client's per-request timeout is applied.
func (c *client) get(ctx context.Context, path string) (*http.Response, error) {
	if _, ok := ctx.Deadline(); !ok && c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("arrapi: build request %s: %w", path, err)
	}
	req.Header.Set(headerAPIKey, c.apiKey)

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
	return doSingleflight(ctx, c, "system/status", func(fctx context.Context) (SystemStatus, error) {
		return fetchOne[SystemStatus](fctx, c, apiPrefix+"/system/status")
	})
}

// Close releases idle connections held by the client's HTTP transport. It is
// safe to call more than once.
func (c *client) Close() { c.httpClient.CloseIdleConnections() }

// doSingleflight coalesces concurrent calls that share a key so only one HTTP
// request is in flight per key; late callers receive the shared result. The
// inner function runs on a cancellation-decoupled context so one caller's
// cancellation does not abort the flight for the others; each caller still
// observes its own context via the select. A coalesced read's result may be
// shared across callers, so treat a returned slice as read-only.
func doSingleflight[T any](ctx context.Context, c *client, key string, fn func(context.Context) (T, error)) (T, error) {
	ch := c.sfGroup.DoChan(key, func() (any, error) {
		return fn(context.WithoutCancel(ctx))
	})
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			var zero T
			return zero, res.Err
		}
		v, ok := res.Val.(T)
		if !ok {
			var zero T
			return zero, fmt.Errorf("arrapi: singleflight result type mismatch for %q", key)
		}
		return v, nil
	}
}

// fetchAll performs an authenticated GET and decodes the JSON array response,
// retrying transient failures (429, 5xx, transient transport errors) with
// jittered exponential backoff. GETs are idempotent, so retry is safe.
func fetchAll[T any](ctx context.Context, c *client, path string) ([]T, error) {
	return httpx.RetryWithBackoff(ctx, c.maxAttempts, c.baseDelay, "arrapi "+path,
		func(ctx context.Context) ([]T, error) {
			resp, err := c.get(ctx, path) //nolint:bodyclose // closed by decodeSlice
			if err != nil {
				return nil, err
			}
			return decodeSlice[T](resp, path)
		})
}

// fetchOne performs an authenticated GET and decodes a single JSON object,
// with the same retry policy as fetchAll.
func fetchOne[T any](ctx context.Context, c *client, path string) (T, error) {
	return httpx.RetryWithBackoff(ctx, c.maxAttempts, c.baseDelay, "arrapi "+path,
		func(ctx context.Context) (T, error) {
			var zero T
			resp, err := c.get(ctx, path) //nolint:bodyclose // closed by decodeObject
			if err != nil {
				return zero, err
			}
			return decodeObject[T](resp, path)
		})
}
