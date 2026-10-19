// Package transport is arrapi's HTTP core. Each of Probe, GetJSON, GetByID and Post
// sends exactly one attempt and returns one Result: Decide judges how the attempt
// ended against the Identity it was sent on, and the caller's attempt loop decides
// what happens next. Every request and response passes one observer, which keeps the
// client's State current and refuses a send once an answer has contradicted the
// detection it was drawn on.
package transport

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/httpx/v5"
)

const (
	headerAPIKey = "X-Api-Key" //nolint:gosec // G101: an HTTP header name, not a credential
	userAgent    = "arrapi (+https://github.com/cplieger/arrapi)"
)

// Client sends arrapi's requests to one server. It is safe for concurrent use; its
// only shared mutable state is the State pointer, changed by compare-and-swap.
type Client struct {
	state atomic.Pointer[State]
	data  *http.Client // probes and reads: the exact-origin policy, then the caller's CheckRedirect
	cmd   *http.Client // commands: no redirect is ever followed
	// afterCompare, set only by internal tests before the client sends, runs between
	// the observer's comparison of an answer with the State and its store or anchor.
	afterCompare func()
	key          arrapi.APIKey
	base         string // the configured base URL without a trailing slash
	basePath     string // its escaped path without a trailing slash
	origin       string // scheme://host[:port] of base, presented
	cfg          Config
}

// New validates baseURL (absolute http or https, a host, no query or fragment; a path
// and user info are allowed) and key (not empty or whitespace; only letters, digits,
// '.', '_' and '-'), and sends nothing. A validation error is one fixed sentence that
// renders neither the URL nor the key.
func New(baseURL string, key arrapi.APIKey, cfg Config) (*Client, error) {
	u, err := validate(baseURL, key)
	if err != nil {
		return nil, err
	}
	c := &Client{
		key:      key,
		base:     strings.TrimRight(baseURL, "/"),
		basePath: strings.TrimRight(u.EscapedPath(), "/"),
		cfg:      cfg.resolved(),
	}
	c.origin = c.Present(originOf(u), maxOriginBytes)
	c.state.Store(&State{})

	base := c.cfg.HTTPClient
	if base == nil {
		base = &http.Client{Transport: pooledTransport()}
	}
	next := base.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	obs := &observer{next: next, c: c}
	data, cmd := *base, *base
	data.Transport, cmd.Transport = obs, obs
	data.CheckRedirect = readRedirectPolicy(base.CheckRedirect)
	// Commands follow no redirect: net/http rewrites a POST answered 301, 302 or 303
	// into a GET (net/http/client.go redirectBehavior), whose 2xx would read as the
	// command's success.
	cmd.CheckRedirect = httpx.RefuseAllRedirects
	c.data, c.cmd = &data, &cmd
	return c, nil
}

// pooledTransport is sized for a single server. The client it serves sets no
// http.Client.Timeout: each attempt is bounded by its own context.
func pooledTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
	}
}

// BaseURL returns the configured base URL without a trailing slash.
func (c *Client) BaseURL() string { return c.base }

// Config returns the configuration with its defaults and clamps applied.
func (c *Client) Config() Config { return c.cfg }

// Close releases idle connections of the transport arrapi sends through, the
// caller's included; it is idempotent.
func (c *Client) Close() { c.data.CloseIdleConnections() }
