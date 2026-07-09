package arrapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cplieger/httpx/v2"
)

// avgItemSize is a conservative lower bound on a decoded list item's JSON size,
// used only as a pre-allocation hint. Intentionally low so it never
// over-allocates badly; slice growth handles any undershoot.
const avgItemSize = 200

// statusError drains and closes a non-2xx response and returns a *StatusError
// carrying the (size-capped) response body and any Retry-After hint.
func statusError(resp *http.Response, path string) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	resp.Body.Close()
	e := &StatusError{
		Code:       resp.StatusCode,
		Path:       path,
		RetryAfter: httpx.ParseRetryAfter(resp.Header.Get("Retry-After")),
	}
	if err == nil {
		e.Body = strings.TrimSpace(string(body))
	}
	return e
}

// readBounded reads the response body up to limit bytes and closes it. If the
// body exceeds the limit it returns a *ResponseTooLargeError rather than
// silently truncating, so a caller never decodes a partial payload.
func readBounded(resp *http.Response, limit int64, path string) ([]byte, error) {
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("arrapi: read %s: %w", path, err)
	}
	if int64(len(data)) > limit {
		return nil, &ResponseTooLargeError{Path: path, Limit: limit}
	}
	return data, nil
}

// decodeSlice reads a bounded JSON array response and decodes it into a slice.
func decodeSlice[T any](resp *http.Response, path string) ([]T, error) {
	data, err := readBounded(resp, maxListBytes, path)
	if err != nil {
		return nil, err
	}
	var items []T
	if hint := len(data) / avgItemSize; hint > 0 {
		items = make([]T, 0, hint)
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("arrapi: decode %s: %w", path, err)
	}
	return items, nil
}

// decodeObject reads a bounded single-object JSON response and decodes it.
func decodeObject[T any](resp *http.Response, path string) (T, error) {
	var v T
	data, err := readBounded(resp, maxObjectBytes, path)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("arrapi: decode %s: %w", path, err)
	}
	return v, nil
}
