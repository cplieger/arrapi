package arrapi_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/go/textsafe"
)

// hostile is server text that would forge a log line (CR, LF), reorder it (a bidi
// override) or flood it (1 MiB), if Error rendered it as given.
var hostile = "a\r\nforged\u202eb" + strings.Repeat("x", 1<<20)

// assertOneLine fails unless s carries no rune a single-line sink must not receive.
func assertOneLine(t *testing.T, s string) {
	t.Helper()
	for i, r := range s {
		if textsafe.IsUnsafeSingleLine(r) {
			t.Fatalf("Error() holds unsafe rune %U at byte %d", r, i)
		}
	}
}

func TestStatusErrorError(t *testing.T) {
	tests := []struct {
		name string
		err  *arrapi.StatusError
		want string
	}{
		{
			"message preferred over body",
			&arrapi.StatusError{Code: 400, Path: "/api/v5/episode", Message: "seriesId is required", Body: `{"message":"seriesId is required","description":"at Sonarr.Api"}`},
			"arrapi: /api/v5/episode: HTTP 400: seriesId is required",
		},
		{
			"body when no message",
			&arrapi.StatusError{Code: 502, Path: "/api/v3/series", Body: "<html>Bad Gateway</html>"},
			"arrapi: /api/v3/series: HTTP 502: <html>Bad Gateway</html>",
		},
		{
			"multi-line body on one line",
			&arrapi.StatusError{Code: 502, Path: "/api/v3/series", Body: "upstream failed\r\nlevel=INFO msg=forged"},
			"arrapi: /api/v3/series: HTTP 502: upstream failed  level=INFO msg=forged",
		},
		{
			"bare without message or body",
			&arrapi.StatusError{Code: 500, Path: "/api/v3/movie"},
			"arrapi: /api/v3/movie: HTTP 500",
		},
		{
			"attributed adds no text",
			&arrapi.StatusError{Code: 500, Path: "/api/v3/movie", Attributed: true},
			"arrapi: /api/v3/movie: HTTP 500",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusErrorErrorIsOneLine(t *testing.T) {
	for name, e := range map[string]*arrapi.StatusError{
		"message": {Code: 500, Path: hostile, Message: hostile},
		"body":    {Code: 500, Path: hostile, Body: hostile},
		"bare":    {Code: 500, Path: hostile},
	} {
		t.Run(name, func(t *testing.T) { assertOneLine(t, e.Error()) })
	}
}

// retryableByStatusRule is the status rule as the defining RFCs (9110, 6585, 2295, 5842,
// 2774) define the codes: 408 and 429 ask for a later retry, and a 5xx may succeed on
// one except those that repeat (501 Not Implemented, 505 HTTP Version Not Supported,
// 506 Variant Also Negotiates, 508 Loop Detected, 510 Not Extended, 511 Network
// Authentication Required).
func retryableByStatusRule(code int) bool {
	switch code {
	case 408, 429:
		return true
	case 501, 505, 506, 508, 510, 511:
		return false
	}
	return code >= 500 && code <= 599
}

func TestStatusErrorIsTransient(t *testing.T) {
	for code := 100; code <= 699; code++ {
		want := retryableByStatusRule(code)
		t.Run(fmt.Sprintf("HTTP %d", code), func(t *testing.T) {
			e := &arrapi.StatusError{Code: code}
			if got := e.IsTransient(); got != want {
				t.Errorf("IsTransient() = %v, want %v", got, want)
			}
			if got := httpclient.IsTransient(fmt.Errorf("wrapped: %w", e)); got != want {
				t.Errorf("httpclient.IsTransient(wrapped) = %v, want %v", got, want)
			}
		})
	}
}

func TestStatusErrorRetryAfterHintWithoutHint(t *testing.T) {
	var hint httpclient.RetryAfterHint = &arrapi.StatusError{Code: 503, Path: "/api/v3/series"}
	if got := hint.RetryAfterHint(); got != 0 {
		t.Errorf("RetryAfterHint() without a hint = %v, want 0", got)
	}
}

func TestResponseTooLargeErrorError(t *testing.T) {
	e := &arrapi.ResponseTooLargeError{Path: "/api/v3/series", Limit: 64 << 20}
	want := "arrapi: /api/v3/series: response exceeds 67108864-byte limit"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestUnsupportedAPIErrorNamesTheFix(t *testing.T) {
	tests := []struct {
		name string
		err  *arrapi.UnsupportedAPIError
		want []string
	}{
		{
			"Sonarr v5 (API v5) reachable only over the /api/v3 refusal path",
			&arrapi.UnsupportedAPIError{Reason: arrapi.ReasonV5Unreachable, App: "Sonarr", Version: "5.0.0.2635", Want: "Sonarr", Path: "/api/v3/system/status"},
			[]string{"Sonarr 5.0.0.2635 answers only over /api/v3", "arrapi reads Sonarr 5 and later only over /api/v5", "let /api/v5 through the reverse proxy"},
		},
		{
			"Sonarr v5 (API v5) announced, /api/v5 answers 404",
			&arrapi.UnsupportedAPIError{Reason: arrapi.ReasonV5Unreachable, Current: "v5", Deprecated: []string{"v3"}, Want: "Sonarr", Path: "/api/v5/system/status"},
			[]string{"Sonarr announces API v5 but /api/v5 answers 404", "let /api/v5 through the reverse proxy"},
		},
		{
			"Sonarr v3 (unsupported)",
			&arrapi.UnsupportedAPIError{Reason: arrapi.ReasonUnsupportedVersion, App: "Sonarr", Version: "3.0.10.1567", Want: "Sonarr", Path: "/api/v3/system/status"},
			[]string{"Sonarr 3.0.10.1567 is not supported", "arrapi reads Sonarr 4 over /api/v3 and Sonarr 5 and later over /api/v5"},
		},
		{
			"unknown generation with deprecated list",
			&arrapi.UnsupportedAPIError{Reason: arrapi.ReasonUnknownGeneration, App: "Sonarr", Version: "7.0.0.1", Current: "v7", Deprecated: []string{"v6", "v4"}, Want: "Sonarr"},
			[]string{"Sonarr announces API v7 (deprecated: v6, v4), which arrapi does not implement"},
		},
		{
			"unknown generation without deprecated list",
			&arrapi.UnsupportedAPIError{Reason: arrapi.ReasonUnknownGeneration, App: "Radarr", Version: "7.0.0.1", Current: "v4", Want: "Radarr"},
			[]string{"Radarr announces API v4, which arrapi does not implement"},
		},
		{
			"wrong application",
			&arrapi.UnsupportedAPIError{Reason: arrapi.ReasonWrongApp, App: "Radarr", Version: "6.4.4.10685", Want: "Sonarr", Path: "/api/v3/system/status"},
			[]string{"the URL answers as Radarr, not Sonarr"},
		},
		{
			"not the API",
			&arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNotAPI, Want: "Sonarr", Path: "/api/v5/system/status"},
			[]string{"/api/v5/system/status answered with a web page or other content, not the Sonarr API", "check the URL's base path"},
		},
		{
			"no version header",
			&arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNoVersionHeader, App: "Radarr", Version: "6.4.4.10685", Want: "Radarr", Path: "/api/v3/system/status"},
			[]string{"Radarr's answer to /api/v3/system/status carries no X-Application-Version", "let the header through the reverse proxy"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.err.Error()
			if !strings.HasPrefix(got, "arrapi: ") {
				t.Errorf("Error() = %q, want the arrapi: prefix", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("Error() = %q, want it to contain %q", got, w)
				}
			}
		})
	}
}

func TestUnsupportedAPIErrorRendersOneBoundedLine(t *testing.T) {
	reasons := []arrapi.UnsupportedReason{
		0,
		arrapi.ReasonV5Unreachable,
		arrapi.ReasonUnsupportedVersion,
		arrapi.ReasonUnknownGeneration,
		arrapi.ReasonWrongApp,
		arrapi.ReasonNotAPI,
		arrapi.ReasonNoVersionHeader,
	}
	for _, reason := range reasons {
		t.Run(fmt.Sprintf("server fields, reason %d", reason), func(t *testing.T) {
			e := &arrapi.UnsupportedAPIError{
				Reason:     reason,
				App:        hostile,
				Version:    hostile,
				Current:    hostile,
				Deprecated: slices.Repeat([]string{hostile}, 20),
				Want:       "Sonarr",
				Path:       "/api/v3/system/status",
			}
			got := e.Error()
			assertOneLine(t, got)
			if strings.Contains(got, strings.Repeat("x", 65)) {
				t.Errorf("Error() renders a server field over 64 bytes")
			}
			if len(got) > 1024 {
				t.Errorf("len(Error()) = %d, want at most 1024 with every server field capped", len(got))
			}
		})
		t.Run(fmt.Sprintf("every field, reason %d", reason), func(t *testing.T) {
			e := &arrapi.UnsupportedAPIError{
				Reason: reason, App: hostile, Version: hostile, Current: hostile,
				Deprecated: []string{hostile}, Want: hostile, Path: hostile,
			}
			assertOneLine(t, e.Error())
		})
	}
}

func TestServerChangedErrorFields(t *testing.T) {
	var names []string
	for f := range reflect.TypeFor[arrapi.ServerChangedError]().Fields() {
		names = append(names, f.Name)
	}
	want := []string{"Status", "Origin", "Path", "From", "To"}
	if !slices.Equal(names, want) {
		t.Errorf("ServerChangedError fields = %v, want exactly %v", names, want)
	}
}

func TestServerChangedErrorError(t *testing.T) {
	const (
		origin  = "http://sonarr.example:8989"
		keyPath = "/api/v3/series/0123456789abcdef0123456789abcdef?apikey=0123456789abcdef" // gitleaks:allow: a key-shaped placeholder Error must never render
	)
	changed := "arrapi: http://sonarr.example:8989 answered with version 5.0.0.2635, but version 4.0.20.3014 also answered this client: the server behind the URL changed"
	unconfirmed := "arrapi: http://sonarr.example:8989: version 4.0.20.3014 is no longer confirmed (an answer without X-Application-Version, a detection that failed, or another answer since): a later call uses a current detection or detects again"
	tests := []struct {
		name string
		err  *arrapi.ServerChangedError
		want string
	}{
		{"another version answered", &arrapi.ServerChangedError{Origin: origin, Path: keyPath, From: "4.0.20.3014", To: "5.0.0.2635"}, changed},
		{"no version answered", &arrapi.ServerChangedError{Origin: origin, Path: keyPath, From: "4.0.20.3014"}, unconfirmed},
		{"the same version again", &arrapi.ServerChangedError{Origin: origin, Path: keyPath, From: "4.0.20.3014", To: "4.0.20.3014"}, unconfirmed},
		{
			"with the answer that failed",
			&arrapi.ServerChangedError{Status: &arrapi.StatusError{Code: 503, Path: keyPath}, Origin: origin, Path: keyPath, From: "4.0.20.3014", To: "5.0.0.2635"},
			changed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.err.Error()
			if got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
			for _, rendered := range []string{got, fmt.Sprintf("%v", tc.err), fmt.Sprintf("%+v", tc.err)} {
				for _, banned := range []string{"/api", "?", "0123456789abcdef", "Sonarr", "Radarr", "sent to", "before", "version ,", "  "} {
					if strings.Contains(rendered, banned) {
						t.Errorf("rendering %q contains %q", rendered, banned)
					}
				}
			}
		})
	}
}

func TestServerChangedErrorRendersOneBoundedLine(t *testing.T) {
	hostileOrigin := "http://a\r\nforged\u202e" + strings.Repeat("o", 1<<20)
	tests := []struct {
		name string
		to   string
	}{
		{"changed", hostile},
		{"unconfirmed", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &arrapi.ServerChangedError{Origin: hostileOrigin, From: hostile, To: tc.to}
			got := e.Error()
			assertOneLine(t, got)
			if strings.Contains(got, strings.Repeat("x", 65)) {
				t.Errorf("Error() renders a version over 64 bytes")
			}
			if strings.Contains(got, strings.Repeat("o", 256)) {
				t.Errorf("Error() renders an origin over 255 bytes")
			}
		})
	}
}

func TestServerChangedErrorUnwrap(t *testing.T) {
	bare := &arrapi.ServerChangedError{Origin: "http://sonarr:8989", From: "4.0.20.3014", To: "5.0.0.2635"}
	if got := bare.Unwrap(); got != nil {
		t.Errorf("Unwrap() without Status = %#v, want a nil error", got)
	}
	if _, ok := errors.AsType[*arrapi.StatusError](bare); ok {
		t.Error("errors.As found a *StatusError on a ServerChangedError without Status")
	}

	status := &arrapi.StatusError{Code: 404, Path: "/api/v3/series/1"}
	withStatus := &arrapi.ServerChangedError{Status: status, Origin: "http://sonarr:8989", From: "4.0.20.3014", To: "5.0.0.2635"}
	got, ok := errors.AsType[*arrapi.StatusError](fmt.Errorf("call: %w", withStatus))
	if !ok || got != status {
		t.Errorf("errors.As = %v, %v; want the Status pointer", got, ok)
	}
}

func TestErrCommandOutcomeUnknownWrapsItsCause(t *testing.T) {
	changed := &arrapi.ServerChangedError{Origin: "http://sonarr:8989", From: "4.0.20.3014", To: "5.0.0.2635"}
	noHeader := &arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNoVersionHeader, Want: "Sonarr", Path: "/api/v3/command"}

	err := fmt.Errorf("%w: %w", arrapi.ErrCommandOutcomeUnknown, changed)
	if !errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
		t.Error("errors.Is(err, ErrCommandOutcomeUnknown) = false")
	}
	if got, ok := errors.AsType[*arrapi.ServerChangedError](err); !ok || got != changed {
		t.Errorf("errors.As(*ServerChangedError) = %v, %v; want the cause", got, ok)
	}
	want := "arrapi: the command's outcome is unknown and it was not re-sent: arrapi: http://sonarr:8989 answered with version 5.0.0.2635"
	if got := err.Error(); !strings.HasPrefix(got, want) {
		t.Errorf("Error() = %q, want prefix %q", got, want)
	}

	err = fmt.Errorf("%w: %w", arrapi.ErrCommandOutcomeUnknown, noHeader)
	if !errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
		t.Error("errors.Is(err, ErrCommandOutcomeUnknown) = false")
	}
	if got, ok := errors.AsType[*arrapi.UnsupportedAPIError](err); !ok || got != noHeader {
		t.Errorf("errors.As(*UnsupportedAPIError) = %v, %v; want the cause", got, ok)
	}
}

// TestDecodeErrorCallerBuilt pins Error for a value built outside arrapi, whose
// unexported fields are empty: the path on one line, the generic reason, never Err.
func TestDecodeErrorCallerBuilt(t *testing.T) {
	cause := errors.New("cause text " + hostile[:20])
	e := &arrapi.DecodeError{Err: cause, Method: "GET", Path: "/api/v3/series\r\nforged", Status: 200}
	const want = "arrapi: decode /api/v3/series  forged: the answer does not decode"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(e, cause) || errors.Unwrap(e) != cause {
		t.Errorf("Unwrap() = %v, want the cause it holds", errors.Unwrap(e))
	}
	if got := (&arrapi.DecodeError{}).Error(); got != "arrapi: decode : the answer does not decode" {
		t.Errorf("zero DecodeError Error() = %q", got)
	}
	if errors.Unwrap(&arrapi.DecodeError{}) != nil {
		t.Error("zero DecodeError unwraps to a cause")
	}
}

// TestCheckErrorError pins Error for caller-built values: the *DecodeError's text only
// under CheckServer, the cause's text otherwise, a fixed sentence without a cause, and
// one line whatever the cause renders.
func TestCheckErrorError(t *testing.T) {
	decode := &arrapi.DecodeError{Path: "/api/v3/system/status", ContentType: "application/json"}
	notAPI := &arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNotAPI, Want: "Sonarr", Path: "/api/v3/system/status", Err: decode}
	const notAPIText = "arrapi: /api/v3/system/status answered with a web page or other content, not the Sonarr API: check the URL's base path"
	tests := []struct {
		name string
		e    *arrapi.CheckError
		want string
	}{
		{"server over a decode error", &arrapi.CheckError{Reason: arrapi.CheckServer, Err: notAPI}, decode.Error()},
		{"not the API over a decode error", &arrapi.CheckError{Reason: arrapi.CheckNotArrAPI, Err: notAPI}, notAPIText},
		{
			"server without a decode error",
			&arrapi.CheckError{Reason: arrapi.CheckServer, Err: &arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNotAPI, Want: "Sonarr", Path: "/api/v3/system/status"}},
			notAPIText,
		},
		{"no cause", &arrapi.CheckError{Reason: arrapi.CheckURL}, "arrapi: connection check failed"},
		{"a cause on two lines", &arrapi.CheckError{Reason: arrapi.CheckURL, Err: errors.New("first\nsecond")}, "first second"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.e.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckErrorUnwrap(t *testing.T) {
	cause := &arrapi.StatusError{Code: 502}
	e := &arrapi.CheckError{Reason: arrapi.CheckServer, Err: fmt.Errorf("wrapped: %w", cause)}
	if got, ok := errors.AsType[*arrapi.StatusError](e); !ok || got != cause {
		t.Errorf("errors.As(*StatusError) = %v, %v; want the cause", got, ok)
	}
	if errors.Unwrap(&arrapi.CheckError{}) != nil {
		t.Error("a CheckError without a cause unwraps to one")
	}
}

func TestUnsupportedAPIErrorUnwrap(t *testing.T) {
	decode := &arrapi.DecodeError{Path: "/api/v3/system/status"}
	e := &arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNotAPI, Err: decode}
	if got, ok := errors.AsType[*arrapi.DecodeError](e); !ok || got != decode {
		t.Errorf("errors.As(*DecodeError) = %v, %v; want Err", got, ok)
	}
	if errors.Unwrap(&arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNotAPI}) != nil {
		t.Error("an UnsupportedAPIError without Err unwraps to a cause")
	}
}

func TestGrabErrorError(t *testing.T) {
	se := &arrapi.StatusError{Code: 404, Path: "/api/v3/release", Message: "Couldn't find requested release in cache, try searching again"}
	const seText = "arrapi: /api/v3/release: HTTP 404: Couldn't find requested release in cache, try searching again"
	tests := []struct {
		name string
		e    *arrapi.GrabError
		want string
	}{
		{"not cached", &arrapi.GrabError{Err: se, Reason: arrapi.GrabNotCached}, "arrapi: grab refused (not in the search cache): " + seText},
		{"unmapped", &arrapi.GrabError{Err: se, Reason: arrapi.GrabUnmapped}, "arrapi: grab refused (not mapped): " + seText},
		{"unavailable", &arrapi.GrabError{Err: se, Reason: arrapi.GrabUnavailable}, "arrapi: grab refused (unavailable from the indexer): " + seText},
		{"a reason outside the three", &arrapi.GrabError{Err: se, Reason: 9}, "arrapi: grab refused (refused): " + seText},
		{"the zero reason", &arrapi.GrabError{Err: se}, "arrapi: grab refused (refused): " + seText},
		{"no status", &arrapi.GrabError{Reason: arrapi.GrabNotCached}, "arrapi: grab refused"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.e.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGrabErrorUnwrap(t *testing.T) {
	se := &arrapi.StatusError{Code: 409, Message: "Getting release from indexer failed"}
	var err error = &arrapi.GrabError{Err: se, Reason: arrapi.GrabUnavailable}
	if got, ok := errors.AsType[*arrapi.StatusError](err); !ok || got != se {
		t.Errorf("errors.As(*StatusError) = %v, %v; want Err", got, ok)
	}
	if got, ok := errors.AsType[*arrapi.GrabError](fmt.Errorf("wrapped: %w", err)); !ok || got.Reason != arrapi.GrabUnavailable {
		t.Errorf("errors.As(*GrabError) through a wrap = %v, %v; want the GrabError", got, ok)
	}
	if u := (&arrapi.GrabError{Reason: arrapi.GrabNotCached}).Unwrap(); u != nil {
		t.Errorf("Unwrap() without Err = %#v, want an untyped nil", u)
	}
}
