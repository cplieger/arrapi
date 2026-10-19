package transport

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/httpx/v5"
)

// refuseEveryHop makes the default policy refuse every hop with an untyped error, so
// the wrapper's text and its %w are judged apart from httpx's reasons.
func refuseEveryHop(t *testing.T) error {
	t.Helper()
	refusal := errors.New("refused by the stub policy")
	saved := defaultRedirectPolicy
	defaultRedirectPolicy = func(*http.Request, []*http.Request) error { return refusal }
	t.Cleanup(func() { defaultRedirectPolicy = saved })
	return refusal
}

func TestRefusedUpgradeNamesTheHTTPSURL(t *testing.T) {
	tests := []struct {
		name     string
		basePath string
		location string
		want     string
	}{
		{"root base", "", "https://127.0.0.1:8443/api/v3/series", "change the url to https://127.0.0.1:8443: "},
		{"base path kept", "/sonarr", "https://127.0.0.1:8443/sonarr/api/v3/series", "change the url to https://127.0.0.1:8443/sonarr: "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			refusal := refuseEveryHop(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Application-Version", iv4)
				if r.URL.Path == tc.basePath+"/api" {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				http.Redirect(w, r, tc.location, http.StatusMovedPermanently)
			}))
			t.Cleanup(srv.Close)
			c, err := New(srv.URL+tc.basePath+"/", "test-key", Config{})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			t.Cleanup(c.Close)
			r := NewProbeRun()
			var out any
			c.Probe(t.Context(), r, "/api", &out)
			epoch, _ := r.Anchor()
			d := &Detection{Identity: Identity{App: "Sonarr", AppVersion: iv4}, Epoch: epoch}
			res := c.GetJSON(t.Context(), d, "/api/v3/series", MaxListBytes, &out)
			if res.Err == nil {
				t.Fatal("the upgrade was followed")
			}
			if !strings.HasPrefix(res.Err.Error(), "arrapi: /api/v3/series redirects to https://127.0.0.1:8443: ") || !strings.Contains(res.Err.Error(), tc.want) {
				t.Errorf("Error() = %q, want the HTTPS fix %q", res.Err.Error(), tc.want)
			}
			if !errors.Is(res.Err, refusal) {
				t.Errorf("Err = %v, want the policy's refusal reachable through errors.Is", res.Err)
			}
		})
	}
}

func TestRefusalThatIsNotAnUpgradeNamesTheTarget(t *testing.T) {
	refusal := refuseEveryHop(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Application-Version", iv4)
		if r.URL.Path == "/api" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.Redirect(w, r, "/elsewhere?apikey=x", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "test-key", Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	r := NewProbeRun()
	var out any
	c.Probe(t.Context(), r, "/api", &out)
	epoch, _ := r.Anchor()
	d := &Detection{Identity: Identity{App: "Sonarr", AppVersion: iv4}, Epoch: epoch}
	res := c.GetJSON(t.Context(), d, "/api/v3/series", MaxListBytes, &out)
	want := "arrapi: /api/v3/series redirect to " + srv.URL + " refused: refused by the stub policy"
	if res.Err == nil || res.Err.Error() != want || !errors.Is(res.Err, refusal) {
		t.Errorf("Err = %v, want %q wrapping the policy's refusal", res.Err, want)
	}
}

func TestPolicyUseLastResponseIsTheAnswer(t *testing.T) {
	saved := defaultRedirectPolicy
	defaultRedirectPolicy = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	t.Cleanup(func() { defaultRedirectPolicy = saved })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Application-Version", iv4)
		if r.URL.Path == "/api" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "test-key", Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	r := NewProbeRun()
	var out any
	c.Probe(t.Context(), r, "/api", &out)
	epoch, _ := r.Anchor()
	d := &Detection{Identity: Identity{App: "Sonarr", AppVersion: iv4}, Epoch: epoch}
	res := c.GetJSON(t.Context(), d, "/api/v3/series", MaxListBytes, &out)
	se, ok := errors.AsType[*arrapi.StatusError](res.Err)
	if res.Verdict != VerdictStatus || !ok || se.Code != http.StatusFound {
		t.Errorf("result = (%v, %v), want the 302 itself as a *StatusError", res.Verdict, res.Err)
	}
}

// TestDefaultClientRefusesDowngrade is the converse pin of the exact-origin rule, true
// of the same-host policy as well (httpx names it RedirectDowngrade there and
// RedirectOtherOrigin under WithSameOrigin): an https base redirected to http on its
// own host is refused before the http origin is dialled. It is internal so the default
// client can trust the test server's certificate.
func TestDefaultClientRefusesDowngrade(t *testing.T) {
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { plainHits.Add(1) }))
	t.Cleanup(plain.Close)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Application-Version", iv4)
		if r.URL.Path == "/api" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.Redirect(w, r, plain.URL+"/api/v3/series", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "test-key", Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	c.data.Transport.(*observer).next.(*http.Transport).TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	r := NewProbeRun()
	var out any
	if _, res := c.Probe(t.Context(), r, "/api", &out); res.Err != nil {
		t.Fatalf("probe over https: %v", res.Err)
	}
	epoch, _ := r.Anchor()
	d := &Detection{Identity: Identity{App: "Sonarr", AppVersion: iv4}, Epoch: epoch}
	res := c.GetJSON(t.Context(), d, "/api/v3/series", MaxListBytes, &out)
	if res.Err == nil {
		t.Fatal("the downgrade was followed")
	}
	if _, ok := errors.AsType[*httpx.RedirectRefusedError](res.Err); !ok {
		t.Errorf("Err = %v, want httpx's refusal reachable through errors.As", res.Err)
	}
	if n := plainHits.Load(); n != 0 {
		t.Errorf("the http origin received %d requests, want none", n)
	}
}
