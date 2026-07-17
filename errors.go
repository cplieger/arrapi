package arrapi

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cplieger/httpx/v3"
)

// StatusError is a non-2xx HTTP response from a Sonarr or Radarr API. It
// implements the httpx.Transient interface, so the httpx retry helpers treat a
// 429 or any 5xx as retryable and every 4xx as permanent. RetryAfter carries
// the server's Retry-After hint (capped) whenever the response includes a
// Retry-After header (in practice a 429 or a 503); it is zero otherwise. Only a
// transient status actually consults it during retry.
type StatusError struct {
	Body       string
	Path       string
	RetryAfter time.Duration
	Code       int
}

// compile-time assertions that *StatusError participates in httpx transient
// classification and carries a retry-wait hint.
var (
	_ httpx.Transient      = (*StatusError)(nil)
	_ httpx.RetryAfterHint = (*StatusError)(nil)
)

// Error implements the error interface.
func (e *StatusError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("arrapi: %s: HTTP %d: %s", e.Path, e.Code, e.Body)
	}
	return fmt.Sprintf("arrapi: %s: HTTP %d", e.Path, e.Code)
}

// IsTransient reports whether the response is retryable: HTTP 429 (rate
// limited) or any 5xx (server error).
func (e *StatusError) IsTransient() bool {
	return e.Code == http.StatusTooManyRequests || e.Code >= 500
}

// RetryAfterHint implements httpx.RetryAfterHint: it exposes the capped
// Retry-After hint so httpx.RetryWithBackoff waits it out before the next retry
// instead of its jittered backoff. It is zero when the response carried no
// Retry-After (in practice, anything but a 429 or a 503), in which case
// RetryWithBackoff falls back to the jittered exponential backoff.
func (e *StatusError) RetryAfterHint() time.Duration { return e.RetryAfter }

// ResponseTooLargeError is returned when a response body exceeds the client's
// size cap for that endpoint. The body is rejected rather than truncated, so a
// caller never decodes a silently-cut payload.
type ResponseTooLargeError struct {
	Path  string
	Limit int64
}

// Error implements the error interface.
func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("arrapi: %s: response exceeds %d-byte limit", e.Path, e.Limit)
}

// IsNotFound reports whether err is (or wraps) a *StatusError with a 404 status.
func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusNotFound
}

// IsRateLimited reports whether err is (or wraps) a *StatusError with a 429
// status. When true, the *StatusError's RetryAfter carries the server's hint if
// it sent one.
func IsRateLimited(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusTooManyRequests
}
