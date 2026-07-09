package arrapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cplieger/httpx/v2"
)

// avgItemSize is a rough per-item JSON size used only to seed slice capacity;
// the decoder grows the slice as needed, so an undershoot is cheap.
const avgItemSize = 200

// maxPrealloc caps the speculative capacity so an oversized body cannot drive a
// large up-front allocation.
const maxPrealloc = 8192

// statusError drains and closes a non-2xx response and returns a *StatusError
// carrying the (size-capped) response body and any Retry-After hint. The
// caller's apiKey is redacted from the captured body so a hostile or
// compromised endpoint cannot reflect the request credential back into a
// caller's logs.
func statusError(resp *http.Response, path, apiKey string) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	resp.Body.Close()
	e := &StatusError{
		Code:       resp.StatusCode,
		Path:       path,
		RetryAfter: httpx.ParseRetryAfter(resp.Header.Get("Retry-After")),
	}
	if err == nil {
		e.Body = redactSecret(strings.TrimSpace(string(body)), apiKey)
	}
	return e
}

// redactSecret replaces every occurrence of secret in s with a placeholder. It
// returns s unchanged when secret is empty, so a client configured without a
// key (never valid in practice) does not redact arbitrary empty matches.
func redactSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[REDACTED]")
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
	if hint := min(len(data)/avgItemSize, maxPrealloc); hint > 0 {
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

// decodePage reads a bounded paged-collection JSON object and decodes it. It uses the
// list cap (maxListBytes) rather than the single-object cap because a history page
// wraps an arbitrarily long records array.
func decodePage[T any](resp *http.Response, path string) (T, error) {
	var v T
	data, err := readBounded(resp, maxListBytes, path)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("arrapi: decode %s: %w", path, err)
	}
	return v, nil
}
