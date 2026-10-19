package transport

import (
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/cplieger/arrapi/v3"
)

// The validation errors render neither the URL nor the key and wrap nothing that
// does: url.Parse's *url.Error quotes its raw input, which can carry a key pasted
// into the query or a user-info password.
var (
	errBaseURLInvalid  = errors.New("arrapi: baseURL is not a valid URL")
	errBaseURLAbsolute = errors.New("arrapi: baseURL must be an absolute http or https URL")
	errBaseURLHost     = errors.New("arrapi: baseURL must have a host")
	errBaseURLQuery    = errors.New("arrapi: baseURL must not carry a query or a fragment")
	errKeyEmpty        = errors.New("arrapi: key is empty")
	errKeyAlphabet     = errors.New("arrapi: key may hold only letters, digits, '.', '_' and '-'")
)

// keyAlphabet is every byte that each encoder able to write the key into an answer
// (Go's net/url and encoding/json, .NET's Uri.EscapeDataString, WebUtility.UrlEncode and
// System.Text.Json) leaves as it is, and that no runesafe sanitizer writes or rewrites:
// so the key as sent is the only form those encoders write, and redaction (redact.go)
// matches it byte for byte, before or after sanitizing alike.
const keyAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-"

func validate(baseURL string, key arrapi.APIKey) (*url.URL, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, errBaseURLInvalid
	}
	if !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errBaseURLAbsolute
	}
	if u.Hostname() == "" {
		return nil, errBaseURLHost
	}
	if strings.ContainsAny(baseURL, "?#") {
		return nil, errBaseURLQuery
	}
	if strings.TrimSpace(string(key)) == "" {
		return nil, errKeyEmpty
	}
	if strings.ContainsFunc(string(key), func(r rune) bool { return !strings.ContainsRune(keyAlphabet, r) }) {
		return nil, errKeyAlphabet
	}
	return u, nil
}

// originOf renders scheme://host[:port] of u: no user info, and the port only when
// it is not the scheme's default.
func originOf(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := u.Hostname()
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		return scheme + "://" + net.JoinHostPort(host, port)
	}
	if strings.Contains(host, ":") {
		return scheme + "://[" + host + "]"
	}
	return scheme + "://" + host
}
