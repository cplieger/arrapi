package arrapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// avgItemSize is a conservative lower bound on a decoded list item's JSON size,
// used only as a pre-allocation hint from Content-Length. Intentionally low so
// it never over-allocates badly; slice growth handles any undershoot.
const avgItemSize = 200

// statusError drains and closes a non-2xx response and returns a *StatusError
// carrying the (size-capped) response body for context.
func statusError(resp *http.Response, path string) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	resp.Body.Close()
	e := &StatusError{Code: resp.StatusCode, Path: path}
	if err == nil {
		e.Body = strings.TrimSpace(string(body))
	}
	return e
}

// decodeSlice decodes a bounded JSON array response into a slice, using
// Content-Length as a pre-allocation hint. It closes the response body.
func decodeSlice[T any](resp *http.Response, path string) ([]T, error) {
	defer resp.Body.Close()

	var items []T
	if cl := resp.ContentLength; cl > 0 {
		if hint := int(cl) / avgItemSize; hint > 0 {
			items = make([]T, 0, hint)
		}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxListBytes)).Decode(&items); err != nil {
		return nil, fmt.Errorf("arrapi: decode %s: %w", path, err)
	}
	return items, nil
}

// decodeObject decodes a bounded single-object JSON response. It closes the
// response body.
func decodeObject[T any](resp *http.Response, path string) (T, error) {
	defer resp.Body.Close()

	var v T
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxObjectBytes)).Decode(&v); err != nil {
		return v, fmt.Errorf("arrapi: decode %s: %w", path, err)
	}
	return v, nil
}
