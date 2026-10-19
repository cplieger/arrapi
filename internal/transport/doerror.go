package transport

import (
	"errors"
	"fmt"
	"slices"

	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/runesafe/v3"
)

const maxOriginBytes = 255

// safeDoError is the only way a transport or body-read error leaves transport; the
// send check's refusal is arrapi's own error and needs none of this.
// net/http's *url.Error renders the request URL, which after a redirect is a Location
// a server chose and can carry the key; it is replaced by transport's own text, which
// leaves through leaving.
func (c *Client) safeDoError(path string, err error) error {
	cause := httpx.LogSafeError(err)
	var safe error
	if r, ok := errors.AsType[*redirectRefusedError](cause); ok {
		target, refusal := c.Present(originOf(r.target), maxOriginBytes), c.redactRefusal(r.cause)
		// The cap first: httpx judges the count before the target, so a refused hop past
		// the cap can also be an upgrade or another origin.
		switch {
		case r.tooMany:
			safe = fmt.Errorf("arrapi: %s stopped after %d redirects: %w", path, r.followed, refusal)
		case r.upgrade:
			safe = fmt.Errorf("arrapi: %s redirects to %s: change the url to %s%s: %w", path, target, target, c.basePath, refusal)
		default:
			safe = fmt.Errorf("arrapi: %s redirect to %s refused: %w", path, target, refusal)
		}
	} else {
		safe = fmt.Errorf("arrapi: request %s: %w", path, c.redactRefusal(cause))
	}
	return c.leaving(safe)
}

// redactRefusal returns err, or, when err is httpx's refusal of an origin that holds
// the key (a server can put it in a redirect target's host), a copy whose Origin is
// presented, so the chain stays typed for errors.As and renders no key. Only a direct
// cause can be replaced; a refusal deeper in a chain is left to leaving.
func (c *Client) redactRefusal(err error) error {
	refused, ok := err.(*httpx.RedirectRefusedError)
	if !ok || !c.containsKey(refused.Origin) {
		return err
	}
	redacted := *refused
	redacted.Origin = c.Present(refused.Origin, maxOriginBytes)
	return &redacted
}

// leaving is err as it leaves transport, rendering one line: a redacted copy of its
// text wrapping nothing when any error of its tree renders the key, else err behind
// oneLineError.
func (c *Client) leaving(err error) error {
	if c.treeRendersKey(err) {
		return errors.New(c.Present(err.Error(), maxMessageBytes))
	}
	return &oneLineError{err: err}
}

// oneLineError renders its error's text with every rune unsafe in a single-line sink
// a space: a transport cause can quote server bytes (crypto/x509's HostnameError joins
// a certificate's DNS names as written). errors.Is and errors.As reach the error.
type oneLineError struct{ err error }

func (e *oneLineError) Error() string { return runesafe.SanitizeSingleLine(e.err.Error()) }
func (e *oneLineError) Unwrap() error { return e.err }

// treeRendersKey walks both Unwrap forms, as errors.Is and errors.As do, so a key
// below a branch whose parent renders it not is still found.
func (c *Client) treeRendersKey(err error) bool {
	if err == nil {
		return false
	}
	if c.containsKey(err.Error()) || c.containsKey(fmt.Sprintf("%+v", err)) {
		return true
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		return slices.ContainsFunc(multi.Unwrap(), c.treeRendersKey)
	}
	return c.treeRendersKey(errors.Unwrap(err))
}
