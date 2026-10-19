package transport

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/cplieger/httpx/v5"
)

// defaultRedirectPolicy is every read client's follow-or-refuse decision: the exact
// origin (RFC 6454) under httpx's own hop cap; transport passes no hop count of its own.
var defaultRedirectPolicy = httpx.RedirectPolicyFunc(httpx.WithSameOrigin(true))

// redirectRefusedError is checkDefaultRedirect's error: the policy's refusal
// plus what safeDoError needs to name the cap or the fix. It never leaves transport,
// because target is a Location a server chose and can carry the key.
type redirectRefusedError struct {
	cause    error
	target   *url.URL
	followed int  // the redirects followed before the refused one
	tooMany  bool // the policy's hop cap refused it
	upgrade  bool
}

func (e *redirectRefusedError) Error() string { return e.cause.Error() }
func (e *redirectRefusedError) Unwrap() error { return e.cause }

// readRedirectPolicy is the CheckRedirect of every probe and read: checkDefaultRedirect,
// then the caller's own policy, if any, which can only refuse more. net/http forwards
// X-Api-Key on every followed redirect, so no caller policy may take it to another
// origin or past the hop cap.
func readRedirectPolicy(caller func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	if caller == nil {
		return checkDefaultRedirect
	}
	return func(req *http.Request, via []*http.Request) error {
		if err := checkDefaultRedirect(req, via); err != nil {
			return err
		}
		return caller(req, via)
	}
}

func checkDefaultRedirect(req *http.Request, via []*http.Request) error {
	err := defaultRedirectPolicy(req, via)
	if err == nil || errors.Is(err, http.ErrUseLastResponse) {
		return err
	}
	refused, typed := errors.AsType[*httpx.RedirectRefusedError](err)
	from := via[0].URL
	return &redirectRefusedError{
		cause:    err,
		target:   req.URL,
		followed: len(via) - 1,
		tooMany:  typed && refused.Reason == httpx.RedirectTooMany,
		upgrade: strings.EqualFold(from.Scheme, "http") && strings.EqualFold(req.URL.Scheme, "https") &&
			asciiLower(from.Hostname()) == asciiLower(req.URL.Hostname()),
	}
}

// asciiLower folds only A-Z: strings.ToLower also folds other bytes and runes, which
// could make two different hosts compare equal.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
