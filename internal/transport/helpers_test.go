package transport_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// testKey is a low-entropy placeholder, not a credential.
const testKey arrapi.APIKey = "test-key-7f3a"

// fake is an httptest server whose handler a test swaps, recording every request as
// "METHOD path?query".
type fake struct {
	srv  *httptest.Server
	h    http.HandlerFunc
	log  []string
	keys []string
	mu   sync.Mutex
}

func newFake(t *testing.T, h http.HandlerFunc) *fake {
	t.Helper()
	f := &fake{h: h}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.log = append(f.log, r.Method+" "+r.URL.RequestURI())
		f.keys = append(f.keys, r.Header.Get("X-Api-Key"))
		h := f.h
		f.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) set(h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.h = h
}

func (f *fake) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

func (f *fake) seenKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.keys)
}

func (f *fake) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = nil
}

// reply answers with code, the version header when v is set, and body.
func reply(code int, v, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if v != "" {
			w.Header().Set("X-Application-Version", v)
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}
}

func newClient(t *testing.T, url string, cfg transport.Config) *transport.Client {
	t.Helper()
	if cfg.BaseDelay == 0 {
		cfg.BaseDelay = time.Millisecond
	}
	c, err := transport.New(url, testKey, cfg)
	if err != nil {
		t.Fatalf("transport.New: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// detect runs a one-probe detection against f answering with version v and installs
// it, as internal/detect would.
func detect(t *testing.T, c *transport.Client, f *fake, v string) *transport.Detection {
	t.Helper()
	f.set(reply(200, v, `{"appName":"Sonarr","version":"`+v+`"}`))
	r := transport.NewProbeRun()
	var status struct{ AppName string }
	meta, res := c.Probe(t.Context(), r, "/api/v3/system/status", &status)
	if res.Err != nil || res.Verdict != transport.VerdictAccept {
		t.Fatalf("detection probe: %v (verdict %v)", res.Err, res.Verdict)
	}
	epoch, ok := r.Anchor()
	if !ok {
		t.Fatal("detection probe did not anchor its run")
	}
	d := &transport.Detection{
		Identity: transport.Identity{App: "Sonarr", AppVersion: meta.AppVersion},
		Epoch:    epoch, API: arrapi.APIv3,
	}
	c.Install(d)
	if c.Current() != d {
		t.Fatal("detection was not installed")
	}
	f.reset()
	return d
}

// assertNoNeedle fails when s holds any of the given secrets.
func assertNoNeedle(t *testing.T, what, s string, secrets ...string) {
	t.Helper()
	for _, k := range secrets {
		if strings.Contains(s, k) {
			t.Errorf("%s holds %q: %q", what, k, s)
		}
	}
}

// assertChainClean checks Error() and %+v of err and of every error of its tree,
// through both Unwrap forms, as errors.Is and errors.As reach them.
func assertChainClean(t *testing.T, err error, secrets ...string) {
	t.Helper()
	if err == nil {
		return
	}
	assertNoNeedle(t, fmt.Sprintf("%T Error()", err), err.Error(), secrets...)
	assertNoNeedle(t, fmt.Sprintf("%T %%+v", err), fmt.Sprintf("%+v", err), secrets...)
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		assertChainClean(t, u.Unwrap(), secrets...)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			assertChainClean(t, e, secrets...)
		}
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// yield is the interval of a deadline-bounded poll.
func yield() { time.Sleep(time.Millisecond) }

// releaser returns a channel a held handler waits on and its idempotent release,
// which also runs at cleanup so a failed check never leaves the handler blocked.
func releaser(t *testing.T) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	release := sync.OnceFunc(func() { close(ch) })
	t.Cleanup(release)
	return ch, release
}
