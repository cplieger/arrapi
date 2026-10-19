package transport_test

import (
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3/internal/transport"
	"github.com/cplieger/arrapi/v3/sonarr"
	"github.com/cplieger/httpx/v5"
)

// crossHost returns f's URL under the name localhost: another origin, though the
// same server.
func crossHost(t *testing.T, f *fake) string {
	t.Helper()
	u := strings.Replace(f.srv.URL, "127.0.0.1", "localhost", 1)
	if u == f.srv.URL {
		t.Skipf("test server URL %q is not 127.0.0.1-based", f.srv.URL)
	}
	return u
}

func TestRedirectRefusalCarriesNoLocationPathOrQuery(t *testing.T) {
	other := newFake(t, reply(200, v4, `[]`))
	target := crossHost(t, other)
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(redirectTo(302, target+"/"+string(testKey)+"/private?apikey="+string(testKey), v4))
	var out any
	res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
	if res.Err == nil {
		t.Fatal("a redirect to another host was followed")
	}
	assertChainClean(t, res.Err, string(testKey), "/private", "apikey")
	if want := fmt.Sprintf("arrapi: /api/v3/series redirect to %s refused: ", target); !strings.HasPrefix(res.Err.Error(), want) {
		t.Errorf("Error() = %q, want it to start %q", res.Err.Error(), want)
	}
	if ue, ok := errors.AsType[*url.Error](res.Err); ok {
		t.Errorf("a *url.Error survived: %v", ue)
	}
	if refused := assertRefused(t, res.Err, httpx.RedirectOtherOrigin); refused.Origin != target {
		t.Errorf("Origin = %q, want %q", refused.Origin, target)
	}
	if res.Verdict != transport.VerdictTransport || res.Next != transport.NextDone {
		t.Errorf("result = (%v, %v), want row 13's refused redirect: VerdictTransport, NextDone", res.Verdict, res.Next)
	}
	if got := len(f.requests()); got != 1 {
		t.Errorf("origin saw %d requests, want 1", got)
	}
	if got := other.requests(); len(got) != 0 {
		t.Errorf("the other host saw %v", got)
	}
}

func TestRedirectToKeyBearingHostRedacted(t *testing.T) {
	other := newFake(t, reply(200, v4, `[]`))
	target := strings.Replace(other.srv.URL, "127.0.0.1", string(testKey)+".localhost", 1)
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(redirectTo(302, target+"/x", v4))
	var out any
	res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
	if res.Err == nil {
		t.Fatal("a redirect to another host was followed")
	}
	assertChainClean(t, res.Err, string(testKey))
	redacted := strings.Replace(other.srv.URL, "127.0.0.1", "[redacted].localhost", 1)
	if !strings.Contains(res.Err.Error(), "redirect to "+redacted+" refused") {
		t.Errorf("Error() = %q, want the key-bearing host redacted as %s", res.Err.Error(), redacted)
	}
	if refused := assertRefused(t, res.Err, httpx.RedirectOtherOrigin); refused.Origin != redacted {
		t.Errorf("Origin = %q, want the redacted origin %q", refused.Origin, redacted)
	}
}

// TestCallerPolicyRefusalToKeyBearingHostRedacted: a WithHTTPClient client under httpx's
// own policy gets the same typed, redacted refusal as the default client.
func TestCallerPolicyRefusalToKeyBearingHostRedacted(t *testing.T) {
	other := newFake(t, reply(200, v4, `[]`))
	target := strings.Replace(other.srv.URL, "127.0.0.1", string(testKey)+".localhost", 1)
	f := newFake(t, nil)
	hc := &http.Client{CheckRedirect: httpx.RedirectPolicyFunc(httpx.WithSameOrigin(true))}
	c := newClient(t, f.srv.URL, transport.Config{HTTPClient: hc})
	d := detect(t, c, f, v4)
	f.set(redirectTo(302, target+"/x", v4))
	var out any
	res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
	if res.Err == nil {
		t.Fatal("a redirect to another host was followed")
	}
	assertChainClean(t, res.Err, string(testKey))
	redacted := strings.Replace(other.srv.URL, "127.0.0.1", "[redacted].localhost", 1)
	if refused := assertRefused(t, res.Err, httpx.RedirectOtherOrigin); refused.Origin != redacted {
		t.Errorf("Origin = %q, want the redacted origin %q", refused.Origin, redacted)
	}
	if got := other.requests(); len(got) != 0 {
		t.Errorf("the other host saw %v", got)
	}
}

func TestCrossHostRedirectDoesNotForwardTheKey(t *testing.T) {
	other := newFake(t, reply(200, v4, `[]`))
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(redirectTo(302, crossHost(t, other)+"/api/v3/series", v4))
	var out any
	if res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out); res.Err == nil {
		t.Fatal("a redirect to another host was followed")
	}
	for _, k := range other.seenKeys() {
		if k != "" {
			t.Errorf("the other host received X-Api-Key %q", k)
		}
	}
}

func TestSameOriginRedirectFollowed(t *testing.T) {
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(routes(map[string]http.HandlerFunc{
		"/api/v3/series": redirectTo(302, "/redirected", v4),
		"/redirected":    reply(200, v4, `[{"title":"ok"}]`),
	}))
	var out []struct{ Title string }
	if res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out); res.Err != nil || len(out) != 1 || out[0].Title != "ok" {
		t.Errorf("same-origin redirect = %v (out %v), want it followed", res.Err, out)
	}
	if keys := f.seenKeys(); keys[len(keys)-1] != string(testKey) {
		got := keys[len(keys)-1]
		t.Errorf("the same-origin hop carried X-Api-Key %q, want the key", got)
	}
}

// assertRefused checks that err carries the redirect policy's typed refusal for reason.
func assertRefused(t *testing.T, err error, reason httpx.RedirectReason) *httpx.RedirectRefusedError {
	t.Helper()
	refused, ok := errors.AsType[*httpx.RedirectRefusedError](err)
	if !ok {
		t.Fatalf("Err = %v, want a *httpx.RedirectRefusedError reachable through errors.As", err)
	}
	if refused.Reason != reason {
		t.Errorf("Reason = %v, want %v", refused.Reason, reason)
	}
	return refused
}

// sonarr4 is an httptest Sonarr 4.0 under basePath: its detection answers, and every
// other path answered by route, or a 200 "[]" when route has no answer for it.
type sonarr4 struct {
	srv  *httptest.Server
	mu   sync.Mutex
	keys map[string]string // the X-Api-Key each path was last sent with
}

func newSonarr4(t *testing.T, basePath string, route func(w http.ResponseWriter, r *http.Request) bool) *sonarr4 {
	t.Helper()
	s := &sonarr4{keys: map[string]string{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.keys[r.URL.Path] = r.Header.Get("X-Api-Key")
		s.mu.Unlock()
		w.Header().Set("X-Application-Version", v4)
		switch r.URL.Path {
		case basePath + "/api":
			_, _ = w.Write([]byte(`{"current":"v3","deprecated":[]}`))
		case basePath + "/api/v3/system/status":
			_, _ = w.Write([]byte(`{"appName":"Sonarr","version":"` + v4 + `"}`))
		default:
			if route == nil || !route(w, r) {
				_, _ = w.Write([]byte(`[]`))
			}
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// key returns the X-Api-Key path was sent with, and whether it was requested.
func (s *sonarr4) key(path string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[path]
	return k, ok
}

// newSonarrClient is a sonarr client on the default HTTP client, which these tests pin.
func newSonarrClient(t *testing.T, base string) *sonarr.Client {
	t.Helper()
	c, err := sonarr.New(base, testKey, sonarr.WithBaseDelay(time.Millisecond), sonarr.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("sonarr.New: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// countingTLS is a TLS server that counts the connections it accepts: a hop the
// default client refuses never connects.
func countingTLS(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, &conns
}

// counting is a plain server that records the X-Api-Key of every request it receives.
func counting(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("X-Api-Key"))
		mu.Unlock()
		w.Header().Set("X-Application-Version", v4)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), keys...)
	}
}

func redirectSeries(location string) func(w http.ResponseWriter, r *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasSuffix(r.URL.Path, "/api/v3/series") {
			return false
		}
		w.Header().Set("Location", location)
		w.WriteHeader(http.StatusFound)
		return true
	}
}

// TestDefaultClientKeepsKeyOnOrigin pins keepsKeyOnOrigin's rule on the default client.
func TestDefaultClientKeepsKeyOnOrigin(t *testing.T) {
	keepsKeyOnOrigin(t, newSonarrClient)
}

// TestCallerClientKeepsKeyOnOrigin: a WithHTTPClient client gets the same exact-origin
// rule and hop cap whatever its own CheckRedirect allows, since net/http forwards
// X-Api-Key on every redirect it follows (a nil CheckRedirect follows 10 to any host).
func TestCallerClientKeepsKeyOnOrigin(t *testing.T) {
	for name, policy := range map[string]func(*http.Request, []*http.Request) error{
		"net/http's default policy":         nil,
		"a policy following every redirect": func(*http.Request, []*http.Request) error { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			keepsKeyOnOrigin(t, func(t *testing.T, base string) *sonarr.Client {
				t.Helper()
				c, err := sonarr.New(base, testKey, sonarr.WithHTTPClient(&http.Client{CheckRedirect: policy}),
					sonarr.WithBaseDelay(time.Millisecond), sonarr.WithLogger(slog.New(slog.DiscardHandler)))
				if err != nil {
					t.Fatalf("sonarr.New: %v", err)
				}
				t.Cleanup(c.Close)
				return c
			})
		})
	}
}

// TestCallerPolicyStillRefusesMore: a caller's CheckRedirect runs after arrapi's and
// can refuse a hop arrapi would follow; its error stays reachable.
func TestCallerPolicyStillRefusesMore(t *testing.T) {
	errCaller := errors.New("caller refuses every redirect")
	s := newSonarr4(t, "", func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v3/series" {
			return false
		}
		http.Redirect(w, r, "/moved/series", http.StatusFound)
		return true
	})
	var asked atomic.Int32
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { asked.Add(1); return errCaller }}
	c, err := sonarr.New(s.srv.URL, testKey, sonarr.WithHTTPClient(hc), sonarr.WithMaxAttempts(1), sonarr.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("sonarr.New: %v", err)
	}
	t.Cleanup(c.Close)
	if _, err := c.Series(t.Context()); !errors.Is(err, errCaller) {
		t.Errorf("Series = %v, want the caller's refusal", err)
	}
	if asked.Load() == 0 {
		t.Error("the caller's policy was never asked")
	}
	if _, ok := s.key("/moved/series"); ok {
		t.Error("the hop the caller refused was requested")
	}
}

// keepsKeyOnOrigin pins the redirect rule every read client gets, the exact origin of
// RFC 6454 (scheme, host, effective port), and its hop cap, httpx's default of 5
// followed with the 6th refused.
func keepsKeyOnOrigin(t *testing.T, newSonarrClient func(*testing.T, string) *sonarr.Client) {
	t.Helper()
	t.Run("another port on the same host is not followed", func(t *testing.T) {
		other, keys := counting(t)
		port := other.Listener.Addr().(*net.TCPAddr).Port
		s := newSonarr4(t, "", redirectSeries("http://127.0.0.1:"+strconv.Itoa(port)+"/api/v3/series"))
		if _, err := newSonarrClient(t, s.srv.URL).Series(t.Context()); err == nil {
			t.Error("the hop to another port was followed")
		}
		if got := keys(); len(got) != 0 {
			t.Errorf("the other port received %d requests, X-Api-Key %q", len(got), got)
		}
	})
	t.Run("another host is not followed", func(t *testing.T) {
		other, keys := counting(t)
		target := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
		s := newSonarr4(t, "", redirectSeries(target+"/api/v3/series"))
		if _, err := newSonarrClient(t, s.srv.URL).Series(t.Context()); err == nil {
			t.Error("the hop to another host was followed")
		}
		if got := keys(); len(got) != 0 {
			t.Errorf("the other host received %d requests, X-Api-Key %q", len(got), got)
		}
	})
	t.Run("http to https on the same host is not followed", func(t *testing.T) {
		tlsSrv, conns := countingTLS(t)
		s := newSonarr4(t, "", redirectSeries(tlsSrv.URL+"/api/v3/series"))
		if _, err := newSonarrClient(t, s.srv.URL).Series(t.Context()); err == nil {
			t.Error("the upgrade was followed")
		}
		if n := conns.Load(); n != 0 {
			t.Errorf("the https origin accepted %d connections, want none", n)
		}
	})
	t.Run("the same origin is followed with the key", func(t *testing.T) {
		s := newSonarr4(t, "", func(w http.ResponseWriter, r *http.Request) bool {
			switch r.URL.Path {
			case "/api/v3/series":
				http.Redirect(w, r, "/moved/series", http.StatusFound)
			case "/moved/series":
				_, _ = w.Write([]byte(`[{"id":7,"title":"Moved"}]`))
			default:
				return false
			}
			return true
		})
		series, err := newSonarrClient(t, s.srv.URL).Series(t.Context())
		if err != nil || len(series) != 1 || series[0].Title != "Moved" {
			t.Fatalf("Series = %v, %v; want the same-origin hop followed", series, err)
		}
		if k, ok := s.key("/moved/series"); !ok || k != string(testKey) {
			t.Errorf("the same-origin hop carried X-Api-Key %q (sent %v), want the key", k, ok)
		}
	})
	chain := func(redirects int) func(w http.ResponseWriter, r *http.Request) bool {
		return func(w http.ResponseWriter, r *http.Request) bool {
			hop := 0
			if r.URL.Path != "/api/v3/series" {
				n, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
				if err != nil {
					return false
				}
				hop = n
			}
			if hop < redirects {
				http.Redirect(w, r, "/hop/"+strconv.Itoa(hop+1), http.StatusFound)
				return true
			}
			_, _ = w.Write([]byte(`[]`))
			return true
		}
	}
	t.Run("5 same-origin redirects are followed with the key", func(t *testing.T) {
		s := newSonarr4(t, "", chain(5))
		if _, err := newSonarrClient(t, s.srv.URL).Series(t.Context()); err != nil {
			t.Fatalf("Series = %v, want 5 redirects followed", err)
		}
		for hop := 1; hop <= 5; hop++ {
			if k, ok := s.key("/hop/" + strconv.Itoa(hop)); !ok || k != string(testKey) {
				t.Errorf("hop %d carried X-Api-Key %q (sent %v), want the key", hop, k, ok)
			}
		}
	})
	t.Run("the 6th redirect is refused", func(t *testing.T) {
		s := newSonarr4(t, "", chain(6))
		_, err := newSonarrClient(t, s.srv.URL).Series(t.Context())
		if err == nil {
			t.Fatal("6 redirects were followed")
		}
		if !strings.Contains(err.Error(), "stopped after 5 redirects") || strings.HasPrefix(err.Error(), "arrapi: /api/v3/series redirect to") {
			t.Errorf("Error() = %q, want it to name the cap as stopped after 5 redirects", err.Error())
		}
		if _, ok := s.key("/hop/6"); ok {
			t.Error("the 6th redirect's target was requested")
		}
	})
}

// TestRefusedUpgradeNamesHTTPSFix pins the text of a refused http to https hop: it
// names the URL to configure, the target's port and the configured base path kept.
func TestRefusedUpgradeNamesHTTPSFix(t *testing.T) {
	tlsSrv, conns := countingTLS(t)
	tlsPort := strconv.Itoa(tlsSrv.Listener.Addr().(*net.TCPAddr).Port)
	tests := []struct {
		name     string
		basePath string
		location string
		want     string
	}{
		{"the default port", "", "https://127.0.0.1/api/v3/series", "change the url to https://127.0.0.1: "},
		{"a non-default port", "", "https://127.0.0.1:" + tlsPort + "/api/v3/series", "change the url to https://127.0.0.1:" + tlsPort + ": "},
		{"the base path kept", "/sonarr", "https://127.0.0.1:" + tlsPort + "/sonarr/api", "change the url to https://127.0.0.1:" + tlsPort + "/sonarr: "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newSonarr4(t, tc.basePath, redirectSeries(tc.location))
			_, err := newSonarrClient(t, s.srv.URL+tc.basePath).Series(t.Context())
			if err == nil {
				t.Fatal("the upgrade was followed")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Error() = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
	if n := conns.Load(); n != 0 {
		t.Errorf("the https origin accepted %d connections, want none", n)
	}
}

// TestDefaultClientRefusalCarriesNoLocationPathOrQuery is the black-box twin of
// TestRedirectRefusalCarriesNoLocationPathOrQuery through sonarr.New.
func TestDefaultClientRefusalCarriesNoLocationPathOrQuery(t *testing.T) {
	other, keys := counting(t)
	target := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	s := newSonarr4(t, "", redirectSeries(target+"/"+string(testKey)+"/private?apikey="+string(testKey)))
	_, err := newSonarrClient(t, s.srv.URL).Series(t.Context())
	if err == nil {
		t.Fatal("a redirect to another host was followed")
	}
	assertChainClean(t, err, string(testKey), "/private", "apikey")
	if !strings.Contains(err.Error(), target) {
		t.Errorf("Error() = %q, want it to name the refused origin %s", err.Error(), target)
	}
	if refused := assertRefused(t, err, httpx.RedirectOtherOrigin); refused.Origin != target {
		t.Errorf("Origin = %q, want %q", refused.Origin, target)
	}
	if got := keys(); len(got) != 0 {
		t.Errorf("the other host received %d requests", len(got))
	}
}

// TestHopCapNamedBeforeOrigin: the policy judges the count before the target
// (httpx's RedirectPolicyFunc), so a 6th redirect to another host, or one upgrading to
// https on the same host, is the cap.
func TestHopCapNamedBeforeOrigin(t *testing.T) {
	other, keys := counting(t)
	tlsSrv, conns := countingTLS(t)
	for name, sixth := range map[string]string{
		"another host":        strings.Replace(other.URL, "127.0.0.1", "localhost", 1),
		"an upgrade to https": tlsSrv.URL,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Application-Version", v4)
				hop, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
				if hop == 5 {
					http.Redirect(w, r, sixth+"/api/v3/series", http.StatusFound)
					return
				}
				http.Redirect(w, r, "/hop/"+strconv.Itoa(hop+1), http.StatusFound)
			})
			var out any
			res := c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &out)
			if res.Err == nil {
				t.Fatal("the 6th redirect was followed")
			}
			if want := "arrapi: /api/v3/series stopped after 5 redirects: "; !strings.HasPrefix(res.Err.Error(), want) {
				t.Errorf("Error() = %q, want it to start %q", res.Err.Error(), want)
			}
			assertRefused(t, res.Err, httpx.RedirectTooMany)
		})
	}
	if got := keys(); len(got) != 0 {
		t.Errorf("the other host received %d requests", len(got))
	}
	if n := conns.Load(); n != 0 {
		t.Errorf("the https origin accepted %d connections, want none", n)
	}
}
