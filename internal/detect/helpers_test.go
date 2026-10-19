package detect

import (
	"context"
	"io"
	"log/slog"
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
const testKey arrapi.APIKey = "test-key-5d1c"

const (
	v4     = "4.0.20.3014"
	v5     = "5.0.0.2635"
	vR     = "6.4.4.10685"
	base   = "http://arr.test"
	series = "/api/v3/series"
)

// step answers one request; a non-nil error is the transport's.
type step func(w http.ResponseWriter, r *http.Request) error

// fakeServer is an in-memory RoundTripper, so tests run inside synctest bubbles.
// Each route answers with its queue of steps, repeating the last; an unrouted GET
// answers 200 "[]" and an unrouted POST 201 "{}", both with the default version.
type fakeServer struct {
	routes map[string][]step
	onRead map[string]func() // runs once, at the first read of the route's next body
	defV   string
	log    []string
	mu     sync.Mutex
}

func newFake() *fakeServer { return &fakeServer{routes: map[string][]step{}} }

func (f *fakeServer) on(key string, steps ...step) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[key] = steps
}

func (f *fakeServer) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

func (f *fakeServer) clearLog() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = nil
}

func (f *fakeServer) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.Path
	f.mu.Lock()
	f.log = append(f.log, key)
	var s step
	if q := f.routes[key]; len(q) > 0 {
		s = q[0]
		if len(q) > 1 {
			f.routes[key] = q[1:]
		}
	}
	defV := f.defV
	hook := f.onRead[key]
	delete(f.onRead, key)
	f.mu.Unlock()
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	if s == nil {
		if req.Method == http.MethodPost {
			s = ans(201, defV, "{}")
		} else {
			s = ans(200, defV, "[]")
		}
	}
	rec := httptest.NewRecorder()
	if err := s(rec, req); err != nil {
		return nil, err
	}
	resp := rec.Result()
	resp.Request = req
	if hook != nil {
		resp.Body = &hookedBody{ReadCloser: resp.Body, hook: hook}
	}
	return resp, nil
}

// hookedBody runs hook at its first Read: after the observer judged the answer's
// header and before the caller decodes it.
type hookedBody struct {
	io.ReadCloser
	hook func()
}

func (b *hookedBody) Read(p []byte) (int, error) {
	if b.hook != nil {
		b.hook()
		b.hook = nil
	}
	return b.ReadCloser.Read(p)
}

// beforeDecode runs fn at the first read of key's next answer body.
func (f *fakeServer) beforeDecode(key string, fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onRead == nil {
		f.onRead = map[string]func(){}
	}
	f.onRead[key] = fn
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

func apiBody(current string, deprecated ...string) string {
	return `{"current":"` + current + `","deprecated":["` + strings.Join(deprecated, `","`) + `"]}`
}

func apiBodyNoDeprecated(current string) string {
	return `{"current":"` + current + `","deprecated":[]}`
}

func statusBody(app, version string) string {
	return `{"appName":"` + app + `","version":"` + version + `","arrapiUnknownField":1}`
}

// serveSonarr4 routes f as a Sonarr whose /api announces current "v3" answers at
// application version v.
func serveSonarr4(f *fakeServer, v string) {
	f.mu.Lock()
	f.routes, f.defV = map[string][]step{}, v
	f.mu.Unlock()
	f.on("GET /api", ans(200, v, apiBodyNoDeprecated("v3")))
	f.on("GET /api/v5/system/status", ans(404, v, ""))
	f.on("GET /api/v3/system/status", ans(200, v, statusBody("Sonarr", v)))
}

// serveSonarr5 routes f as a Sonarr 5.0 at version v answers.
func serveSonarr5(f *fakeServer, v string) {
	f.mu.Lock()
	f.routes, f.defV = map[string][]step{}, v
	f.mu.Unlock()
	f.on("GET /api", ans(200, v, apiBody("v5", "v3")))
	f.on("GET /api/v5/system/status", ans(200, v, statusBody("Sonarr", v)))
	f.on("GET /api/v3/system/status", ans(200, v, statusBody("Sonarr", v)))
}

// serveRadarr routes f as a Radarr at version v answers.
func serveRadarr(f *fakeServer, v string) {
	f.mu.Lock()
	f.routes, f.defV = map[string][]step{}, v
	f.mu.Unlock()
	f.on("GET /api", ans(200, v, apiBodyNoDeprecated("v3")))
	f.on("GET /api/v5/system/status", ans(404, v, ""))
	f.on("GET /api/v3/system/status", ans(200, v, statusBody("Radarr", v)))
}

func newClient(t *testing.T, f *fakeServer, cfg transport.Config) *transport.Client {
	t.Helper()
	return newClientKey(t, f, testKey, cfg)
}

func newClientKey(t *testing.T, f *fakeServer, key arrapi.APIKey, cfg transport.Config) *transport.Client {
	t.Helper()
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Transport: f}
	}
	if cfg.BaseDelay == 0 {
		cfg.BaseDelay = time.Millisecond
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	c, err := transport.New(base, key, cfg)
	if err != nil {
		t.Fatalf("transport.New: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func run(t *testing.T, c *transport.Client, app string) (*transport.Detection, error) {
	t.Helper()
	return walkArms(t.Context(), c, transport.NewProbeRun(), app, false)
}

// installed runs a detection against f, installs it and clears f's log.
func installed(t *testing.T, c *transport.Client, f *fakeServer, app string) *transport.Detection {
	t.Helper()
	d, err := run(t, c, app)
	if err != nil {
		t.Fatalf("detection: %v", err)
	}
	c.Install(d)
	if c.Current() != d {
		t.Fatal("detection not installed")
	}
	f.clearLog()
	return d
}

func wantRequests(t *testing.T, f *fakeServer, want ...string) {
	t.Helper()
	if got := f.requests(); !slices.Equal(got, want) {
		t.Errorf("requests =\n  %q\nwant\n  %q", got, want)
	}
}

// engine is the test sessions' engine: the client and the API prefix its paths go
// under.
type engine struct {
	c      *transport.Client
	prefix string
}

func newSession(c *transport.Client) *Session[engine] {
	return NewSession(c, AppSonarr, func(d *transport.Detection) engine {
		if d.API == arrapi.APIv5 {
			return engine{c, "/api/v5"}
		}
		return engine{c, "/api/v3"}
	})
}

// readSeries is a list read of the engine's series path.
func readSeries(out *[]any) func(context.Context, *transport.Detection, engine) transport.Result {
	return func(ctx context.Context, d *transport.Detection, e engine) transport.Result {
		return e.c.GetJSON(ctx, d, e.prefix+"/series", transport.MaxListBytes, out)
	}
}

// rescan is a command on the engine's command path.
func rescan(ctx context.Context, d *transport.Detection, e engine) transport.Result {
	return e.c.Post(ctx, d, e.prefix+"/command", map[string]any{"name": "RescanSeries", "seriesId": 1})
}

// recorder captures log records.
type recorder struct {
	recs []slog.Record
	mu   sync.Mutex
}

func (*recorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec.Clone())
	return nil
}
func (r *recorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recorder) WithGroup(string) slog.Handler      { return r }

// find returns the attrs of the records with message msg, rendered as text.
func (r *recorder) find(msg string) []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]string
	for _, rec := range r.recs {
		if rec.Message != msg {
			continue
		}
		m := map[string]string{"level": rec.Level.String()}
		rec.Attrs(func(a slog.Attr) bool {
			m[a.Key] = a.Value.String()
			return true
		})
		out = append(out, m)
	}
	return out
}

func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, rec := range r.recs {
		b.WriteString(rec.Message)
		rec.Attrs(func(a slog.Attr) bool {
			b.WriteString(" " + a.Key + "=" + a.Value.String())
			return true
		})
		b.WriteString("\n")
	}
	return b.String()
}
