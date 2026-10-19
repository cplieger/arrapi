package transport_test

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"

	"github.com/cplieger/arrapi/v3/internal/transport"
	"github.com/cplieger/runesafe/v3"
)

func TestTransportErrorCarriesNoURL(t *testing.T) {
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL+"/sonarr", transport.Config{})
	d := detect(t, c, f, v4)
	f.srv.Close()
	var out any
	res := c.GetJSON(t.Context(), d, "/api/v3/series?seriesId=1", transport.MaxListBytes, &out)
	if res.Err == nil {
		t.Fatal("a closed server answered")
	}
	if want := "arrapi: request /api/v3/series: "; !strings.HasPrefix(res.Err.Error(), want) {
		t.Errorf("Error() = %q, want it to start %q", res.Err.Error(), want)
	}
	assertChainClean(t, res.Err, "http://", "/sonarr", "seriesId")
	if ue, ok := errors.AsType[*url.Error](res.Err); ok {
		t.Errorf("a *url.Error survived: %v", ue)
	}
	if !errors.Is(res.Err, syscall.ECONNREFUSED) {
		t.Errorf("Err = %v, want the dial cause kept in the chain", res.Err)
	}
	if res.Verdict != transport.VerdictTransport || res.Next != transport.NextRetry {
		t.Errorf("result = (%v, %v), want a transient VerdictTransport with NextRetry", res.Verdict, res.Next)
	}
}

// diagnostics is a caller transport's error: a fixed text over branches, one of
// which keeps the request's key header.
type diagnostics struct{ errs []error }

func (diagnostics) Error() string     { return "request failed" }
func (d diagnostics) Unwrap() []error { return d.errs }

func TestCallerTransportErrorTreeCarriesNoKey(t *testing.T) {
	refused := errors.New("connection refused")
	hc := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, diagnostics{errs: []error{refused, errors.New("sent X-Api-Key " + req.Header.Get("X-Api-Key"))}}
	})}
	c := newClient(t, "http://sonarr:8989", transport.Config{HTTPClient: hc})
	var out any
	res := c.GetJSON(t.Context(), &transport.Detection{}, "/api/v3/series", transport.MaxListBytes, &out)
	if res.Err == nil {
		t.Fatal("a failing transport returned no error")
	}
	assertChainClean(t, res.Err, string(testKey))
	if res.Verdict != transport.VerdictTransport {
		t.Errorf("verdict = %v, want VerdictTransport", res.Verdict)
	}
}

// TestTransportErrorRendersOneLine: an error that leaves transport is one line
// (docs/request-behavior.md "Errors"), whatever text its cause quotes, each unsafe rune
// a space (runesafe.SanitizeSingleLine's documented policy), and its cause stays
// reachable for errors.Is.
func TestTransportErrorRendersOneLine(t *testing.T) {
	forged := errors.New("lookup evil\r\nlevel=INFO msg=forged\x1b[2J")
	const shown = "lookup evil  level=INFO msg=forged [2J"
	f := newFake(t, nil)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	tests := []struct {
		name  string
		path  string
		trip  roundTripFunc
		cause error
		want  string
	}{
		{
			name: "caller transport error",
			path: "/api/v3/series",
			trip: func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/v3/series" {
					return tr.RoundTrip(r)
				}
				return nil, forged
			},
			want: "arrapi: request /api/v3/series: " + shown, cause: forged,
		},
		{
			name: "body read error",
			path: "/api/v3/series",
			trip: func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/v3/series" {
					return tr.RoundTrip(r)
				}
				h := http.Header{"X-Application-Version": {v4}}
				return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(iotest.ErrReader(forged)), Request: r}, nil
			},
			want: "arrapi: request /api/v3/series: " + shown, cause: forged,
		},
		{
			name: "unbuildable request",
			path: "/api/v3/x\x7f\r\ny",
			trip: func(r *http.Request) (*http.Response, error) { return tr.RoundTrip(r) },
			want: "arrapi: build request /api/v3/x   y: ",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient(t, f.srv.URL, transport.Config{HTTPClient: &http.Client{Transport: tc.trip}, MaxAttempts: 1})
			d := detect(t, c, f, v4)
			var out any
			res := c.GetJSON(t.Context(), d, tc.path, transport.MaxListBytes, &out)
			if res.Err == nil {
				t.Fatal("the request returned no error")
			}
			got := res.Err.Error()
			for _, r := range got {
				if runesafe.IsUnsafeSingleLine(r) {
					t.Errorf("Error() = %q holds unsafe rune %U", got, r)
				}
			}
			if !strings.HasPrefix(got, tc.want) {
				t.Errorf("Error() = %q, want it to start %q", got, tc.want)
			}
			if tc.cause != nil && !errors.Is(res.Err, tc.cause) {
				t.Errorf("Err = %v, want the cause kept in the chain", res.Err)
			}
		})
	}
}

func TestBuildRequestErrorCarriesNoURL(t *testing.T) {
	const password = "pw-9c1e" // gitleaks:allow (fake password, redaction test fixture)
	for name, path := range map[string]string{"invalid escape": "/api/v3/x%zz", "control byte": "/api/v3/x\x7f"} {
		t.Run(name, func(t *testing.T) {
			c := newClient(t, "http://user:"+password+"@sonarr:8989/sonarr", transport.Config{})
			var out any
			res := c.GetJSON(t.Context(), &transport.Detection{}, path, transport.MaxListBytes, &out)
			if res.Err == nil {
				t.Fatal("an unbuildable request returned no error")
			}
			assertChainClean(t, res.Err, password, "http://", "sonarr:8989")
			if ue, ok := errors.AsType[*url.Error](res.Err); ok {
				t.Errorf("a *url.Error survived: %v", ue)
			}
			if res.Verdict != transport.VerdictTransport || res.Next != transport.NextDone || res.Sent {
				t.Errorf("result = (%v, %v, sent %v), want VerdictTransport, NextDone, not sent", res.Verdict, res.Next, res.Sent)
			}
		})
	}
}
