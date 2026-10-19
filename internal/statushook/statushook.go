// Package statushook lets internal/transport set the Retry-After hint of an
// *arrapi.StatusError. transport cannot set the unexported field itself, and
// defining StatusError below transport would make arrapi import transport, which
// imports arrapi. arrapi's init assigns SetRetryAfter, and it runs before any
// importer of arrapi can call it.
package statushook

import "time"

// SetRetryAfter stores d, capped to [0, httpx.RetryAfterCap], as the Retry-After
// hint of the *arrapi.StatusError in err's chain; a chain without one is left
// unchanged.
var SetRetryAfter func(err error, d time.Duration)
