package arrapi

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cplieger/httpx/v2"
)

// StatusError is a non-2xx HTTP response from a Sonarr or Radarr API. It
// implements the httpx.Transient interface, so the httpx retry helpers treat a
// 429 or any 5xx as retryable and every 4xx as permanent. RetryAfter carries
// the server's Retry-After hint (capped) when the response is a 429/503 and the
// header is present; it is zero otherwise.
type StatusError struct {
	Body       string
	Path       string
	RetryAfter time.Duration
	Code       int
}

// compile-time assertion that *StatusError participates in httpx transient
// classification.
var _ httpx.Transient = (*StatusError)(nil)

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
