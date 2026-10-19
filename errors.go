package arrapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cplieger/arrapi/v3/internal/statushook"
	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/runesafe/v3"
)

func init() {
	statushook.SetRetryAfter = func(err error, d time.Duration) {
		if e, ok := errors.AsType[*StatusError](err); ok {
			e.retryAfter = min(max(d, 0), httpx.RetryAfterCap)
		}
	}
}

// StatusError is an answer that is not a success, detection probes included: any status
// but 200 to a GET, any non-2xx to a command.
type StatusError struct {
	Message    string // the server's one-line message, key redacted (error-model "message", else ProblemDetails "title"); "" when none
	Body       string // the capture: key redacted, sanitized keeping CR and LF (safe for a sink that escapes them), at most 64 KiB, ending in "..." when cut
	Path       string
	Code       int
	retryAfter time.Duration
}

var (
	_ httpx.Transient      = (*StatusError)(nil)
	_ httpx.RetryAfterHint = (*StatusError)(nil)
)

// Error renders "arrapi: <path>: HTTP <code>", followed by Message, else Body, when set,
// on one line: each rune unsafe in a single-line sink, CR and LF included, becomes a
// space. Body itself keeps its line breaks.
func (e *StatusError) Error() string {
	detail := e.Message
	if detail == "" {
		detail = e.Body
	}
	path := runesafe.SanitizeSingleLine(e.Path)
	if detail == "" {
		return fmt.Sprintf("arrapi: %s: HTTP %d", path, e.Code)
	}
	return fmt.Sprintf("arrapi: %s: HTTP %d: %s", path, e.Code, runesafe.SanitizeSingleLine(detail))
}

// IsTransient reports whether the status is worth sending again: 429, or 500 through
// 599. 600 and above are not.
func (e *StatusError) IsTransient() bool {
	return e.Code == http.StatusTooManyRequests || (e.Code >= 500 && e.Code <= 599)
}

// RetryAfterHint is the answer's capped Retry-After; 0 when it carried none.
func (e *StatusError) RetryAfterHint() time.Duration { return e.retryAfter }

// ResponseTooLargeError is an answer whose body exceeds the cap for its path. The body
// is rejected, never decoded cut.
type ResponseTooLargeError struct {
	Path  string
	Limit int64
}

func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("arrapi: %s: response exceeds %d-byte limit", e.Path, e.Limit)
}

// UnsupportedReason says why a server cannot be read.
type UnsupportedReason int

// The reasons a server cannot be read.
const (
	ReasonV5Unreachable      UnsupportedReason = iota + 1 // Sonarr 5 or later answers, its announced API path does not
	ReasonUnsupportedVersion                              // a Sonarr below 4, or one announcing API v3 at a major other than 4
	ReasonUnknownGeneration                               // the server announces no API generation arrapi implements
	ReasonWrongApp                                        // system/status names another application
	ReasonNotAPI                                          // a status path answers 200 with something that is not a status, such as a web page (wrong base path)
	ReasonNoVersionHeader                                 // a status answer, or a successful data or command answer, carries no well-formed X-Application-Version
)

// UnsupportedAPIError says the server answered but cannot be read; Error names the fix.
// App, Version, Current and Deprecated are server text with the client's API key redacted,
// single-line and at most 64 bytes each.
type UnsupportedAPIError struct {
	App        string   // appName the server reported; "" when unknown
	Version    string   // its version; "" when unknown
	Current    string   // /api "current"; "" when /api was not readable
	Want       string   // "Sonarr" or "Radarr"
	Path       string   // the request path that answered, relative to the base URL
	Deprecated []string // /api "deprecated": its well-formed tokens ("v" and 1 to 3 digits), at most 8
	Reason     UnsupportedReason
}

// Error renders one line whatever the fields hold: server text is sanitized and capped
// again, because a value built outside arrapi carries no such guarantee. It cannot
// redact a key, which it never holds.
func (e *UnsupportedAPIError) Error() string {
	want, path := runesafe.SanitizeSingleLine(e.Want), runesafe.SanitizeSingleLine(e.Path)
	switch e.Reason {
	case ReasonV5Unreachable:
		if current := serverText(e.Current); current != "" {
			return fmt.Sprintf("arrapi: %s announces API %s but /api/%s answers 404: let /api/%s through the reverse proxy", want, current, current, current)
		}
		return fmt.Sprintf("arrapi: %s %s answers only over /api/v3, and arrapi reads Sonarr 5 and later only over /api/v5: let /api/v5 through the reverse proxy", want, serverText(e.Version))
	case ReasonUnsupportedVersion:
		return fmt.Sprintf("arrapi: %s %s is not supported: arrapi reads Sonarr 4 over /api/v3 and Sonarr 5 and later over /api/v5", want, serverText(e.Version))
	case ReasonUnknownGeneration:
		deprecated := ""
		if list := e.deprecatedList(); list != "" {
			deprecated = " (deprecated: " + list + ")"
		}
		return fmt.Sprintf("arrapi: %s announces API %s%s, which arrapi does not implement", want, serverText(e.Current), deprecated)
	case ReasonWrongApp:
		return fmt.Sprintf("arrapi: the URL answers as %s, not %s", serverText(e.App), want)
	case ReasonNotAPI:
		return fmt.Sprintf("arrapi: %s answered with a web page or other content, not the %s API: check the URL's base path", path, want)
	case ReasonNoVersionHeader:
		return fmt.Sprintf("arrapi: %s's answer to %s carries no X-Application-Version, which arrapi needs to tell server versions apart: let the header through the reverse proxy", want, path)
	default:
		return fmt.Sprintf("arrapi: %s: the %s API cannot be read (reason %d)", path, want, e.Reason)
	}
}

// maxDeprecated matches the bound internal/detect owns (its maxDeprecated).
const maxDeprecated = 8

func (e *UnsupportedAPIError) deprecatedList() string {
	tokens := make([]string, 0, min(len(e.Deprecated), maxDeprecated))
	for _, d := range e.Deprecated[:min(len(e.Deprecated), maxDeprecated)] {
		tokens = append(tokens, serverText(d))
	}
	return strings.Join(tokens, ", ")
}

// ServerChangedError says an answer to this client came from another server version
// than the detection it relied on, or carried no version: the server behind the URL
// changed or can no longer be confirmed. A later call uses a current detection or
// detects again; a pinned read returns it until an answer from the pinned version
// reaches the client again. Origin, From and To have the client's API key redacted.
type ServerChangedError struct {
	Status *StatusError // the answer when it was not a success; nil otherwise
	Origin string       // scheme://host[:port] of the configured base URL; the only location Error renders
	Path   string       // the path arrapi built for the call or probe, never a redirect target's
	From   string       // the X-Application-Version the request or the detection relied on
	To     string       // the other version that answered; "" when none did (an answer without one, a failed detection, or From again), never From
}

// Error names Origin and the versions, never Path: a server change when To is a version
// other than From, else that From is no longer confirmed.
func (e *ServerChangedError) Error() string {
	origin, _ := runesafe.SanitizeSingleLineCapped(e.Origin, maxOriginBytes, "...")
	from := serverText(e.From)
	if e.To != "" && e.To != e.From {
		return fmt.Sprintf("arrapi: %s answered with version %s, but version %s also answered this client: the server behind the URL changed", origin, serverText(e.To), from)
	}
	return fmt.Sprintf("arrapi: %s: version %s is no longer confirmed (an answer without X-Application-Version, a detection that failed, or another answer since): a later call uses a current detection or detects again", origin, from)
}

// Unwrap returns Status, or nil when the answer was a success.
func (e *ServerChangedError) Unwrap() error {
	if e.Status == nil {
		return nil
	}
	return e.Status
}

// ErrCommandOutcomeUnknown is wrapped by a command's error when the server answered 2xx
// but the answer cannot be attributed to the server the command was sent to: it may
// have run, and it was not re-sent.
var ErrCommandOutcomeUnknown = errors.New("arrapi: the command's outcome is unknown and it was not re-sent")

const (
	// maxServerTextBytes matches the bound internal/transport owns (ServerTextBytes).
	maxServerTextBytes = 64
	maxOriginBytes     = 255
)

func serverText(s string) string {
	text, _ := runesafe.SanitizeSingleLineCapped(s, maxServerTextBytes, "...")
	return text
}
