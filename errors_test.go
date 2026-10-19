package arrapi_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/runesafe/v3"
)

// hostile is server text that would forge a log line (CR, LF), reorder it (a bidi
// override) or flood it (1 MiB), if Error rendered it as given.
var hostile = "a\r\nforged\u202eb" + strings.Repeat("x", 1<<20)

// assertOneLine fails unless s carries no rune a single-line sink must not receive.
func assertOneLine(t *testing.T, s string) {
	t.Helper()
	for i, r := range s {
		if runesafe.IsUnsafeSingleLine(r) {
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

func TestStatusErrorIsTransient(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{400, false},
		{404, false},
		{408, false},
		{429, true},
		{499, false},
		{500, true},
		{503, true},
		{599, true},
		{600, false},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("HTTP %d", tc.code), func(t *testing.T) {
			e := &arrapi.StatusError{Code: tc.code}
			if got := e.IsTransient(); got != tc.want {
				t.Errorf("IsTransient() = %v, want %v", got, tc.want)
			}
			if got := httpx.IsTransient(fmt.Errorf("wrapped: %w", e)); got != tc.want {
				t.Errorf("httpx.IsTransient(wrapped) = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStatusErrorRetryAfterHintWithoutHint(t *testing.T) {
	var hint httpx.RetryAfterHint = &arrapi.StatusError{Code: 503, Path: "/api/v3/series"}
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
