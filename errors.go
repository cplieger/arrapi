package arrapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/cplieger/httpx/v2"
)

// StatusError is a non-2xx HTTP response from a Sonarr or Radarr API. It
// implements the httpx.Transient interface, so the httpx retry helpers treat a
// 429 or any 5xx as retryable and every 4xx as permanent.
type StatusError struct {
	Body string
	Path string
	Code int
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

// IsNotFound reports whether err is (or wraps) a *StatusError with a 404 status.
func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusNotFound
}
