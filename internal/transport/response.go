package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/statushook"
	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/runesafe/v3"
)

// The body bounds. Every body is read through one of them or discardBody's.
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
func (c *Client) capture(code int, resp *http.Response, path string) *arrapi.StatusError {
	e := &arrapi.StatusError{Code: code, Path: path}
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
	statushook.SetRetryAfter(e, httpx.ParseRetryAfter(resp.Header.Get("Retry-After")))
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
	s = c.purge(c.mask(runesafe.Sanitize(raw)))
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

// readInto reads at most limit bytes of an accepted body and decodes it into out.
// An over-cap body is rejected, never decoded cut. A failed read is returned as the
// raw error for the caller to judge as a transport outcome.
func (*Client) readInto(body io.ReadCloser, limit int64, path string, out any) (readErr, err error) {
	data, rerr := httpx.ReadLimitedBody(body, limit)
	if rerr != nil {
		if _, ok := errors.AsType[*httpx.ResponseTooLargeError](rerr); ok {
			return nil, &arrapi.ResponseTooLargeError{Path: path, Limit: limit}
		}
		return rerr, nil
	}
	// encoding/json sets a slice nil on null and leaves a struct as it was, so null
	// would pass as an empty answer; no arr route answers 200 with it (ASP.NET Core
	// answers a null result 204).
	if string(bytes.Trim(data, " \t\r\n")) == "null" {
		return nil, &decodeError{path: path, reason: "the answer is null"}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return nil, newDecodeError(path, reflect.TypeOf(out), err)
	}
	if field, i, found := nullItem(data, reflect.TypeOf(out)); found {
		return nil, &decodeError{path: path, field: field, reason: "item " + strconv.Itoa(i) + " is null"}
	}
	return nil, nil
}

// decodeError is an accepted body that failed to decode. It holds only text arrapi
// defines and wraps nothing: a decoder's error can quote any part of the value it
// rejected (time.ParseError quotes the unparsed suffix, UnmarshalTypeError a number
// literal, an UnmarshalJSON method anything), and a server can echo the key there.
type decodeError struct {
	path   string
	field  string
	reason string
}

func (e *decodeError) Error() string {
	if e.field != "" {
		return "arrapi: decode " + e.path + ": field " + e.field + ": " + e.reason
	}
	return "arrapi: decode " + e.path + ": " + e.reason
}

func newDecodeError(path string, out reflect.Type, err error) error {
	if se, ok := errors.AsType[*json.SyntaxError](err); ok {
		return &decodeError{path: path, reason: "invalid JSON at byte " + strconv.FormatInt(se.Offset, 10)}
	}
	if te, ok := errors.AsType[*json.UnmarshalTypeError](err); ok && te.Type != nil {
		return &decodeError{
			path:   path,
			field:  fieldPath(te.Field, declaredNames(out)),
			reason: "a JSON " + jsonKind(te.Value) + " cannot decode into " + te.Type.String(),
		}
	}
	if _, ok := errors.AsType[*time.ParseError](err); ok {
		return &decodeError{path: path, reason: "a date-time is not RFC 3339"}
	}
	return &decodeError{path: path, reason: "the answer does not match the expected shape"}
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
