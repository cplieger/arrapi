package radarr

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
)

// testKey is a low-entropy placeholder, not a credential.
const testKey arrapi.APIKey = "test-key-7a2e"

const (
	vRadarr  = "6.4.4.10685"
	v4Sonarr = "4.0.20.3014"
	v5Sonarr = "5.0.0.2635"
	fakeBase = "http://radarr.test"

	movies  = "GET /api/v3/movie"
	command = "POST /api/v3/command"
)

// detectRadarr is the requests a detection sends to a Radarr.
var detectRadarr = []string{"GET /api", "GET /api/v3/system/status"}

// step answers one request; a non-nil error is the transport's.
type step func(w http.ResponseWriter, r *http.Request) error

// fake is a scripted server, used as an in-memory RoundTripper (so a test can run in
// a synctest bubble) or behind an httptest.Server. Each route answers with its queue
// of steps, repeating the last; an unrouted GET answers 200 "[]" and an unrouted POST
// 201 "{}", both with the default version.
type fake struct {
	routes map[string][]step
	defV   string
	log    []string // method and path
	uris   []string // method and path with the query
	mu     sync.Mutex
}

func newFake() *fake { return &fake{routes: map[string][]step{}} }

func (f *fake) on(key string, steps ...step) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[key] = steps
}

func (f *fake) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

func (f *fake) requestURIs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.uris)
}

func (f *fake) clearLog() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log, f.uris = nil, nil
}

func (f *fake) next(r *http.Request) step {
	key := r.Method + " " + r.URL.Path
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, key)
	f.uris = append(f.uris, r.Method+" "+r.URL.RequestURI())
	if q := f.routes[key]; len(q) > 0 {
		if len(q) > 1 {
			f.routes[key] = q[1:]
		}
		return q[0]
	}
	if r.Method == http.MethodPost {
		return ans(201, f.defV, "{}")
	}
	return ans(200, f.defV, "[]")
}

func (f *fake) RoundTrip(req *http.Request) (*http.Response, error) {
	s := f.next(req)
	rec := httptest.NewRecorder()
	err := s(rec, req)
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := f.next(r)(w, r); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// ans answers code with the version header when v is set.
func ans(code int, v, body string) step {
	return func(w http.ResponseWriter, _ *http.Request) error {
		if v != "" {
			w.Header().Set("X-Application-Version", v)
		}
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
		return nil
	}
}

// body stores the request body in *got, then runs s.
func body(got *string, s step) step {
	return func(w http.ResponseWriter, r *http.Request) error {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}
		*got = string(b)
		return s(w, r)
	}
}

// redirect answers code to location with the version header when v is set.
func redirect(code int, v, location string) step {
	return func(w http.ResponseWriter, _ *http.Request) error {
		if v != "" {
			w.Header().Set("X-Application-Version", v)
		}
		w.Header().Set("Location", location)
		w.WriteHeader(code)
		return nil
	}
}

// dialFails fails the request as a refused dial does.
func dialFails(http.ResponseWriter, *http.Request) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

// held runs s once release is closed, or fails with the request's context.
func held(release <-chan struct{}, s step) step {
	return func(w http.ResponseWriter, r *http.Request) error {
		select {
		case <-release:
		case <-r.Context().Done():
			return r.Context().Err()
		}
		return s(w, r)
	}
}

// then runs fn before s, on the request's goroutine.
func then(fn func(), s step) step {
	return func(w http.ResponseWriter, r *http.Request) error {
		fn()
		return s(w, r)
	}
}

// releaser returns a channel a held step waits on and its idempotent release, which
// also runs at cleanup so a failed check never leaves the step blocked.
func releaser(t *testing.T) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	release := sync.OnceFunc(func() { close(ch) })
	t.Cleanup(release)
	return ch, release
}

func apiBody(current string, deprecated ...string) string {
	q := ""
	if len(deprecated) > 0 {
		q = `"` + strings.Join(deprecated, `","`) + `"`
	}
	return `{"current":"` + current + `","deprecated":[` + q + `]}`
}

func statusBody(app, version string) string {
	return `{"appName":"` + app + `","version":"` + version + `","arrapiUnknownField":1}`
}

func (f *fake) reset(v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes, f.defV = map[string][]step{}, v
}

// radarr routes f as a Radarr at version v.
func (f *fake) radarr(v string) *fake {
	f.reset(v)
	f.on("GET /api", ans(200, v, apiBody("v3")))
	f.on("GET /api/v3/system/status", ans(200, v, statusBody("Radarr", v)))
	return f
}

// sonarr4 routes f as a Sonarr 4 at version v.
func (f *fake) sonarr4(v string) *fake {
	f.reset(v)
	f.on("GET /api", ans(200, v, apiBody("v3")))
	f.on("GET /api/v3/system/status", ans(200, v, statusBody("Sonarr", v)))
	return f
}

// sonarr5 routes f as a Sonarr 5 at version v.
func (f *fake) sonarr5(v string) *fake {
	f.reset(v)
	f.on("GET /api", ans(200, v, apiBody("v5", "v3")))
	f.on("GET /api/v5/system/status", ans(200, v, statusBody("Sonarr", v)))
	f.on("GET /api/v3/system/status", ans(200, v, statusBody("Sonarr", v)))
	return f
}

// newClient returns a Client sending through f in memory, with a 1 ms backoff base
// and a discarding logger unless opts set others.
func newClient(t *testing.T, f *fake, opts ...Option) *Client {
	t.Helper()
	base := []Option{
		WithHTTPClient(&http.Client{Transport: f}),
		WithBaseDelay(time.Millisecond),
		WithLogger(slog.New(slog.DiscardHandler)),
	}
	c, err := New(fakeBase, testKey, append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// cached makes c detect f with one Movies call, then clears f's log.
func cached(t *testing.T, c *Client, f *fake) {
	t.Helper()
	if _, err := c.Movies(t.Context()); err != nil {
		t.Fatalf("first Movies: %v", err)
	}
	f.clearLog()
}

func wantRequests(t *testing.T, f *fake, want ...string) {
	t.Helper()
	if got := f.requests(); !slices.Equal(got, want) {
		t.Errorf("requests =\n  %q\nwant\n  %q", got, want)
	}
}

func wantURIs(t *testing.T, f *fake, want ...string) {
	t.Helper()
	if got := f.requestURIs(); !slices.Equal(got, want) {
		t.Errorf("requests =\n  %q\nwant\n  %q", got, want)
	}
}

func join(parts ...[]string) []string { return slices.Concat(parts...) }

func wantStatus(t *testing.T, err error, code int) {
	t.Helper()
	se, ok := errors.AsType[*arrapi.StatusError](err)
	if !ok || se.Code != code {
		t.Fatalf("err = %v, want a %d *arrapi.StatusError", err, code)
	}
}

func wantChanged(t *testing.T, err error) *arrapi.ServerChangedError {
	t.Helper()
	e, ok := errors.AsType[*arrapi.ServerChangedError](err)
	if !ok {
		t.Fatalf("err = %v, want *arrapi.ServerChangedError", err)
	}
	return e
}

func wantUnsupported(t *testing.T, err error, reason arrapi.UnsupportedReason) {
	t.Helper()
	e, ok := errors.AsType[*arrapi.UnsupportedAPIError](err)
	if !ok || e.Reason != reason {
		t.Fatalf("err = %v, want *arrapi.UnsupportedAPIError with reason %v", err, reason)
	}
}

// count returns how many requests in f's log equal key.
func count(f *fake, key string) int {
	n := 0
	for _, r := range f.requests() {
		if r == key {
			n++
		}
	}
	return n
}

const movieList = `[{"id":7,"title":"Radarr Movie","tmdbId":603}]`

func wantTitle(t *testing.T, got []Movie, want string) {
	t.Helper()
	if len(got) != 1 || got[0].Title != want {
		t.Fatalf("Movies = %+v, want one movie titled %q", got, want)
	}
}
