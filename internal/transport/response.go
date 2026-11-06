package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/decodehook"
	"github.com/cplieger/arrapi/v3/internal/statushook"
	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/go/textsafe"
)

// The body bounds. Every body is read through one of them, a file read's caller-given
// limit, or discardBody's.
const (
	MaxListBytes      = 64 << 20 // a list answer
	MaxObjectBytes    = 1 << 20  // a single object, and every detection probe
	maxErrorBodyBytes = 64 << 10 // the capture of an answer that is not a success
	maxMessageBytes   = 512
)

// ServerTextBytes bounds every presented copy of a server string.
const ServerTextBytes = 64

// capture reads a failure's body into the *arrapi.StatusError the caller receives.
// resp nil is a redirect net/http discarded, which leaves no body.
func (c *Client) capture(code int, resp *http.Response, path string, attributed bool) *arrapi.StatusError {
	e := &arrapi.StatusError{Code: code, Path: path, Attributed: attributed}
	if resp == nil {
		return e
	}
	if raw, lost, err := c.readErrorWindow(resp.Body); err == nil {
		body, cut := c.redactErrorBody(raw, lost)
		e.Body = body
		if !cut {
			e.Message = c.message(raw)
		}
	}
	statushook.SetRetryAfter(e, httpclient.ParseRetryAfter(resp.Header.Get("Retry-After")))
	return e
}

// readErrorWindow reads an error body, closing it, through a window longer than the
// cap by the key's length, so a key straddling the cap is still read whole; lost
// reports that bytes past the window were dropped. The text is raw, never presented.
func (c *Client) readErrorWindow(body io.ReadCloser) (raw string, lost bool, err error) {
	defer body.Close()
	window := maxErrorBodyBytes + len(c.key)
	data, err := io.ReadAll(io.LimitReader(body, int64(window)+1))
	if err != nil {
		return "", false, err
	}
	lost = len(data) > window
	if lost {
		data = data[:window]
	}
	return strings.TrimSpace(string(data)), lost, nil
}

// redactErrorBody sanitizes, redacts, then caps raw error text, keeping its line
// breaks; the cap comes last, so a key straddling it is already gone, and a key the
// read window cut is a trailing prefix cutMarked trims. cut reports that the result is
// not the whole text.
func (c *Client) redactErrorBody(raw string, lost bool) (s string, cut bool) {
	s = c.purge(c.mask(textsafe.Sanitize(raw)))
	if lost || len(s) > maxErrorBodyBytes {
		return c.cutMarked(s, maxErrorBodyBytes), true
	}
	return s, false
}

// ErrorDoc is every member of an error answer arrapi reads: the Sonarr error model's
// message and ProblemDetails' title.
type ErrorDoc struct {
	Message json.RawMessage `json:"message"`
	Title   json.RawMessage `json:"title"`
}

// message is the server's one-line message from the raw, uncut error text: its
// message, else its title, presented.
func (c *Client) message(raw string) string {
	var doc ErrorDoc
	if json.Unmarshal([]byte(raw), &doc) != nil {
		return ""
	}
	for _, field := range []json.RawMessage{doc.Message, doc.Title} {
		var text string
		if json.Unmarshal(field, &text) == nil && strings.TrimSpace(text) != "" {
			return c.Present(text, maxMessageBytes)
		}
	}
	return ""
}

// readInto reads at most a.limit bytes of the accepted answer resp and decodes it into
// a.out. An over-cap body is rejected, never decoded cut. A failed read is returned as
// the raw error for the caller to judge as a transport outcome.
func (c *Client) readInto(a *attempt, resp *http.Response) (readErr, err error) {
	if a.media != "" {
		return c.readFile(a, resp)
	}
	data, readErr, err := readLimited(a, resp)
	if readErr != nil || err != nil {
		return readErr, err
	}
	// encoding/json sets a slice nil on null and leaves a struct as it was, so null
	// would pass as an empty answer; no arr route answers 200 with it (ASP.NET Core
	// answers a null result 204).
	if string(bytes.Trim(data, " \t\r\n")) == "null" {
		return nil, c.decodeFailure(a, resp, decodeFault{reason: "the answer is null"})
	}
	out := reflect.TypeOf(a.out)
	if err := json.Unmarshal(data, a.out); err != nil {
		return nil, c.decodeFailure(a, resp, newDecodeError(out, err))
	}
	if field, i, found := nullItem(data, out); found {
		return nil, c.decodeFailure(a, resp, decodeFault{field: field, reason: "item " + strconv.Itoa(i) + " is null"})
	}
	if i, found := nullListItem(a.out); found {
		return nil, c.decodeFailure(a, resp, decodeFault{reason: "list item " + strconv.Itoa(i) + " is null"})
	}
	return nil, nil
}

// readLimited reads at most a.limit bytes of resp's body, mapping httpclient's
// over-cap error to arrapi's and returning any other read error as readErr.
func readLimited(a *attempt, resp *http.Response) (data []byte, readErr, err error) {
	data, rerr := httpclient.ReadLimitedBody(resp.Body, a.limit)
	if rerr != nil {
		if _, ok := errors.AsType[*httpclient.ResponseTooLargeError](rerr); ok {
			return nil, nil, &arrapi.ResponseTooLargeError{Path: a.shown, Limit: a.limit}
		}
		return nil, rerr, nil
	}
	return data, nil, nil
}

// readFile reads a file answer into a.out, a *[]byte, only when its media type is
// a.media and it holds 1 to a.limit bytes; *a.out stays nil otherwise.
func (c *Client) readFile(a *attempt, resp *http.Response) (readErr, err error) {
	if mediaType, _, perr := mime.ParseMediaType(resp.Header.Get("Content-Type")); perr != nil || mediaType != a.media {
		discardBody(resp)
		return nil, c.decodeFailure(a, resp, decodeFault{reason: "the answer is not " + a.media})
	}
	if resp.ContentLength > a.limit {
		discardBody(resp)
		return nil, &arrapi.ResponseTooLargeError{Path: a.shown, Limit: a.limit}
	}
	data, readErr, err := readLimited(a, resp)
	if readErr != nil || err != nil {
		return readErr, err
	}
	// The route answers a missing or empty resized file with its full-size base after
	// checking only that the base exists, so a zero-length base is a 200
	// (MediaCoverController.cs in Sonarr 4, Sonarr 5 and Radarr).
	if len(data) == 0 {
		return nil, c.decodeFailure(a, resp, decodeFault{reason: "the answer is empty"})
	}
	out, _ := a.out.(*[]byte) // GetFile, the one attempt with a media type, sets a *[]byte
	*out = data
	return nil, nil
}

var errNoCommandID = errors.New("arrapi: the command answer holds no id of 1 or more")

// acceptCommand reads an accepted command's answer when a.out is a *CommandAnswer and
// discards it otherwise. The server already took the command, and a caller that sees
// an error may send it again, so no failure here reaches the Result: it zeroes the id
// and is logged once. Every logged cause renders no byte of the body.
func (c *Client) acceptCommand(a *attempt, resp *http.Response, path string) {
	answer, ok := a.out.(*CommandAnswer)
	if !ok {
		discardBody(resp)
		return
	}
	readErr, cause := c.readInto(a, resp)
	switch {
	case readErr != nil:
		cause = c.safeDoError(path, readErr)
	case cause == nil && answer.ID < 1:
		cause = errNoCommandID
	}
	if cause == nil {
		return
	}
	answer.ID = 0
	c.cfg.Logger.Warn("arrapi: command accepted; its answer was not read", "path", path, "err", cause)
}

// nullListItem finds the first nil item of a top-level list of pointers, GetList's
// shape, which a null item decodes to: no resource list holds one, and GetList's
// mapper would dereference it.
func nullListItem(out any) (index int, found bool) {
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return 0, false
	}
	list := v.Elem()
	if list.Kind() != reflect.Slice || list.Type().Elem().Kind() != reflect.Pointer {
		return 0, false
	}
	for i := range list.Len() {
		if list.Index(i).IsNil() {
			return i, true
		}
	}
	return 0, false
}

// decodeFault is what one decode site found: arrapi's sentence, the declared field it
// names, and the decoder's error already scrubbed by newDecodeError (nil when none).
type decodeFault struct {
	cause         error
	field, reason string
}

// decodeFailure builds the *arrapi.DecodeError of a's accepted answer resp. A cause
// whose text still renders the key is dropped: a hand-set key can be short enough to
// sit in a syntax error's fragment, or equal a declared name.
func (c *Client) decodeFailure(a *attempt, resp *http.Response, f decodeFault) error {
	cause := f.cause
	if c.treeRendersKey(cause) {
		cause = nil
	}
	mediaType, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";")
	return decodehook.New(decodehook.Fields{
		Err:         cause,
		Method:      a.method,
		Path:        a.shown,
		ContentType: c.Present(strings.Trim(mediaType, " \t"), ServerTextBytes),
		Field:       f.field,
		Reason:      f.reason,
		Status:      resp.StatusCode,
	})
}

// newDecodeError names the decoder's err in arrapi's words and keeps of it only what
// holds no server value: a decoder's error can quote any part of the value it rejected
// (time.ParseError the unparsed suffix, UnmarshalTypeError a number literal or a map
// key, an UnmarshalJSON method anything), and a server can echo the key there. A
// *json.SyntaxError is kept as it came, since its text quotes at most one escaped
// character or escape sequence; an UnmarshalTypeError is rebuilt from the kind word,
// declared names and Go types; no other error has a form without the value.
func newDecodeError(out reflect.Type, err error) decodeFault {
	if se, ok := errors.AsType[*json.SyntaxError](err); ok {
		return decodeFault{cause: se, reason: "invalid JSON at byte " + strconv.FormatInt(se.Offset, 10)}
	}
	if te, ok := errors.AsType[*json.UnmarshalTypeError](err); ok && te.Type != nil {
		field, kind := fieldPath(te.Field, declaredNames(out)), jsonKind(te.Value)
		cause := &json.UnmarshalTypeError{Value: kind, Type: te.Type, Offset: te.Offset}
		if field != "" {
			// A Struct with no Field renders as "Go struct field T. of type".
			cause.Struct, cause.Field = te.Struct, field
		}
		return decodeFault{
			cause:  cause,
			field:  field,
			reason: "a JSON " + kind + " cannot decode into " + te.Type.String(),
		}
	}
	if _, ok := errors.AsType[*time.ParseError](err); ok {
		return decodeFault{reason: "a date-time is not RFC 3339"}
	}
	return decodeFault{reason: "the answer does not match the expected shape"}
}

// jsonKind keeps only the kind word of UnmarshalTypeError.Value, which can go on to
// quote the literal ("number 2024...").
func jsonKind(value string) string {
	kind, _, _ := strings.Cut(value, " ")
	switch kind {
	case "string", "number", "bool", "array", "object":
		return kind
	}
	return "value"
}

// fieldPath renders an UnmarshalTypeError.Field only from names out's type declares.
// Go's default encoding/json is built on json/v2 (internal/buildcfg/exp.go JSONv2),
// whose Field is the input's JSON Pointer with "/" as "." (v2_inject.go), so list
// indices and map keys, an empty key included, reach it: an index or empty segment is
// dropped and any segment the type does not declare drops the path.
func fieldPath(field string, names map[string]bool) string {
	var kept []string
	for seg := range strings.SplitSeq(field, ".") {
		if seg == "" || strings.Trim(seg, "0123456789") == "" {
			continue
		}
		if !names[seg] {
			return ""
		}
		kept = append(kept, seg)
	}
	return strings.Join(kept, ".")
}

// declaredNames is every JSON field name, and every embedded field's Go name, of the
// struct types reachable from t, over the fields encoding/json decodes (exported or
// embedded).
func declaredNames(t reflect.Type) map[string]bool {
	names := map[string]bool{}
	addNames(t, names, map[reflect.Type]bool{})
	return names
}

func addNames(t reflect.Type, names map[string]bool, seen map[reflect.Type]bool) {
	for t != nil && (t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map) {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct || seen[t] {
		return
	}
	seen[t] = true
	for f := range t.Fields() {
		name, ok := jsonName(&f)
		if !ok {
			continue
		}
		names[name] = true
		if f.Anonymous {
			names[f.Name] = true
		}
		addNames(f.Type, names, seen)
	}
}

// jsonName is f's JSON name (its Go name when untagged), and false for a field
// encoding/json skips.
func jsonName(f *reflect.StructField) (string, bool) {
	if !f.IsExported() && !f.Anonymous {
		return "", false
	}
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	switch name {
	case "-":
		return "", false
	case "":
		return f.Name, true
	}
	return name, true
}
