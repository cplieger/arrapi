package transport_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
	"github.com/cplieger/httpx/v5"
)

func TestStatusErrorCarriesRetryAfter(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"seconds", "30", 30 * time.Second},
		{"capped", "86400", httpx.RetryAfterCap},
		{"absent", "", 0},
		{"malformed", "soon", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Application-Version", v4)
				if tc.value != "" {
					w.Header().Set("Retry-After", tc.value)
				}
				w.WriteHeader(http.StatusTooManyRequests)
			})
			var out any
			res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
			se, ok := errors.AsType[*arrapi.StatusError](res.Err)
			if !ok || se.Code != 429 {
				t.Fatalf("Err = %v, want *StatusError 429", res.Err)
			}
			if got := se.RetryAfterHint(); got != tc.want {
				t.Errorf("RetryAfterHint() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStatusErrorRedactsTheKeyOverTheWire(t *testing.T) {
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(reply(401, v4, `{"message":"key `+string(testKey)+` is not recognized"}`))
	var out any
	res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
	se, ok := errors.AsType[*arrapi.StatusError](res.Err)
	if !ok {
		t.Fatalf("Err = %v, want *StatusError", res.Err)
	}
	for what, s := range map[string]string{"Body": se.Body, "Message": se.Message, "Error()": se.Error()} {
		assertNoNeedle(t, what, s, string(testKey))
	}
	if se.Message != "key [redacted] is not recognized" {
		t.Errorf("Message = %q", se.Message)
	}
}
