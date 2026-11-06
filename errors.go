package arrapi

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cplieger/arrapi/v3/internal/decodehook"
	"github.com/cplieger/arrapi/v3/internal/statushook"
	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/go/textsafe"
)

func init() {
	statushook.SetRetryAfter = func(err error, d time.Duration) {
		if e, ok := errors.AsType[*StatusError](err); ok {
			e.retryAfter = min(max(d, 0), httpclient.RetryAfterCap)
		}
	}
	decodehook.New = func(f decodehook.Fields) error {
		return &DecodeError{
			Err: f.Err, Method: f.Method, Path: f.Path, ContentType: f.ContentType,
			field: f.Field, reason: f.Reason, Status: f.Status,
		}
	}
}

// StatusError is an answer that is not a success, detection probes included: any status
// but 200 to a GET, any non-2xx to a command.
//
// Attributed is set only on an answer to a read or a command. An answer without the
// header can still come from the server: Sonarr 4, Sonarr 5 before 5.0.0.2701 and Radarr
// send their exception-built errors without it (Sonarr#9161), so false means unknown,
// never "a proxy".
type StatusError struct {
	Message    string // the server's one-line message, key redacted (error-model "message", else ProblemDetails "title"); "" when none
	Body       string // the capture: key redacted, sanitized keeping CR and LF (safe for a sink that escapes them), at most 64 KiB, ending in "..." when cut
	Path       string
	Code       int
	retryAfter time.Duration
	Attributed bool // the answer carried the X-Application-Version of the detection the request was sent on; false for a detection probe's answer and for the Status inside a *ServerChangedError
}

var (
	_ httpclient.Transient      = (*StatusError)(nil)
	_ httpclient.RetryAfterHint = (*StatusError)(nil)
)

// Error renders "arrapi: <path>: HTTP <code>", followed by Message, else Body, when set,
// on one line: each rune unsafe in a single-line sink, CR and LF included, becomes a
// space. Body itself keeps its line breaks.
func (e *StatusError) Error() string {
	detail := e.Message
	if detail == "" {
		detail = e.Body
	}
	path := textsafe.SanitizeSingleLine(e.Path)
	if detail == "" {
		return fmt.Sprintf("arrapi: %s: HTTP %d", path, e.Code)
	}
	return fmt.Sprintf("arrapi: %s: HTTP %d: %s", path, e.Code, textsafe.SanitizeSingleLine(detail))
}

// IsTransient reports whether the status is worth sending again, by
// httpclient.IsRetryableStatus: 408, 429, and 500 through 599 except 501, 505, 506,
// 508, 510 and 511, which repeat on a retry. 600 and above are not.
func (e *StatusError) IsTransient() bool {
	return httpclient.IsRetryableStatus(e.Code)
}

// RetryAfterHint is the answer's capped Retry-After; 0 when it carried none.
func (e *StatusError) RetryAfterHint() time.Duration { return e.retryAfter }

// GrabReason is why an arr refused a grab, read from its status and its own message.
type GrabReason int

// The reasons an arr gives for a refused grab.
const (
	GrabNotCached   GrabReason = iota + 1 // 404: the release left the arr's 30-minute search cache; search again
	GrabUnmapped                          // 404: the arr could not map the release onto a series and episodes, or a movie
	GrabUnavailable                       // 409: the arr could not get the release from the indexer
)

// GrabError is a grab the arr refused for a reason it explained. Err is the refusal,
// with its code and the arr's message, so errors.As reaches *StatusError as for any
// other refusal. Only an answer whose code and message are the arr's own sentences for
// the reason is a GrabError; any other refused grab is a plain *StatusError.
type GrabError struct {
	Err    *StatusError
	Reason GrabReason
}

// Error is "arrapi: grab refused (<reason words>): " followed by Err's text; the words
// are "refused" for a Reason outside the three, and the text is "arrapi: grab refused"
// alone when Err is nil.
func (e *GrabError) Error() string {
	if e.Err == nil {
		return "arrapi: grab refused"
	}
	return "arrapi: grab refused (" + e.Reason.words() + "): " + e.Err.Error()
}

// Unwrap returns Err, or nil when Err is nil.
func (e *GrabError) Unwrap() error {
	if e.Err == nil {
		return nil
	}
	return e.Err
}

func (r GrabReason) words() string {
	switch r {
	case GrabNotCached:
		return "not in the search cache"
	case GrabUnmapped:
		return "not mapped"
	case GrabUnavailable:
		return "unavailable from the indexer"
	}
	return "refused"
}

// ResponseTooLargeError is an answer whose body exceeds the cap for its path. The body
// is rejected, never decoded cut.
type ResponseTooLargeError struct {
	Path  string
	Limit int64
}

func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("arrapi: %s: response exceeds %d-byte limit", e.Path, e.Limit)
}

// DecodeError is an answer arrapi accepted whose body did not decode into the type the
// call reads or, for a file read, an answer that is empty or not the media type the
// call reads. Its own fields and its Error text hold no byte of the body: Error renders
// arrapi's own sentence, naming the path, a field the type declares and the Go type.
// Err is the one exception: when it is a *json.SyntaxError, its text quotes the one
// invalid character or escape sequence, at most 12 bytes, escaped by Go's quoting.
type DecodeError struct {
	Err         error  // the decoder's error with server values removed (see Unwrap); nil when arrapi's own null check refused a decoded value, or when no scrubbed form exists
	Method      string // the request method; "GET" on every DecodeError arrapi returns; a logged one from a command's answer says "POST"
	Path        string // the request path relative to the base URL, without its query, as StatusError.Path carries it
	ContentType string // the answer's Content-Type media type as sent: parameters dropped, key redacted, single-line, at most 64 bytes; "" when absent
	field       string
	reason      string
	Status      int // the answer's HTTP status; 200 for every DecodeError arrapi returns, since a read or probe accepts only 200
}

// Error renders "arrapi: decode <path>: [field <field>: ]<reason>" on one line, with
// "the answer does not decode" as the reason of a value built outside arrapi. It
// never renders Err.
func (e *DecodeError) Error() string {
	reason := e.reason
	if reason == "" {
		reason = "the answer does not decode"
	}
	path := textsafe.SanitizeSingleLine(e.Path)
	if e.field != "" {
		return "arrapi: decode " + path + ": field " + e.field + ": " + reason
	}
	return "arrapi: decode " + path + ": " + reason
}

// Unwrap returns Err: a *json.SyntaxError as the decoder returned it, a
// *json.UnmarshalTypeError rebuilt from its kind word, the declared field path and
// the Go types, or nil. Err is nil whenever its text would render the client's key.
func (e *DecodeError) Unwrap() error { return e.Err }

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
	Err        error    // ReasonNotAPI: the status answer's *DecodeError, or its *ResponseTooLargeError when the body was over the cap; nil otherwise
	App        string   // appName the server reported; "" when unknown
	Version    string   // its version; "" when unknown
	Current    string   // /api "current"; "" when /api was not readable
	Want       string   // "Sonarr" or "Radarr"
	Path       string   // the request path that answered, relative to the base URL
	Deprecated []string // /api "deprecated": its well-formed tokens ("v" and 1 to 3 digits), at most 8
	Reason     UnsupportedReason
}

// Unwrap returns Err.
func (e *UnsupportedAPIError) Unwrap() error { return e.Err }

// Error renders one line whatever the fields hold: server text is sanitized and capped
// again, because a value built outside arrapi carries no such guarantee. It cannot
// redact a key, which it never holds.
func (e *UnsupportedAPIError) Error() string {
	want, path := textsafe.SanitizeSingleLine(e.Want), textsafe.SanitizeSingleLine(e.Path)
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
	origin, _ := textsafe.SanitizeSingleLineCapped(e.Origin, maxOriginBytes, "...")
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

// CheckReason says what a failed connection check found to fix.
type CheckReason int

// The reasons a connection check fails.
const (
	CheckKey         CheckReason = iota + 1 // the key is refused: a 401 or 403, or a key New refuses
	CheckBasePath                           // nothing answers at the URL's path: the last status path detection tries answered 404
	CheckURL                                // the URL is wrong or unreachable: New refuses it; a dial, DNS, TLS or redirect failure, a sign-in portal on another origin included, or the 5 s bound, in the client's own words; or a 400, which a TLS port sends to a plain http:// request
	CheckWrongApp                           // the URL answers as another application
	CheckUnsupported                        // the application answers, but arrapi cannot read it: its version, its API generation, or a reverse proxy hiding /api/v5 or X-Application-Version
	CheckNotArrAPI                          // something answered 200 that is not the Sonarr or Radarr API: a web page (fix the base path, or what answers at the URL), a sign-in portal on the same origin, another JSON API; no *StatusError, so no status to show
	CheckServer                             // the server or a proxy in front of it failed: any other status but 400 (its *StatusError), a JSON status answer cut or malformed (its *DecodeError), a server version change during the check
)

// CheckError is a failed connection check: Reason says what to fix, Err is the
// failure the check met, which errors.Is and errors.As reach.
type CheckError struct {
	Err    error
	Reason CheckReason
}

// Error renders one line: the *DecodeError's text when Reason is CheckServer and Err's
// chain holds a *DecodeError (a status answer whose JSON is cut or malformed), Err's
// text otherwise, and "arrapi: connection check failed" when Err is nil.
func (e *CheckError) Error() string {
	if e.Err == nil {
		return "arrapi: connection check failed"
	}
	text := e.Err.Error()
	// The ReasonNotAPI text wrapping a cut answer names the base path, the wrong fix.
	if de, ok := errors.AsType[*DecodeError](e.Err); ok && e.Reason == CheckServer {
		text = de.Error()
	}
	return textsafe.SanitizeSingleLine(text)
}

// Unwrap returns Err.
func (e *CheckError) Unwrap() error { return e.Err }

const (
	// maxServerTextBytes matches the bound internal/transport owns (ServerTextBytes).
	maxServerTextBytes = 64
	maxOriginBytes     = 255
)

func serverText(s string) string {
	text, _ := textsafe.SanitizeSingleLineCapped(s, maxServerTextBytes, "...")
	return text
}
