package arrapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cplieger/httpx/v3"
	"github.com/cplieger/runesafe"
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
// caller's logs, and the body is sanitized with runesafe.Sanitize so
// terminal-escape, C1, and bidi control runes from a hostile or garbled
// response never reach a caller's log stream.
func statusError(resp *http.Response, path, apiKey string) error {
	body, err := readErrorBody(resp.Body, apiKey)
	e := &StatusError{
		Code:       resp.StatusCode,
		Path:       path,
		RetryAfter: httpx.ParseRetryAfter(resp.Header.Get("Retry-After")),
	}
	if err == nil {
		e.Body = body
	}
	return e
}

// readErrorBody drains and closes body, redacts apiKey (and its
// whitespace-trimmed variant, which an HTTP peer may reflect back after
// stripping header OWS), then caps the result at maxErrorBodyBytes. Redaction
// happens before the cap (over a maxErrorBodyBytes+len(apiKey) read window) so a
// key straddling the cap boundary is still matched and stripped in full rather
// than leaving a credential prefix in the captured body. The residual
// trailing-key-prefix cleanup runs only when the read window actually truncated
// the body AND redaction shrank it -- the sole case that can pull an unmatched
// key prefix back under the cap -- so a fully-read short body whose text merely
// happens to end with the key's first characters is never over-redacted.
//
// The final step sanitizes the captured body with runesafe.Sanitize: C0/C1
// controls, bidi controls, and the U+2028/U+2029 separators become spaces,
// and invalid UTF-8 bytes become U+FFFD. Sanitization runs last so redaction
// string-matches the raw wire bytes, and because U+FFFD replacement can grow
// the byte length, the result is re-capped at maxErrorBodyBytes with
// runesafe.CapBytes, whose rune-boundary cut cannot reintroduce an unsafe
// partial-rune tail.
func readErrorBody(body io.ReadCloser, apiKey string) (string, error) {
	defer body.Close()
	readLimit := maxErrorBodyBytes
	if apiKey != "" {
		readLimit += len(apiKey)
	}
	data, err := io.ReadAll(io.LimitReader(body, int64(readLimit)+1))
	if err != nil {
		return "", err
	}
	truncatedAtReadWindow := len(data) > readLimit
	if truncatedAtReadWindow {
		data = data[:readLimit]
	}
	trimmed := strings.TrimSpace(string(data))
	redacted := httpx.RedactSecretString(trimmed, apiKey)
	trimmedKey := strings.TrimSpace(apiKey)
	if trimmedKey != apiKey {
		redacted = httpx.RedactSecretString(redacted, trimmedKey)
	}
	redactionShrank := len(redacted) < len(trimmed)
	if len(redacted) > maxErrorBodyBytes {
		redacted = redacted[:maxErrorBodyBytes]
	}
	if truncatedAtReadWindow && redactionShrank {
		redacted = trimTrailingSecretPrefix(redacted, apiKey)
		if trimmedKey != apiKey {
			redacted = trimTrailingSecretPrefix(redacted, trimmedKey)
		}
	}
	return runesafe.CapBytes(runesafe.Sanitize(redacted), maxErrorBodyBytes), nil
}

// trimTrailingSecretPrefix removes a trailing run of s that is a non-empty proper prefix
// of secret. httpx.RedactSecretString matches only whole occurrences, so a secret straddling the
// read-window boundary is truncated to a prefix ReplaceAll cannot match; that prefix is
// always a suffix of the redacted, capped body. Stripping it closes the residual
// credential-prefix leak. Returns s unchanged when secret is empty.
func trimTrailingSecretPrefix(s, secret string) string {
	if secret == "" {
		return s
	}
	maxLen := min(len(secret)-1, len(s))
	for n := maxLen; n >= 1; n-- {
		if strings.HasSuffix(s, secret[:n]) {
			return s[:len(s)-n]
		}
	}
	return s
}

// readBounded reads the response body up to limit bytes and closes it. If the
// body exceeds the limit it returns a *ResponseTooLargeError rather than
// silently truncating, so a caller never decodes a partial payload.
func readBounded(resp *http.Response, limit int64, path string) ([]byte, error) {
	data, err := httpx.ReadLimitedBody(resp.Body, limit)
	if err != nil {
		var tooLarge *httpx.ResponseTooLargeError
		if errors.As(err, &tooLarge) {
			return nil, &ResponseTooLargeError{Path: path, Limit: limit}
		}
		return nil, fmt.Errorf("arrapi: read %s: %w", path, err)
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

// decodeBounded reads a JSON response of at most limit bytes and decodes it
// into a T, rejecting an over-cap body via readBounded.
func decodeBounded[T any](resp *http.Response, limit int64, path string) (T, error) {
	var v T
	data, err := readBounded(resp, limit, path)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("arrapi: decode %s: %w", path, err)
	}
	return v, nil
}

// decodeObject reads a bounded single-object JSON response and decodes it.
func decodeObject[T any](resp *http.Response, path string) (T, error) {
	return decodeBounded[T](resp, maxObjectBytes, path)
}

// decodePage reads a bounded paged-collection JSON object and decodes it. It
// uses the list cap (maxListBytes) rather than the single-object cap because a
// history page wraps an arbitrarily long records array.
func decodePage[T any](resp *http.Response, path string) (T, error) {
	return decodeBounded[T](resp, maxListBytes, path)
}
