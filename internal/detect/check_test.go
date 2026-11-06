package detect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"syscall"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// newRefusal returns the error transport.New gives for baseURL and key.
func newRefusal(t *testing.T, baseURL string, key arrapi.APIKey) error {
	t.Helper()
	c, err := transport.New(baseURL, key, transport.Config{})
	if err == nil {
		c.Close()
		t.Fatalf("transport.New(%q) accepted it", baseURL)
	}
	return err
}

// TestClassify pins the reason of every error detection or New can return, first
// matching row first: a *ServerChangedError before the *StatusError it wraps, a 400
// before any other status.
func TestClassify(t *testing.T) {
	notAPI := func(ct string, cause error) error {
		return &arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNotAPI, Err: &arrapi.DecodeError{ContentType: ct, Err: cause, Status: 200}}
	}
	syntax := &json.SyntaxError{Offset: 15}
	shape := &json.UnmarshalTypeError{Value: "array", Type: reflect.TypeFor[StatusDoc]()}
	status := func(code int) error { return &arrapi.StatusError{Code: code} }
	unsupported := func(r arrapi.UnsupportedReason) error { return &arrapi.UnsupportedAPIError{Reason: r} }
	tests := []struct {
		err  error
		name string
		want arrapi.CheckReason
	}{
		{newRefusal(t, "http://arr.test", ""), "empty key", arrapi.CheckKey},
		{newRefusal(t, "http://arr.test", "a+b"), "key outside the alphabet", arrapi.CheckKey},
		{newRefusal(t, "ftp://x", testKey), "not http", arrapi.CheckURL},
		{newRefusal(t, "http://x/?q", testKey), "a query", arrapi.CheckURL},
		{newRefusal(t, "http:///p", testKey), "no host", arrapi.CheckURL},
		{unsupported(arrapi.ReasonWrongApp), "another application", arrapi.CheckWrongApp},
		{notAPI("application/json", syntax), "cut JSON", arrapi.CheckServer},
		{notAPI("Application/JSON", syntax), "cut JSON, upper case", arrapi.CheckServer},
		{notAPI("application/problem+json", syntax), "cut problem JSON", arrapi.CheckServer},
		{notAPI("application/PROBLEM+JSON", syntax), "cut problem JSON, upper case", arrapi.CheckServer},
		{fmt.Errorf("wrapped: %w", notAPI("application/json", syntax)), "cut JSON, wrapped", arrapi.CheckServer},
		{notAPI("text/html", syntax), "cut HTML", arrapi.CheckNotArrAPI},
		{notAPI("", syntax), "cut, no content type", arrapi.CheckNotArrAPI},
		{notAPI("application/jsonp", syntax), "cut, a JSON-like type", arrapi.CheckNotArrAPI},
		{notAPI("application/json+xml", syntax), "cut, json not the suffix", arrapi.CheckNotArrAPI},
		{notAPI("+json", syntax), "cut, a bare suffix", arrapi.CheckNotArrAPI},
		{notAPI("application/json", shape), "JSON of another shape", arrapi.CheckNotArrAPI},
		{notAPI("application/json", nil), "a decode error without a cause", arrapi.CheckNotArrAPI},
		{&arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNotAPI}, "a status without appName", arrapi.CheckNotArrAPI},
		{&arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNotAPI, Err: &arrapi.ResponseTooLargeError{Limit: 1 << 20}}, "over the cap", arrapi.CheckNotArrAPI},
		{unsupported(arrapi.ReasonV5Unreachable), "v5 unreachable", arrapi.CheckUnsupported},
		{unsupported(arrapi.ReasonUnsupportedVersion), "unsupported version", arrapi.CheckUnsupported},
		{unsupported(arrapi.ReasonUnknownGeneration), "unknown generation", arrapi.CheckUnsupported},
		{unsupported(arrapi.ReasonNoVersionHeader), "no version header", arrapi.CheckUnsupported},
		{&arrapi.ServerChangedError{Status: &arrapi.StatusError{Code: 401}}, "server changed over a 401", arrapi.CheckServer},
		{&arrapi.ServerChangedError{Status: &arrapi.StatusError{Code: 404}}, "server changed over a 404", arrapi.CheckServer},
		{&arrapi.ServerChangedError{}, "server changed over a success", arrapi.CheckServer},
		{status(401), "401", arrapi.CheckKey},
		{status(403), "403", arrapi.CheckKey},
		{status(404), "404", arrapi.CheckBasePath},
		{fmt.Errorf("wrapped: %w", status(404)), "404, wrapped", arrapi.CheckBasePath},
		{status(400), "400", arrapi.CheckURL},
		{status(408), "408", arrapi.CheckServer},
		{status(429), "429", arrapi.CheckServer},
		{status(500), "500", arrapi.CheckServer},
		{status(501), "501", arrapi.CheckServer},
		{status(502), "502", arrapi.CheckServer},
		{status(302), "302", arrapi.CheckServer},
		{status(410), "410", arrapi.CheckServer},
		{&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "refused dial", arrapi.CheckURL},
		{&net.DNSError{Err: "no such host", Name: "arr.invalid", IsNotFound: true}, "unknown host", arrapi.CheckURL},
		{context.DeadlineExceeded, "the 5 s bound", arrapi.CheckURL},
		{errors.New("arrapi: request /api: connection reset"), "any other error", arrapi.CheckURL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.err); got != tc.want {
				t.Errorf("classify(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestCheckFailure(t *testing.T) {
	cause := &arrapi.StatusError{Code: 401}
	t.Run("a live context", func(t *testing.T) {
		err := CheckFailure(t.Context(), cause)
		ce, ok := errors.AsType[*arrapi.CheckError](err)
		if !ok || ce.Reason != arrapi.CheckKey || ce.Err != cause {
			t.Fatalf("CheckFailure = %#v, want a CheckKey *CheckError wrapping the cause", err)
		}
	})
	t.Run("an ended context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := CheckFailure(ctx, cause)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("CheckFailure = %v, want context.Canceled", err)
		}
		if _, ok := errors.AsType[*arrapi.CheckError](err); ok {
			t.Errorf("CheckFailure = %v, want no *CheckError", err)
		}
	})
}
