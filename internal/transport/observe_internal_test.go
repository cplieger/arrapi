package transport

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	iv4 = "4.0.20.3014"
	iv5 = "5.0.0.1"
)

func versioned(v, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Application-Version", v)
		_, _ = w.Write([]byte(body))
	}
}

// storeFromOutside advances the State as another call's answer naming v would.
func storeFromOutside(c *Client, v string) {
	s := c.state.Load()
	c.state.Store(&State{Epoch: s.Epoch + 1, AppVersion: v, Path: "/api/v3/tag"})
}

func TestAnchorIsTheComparedState(t *testing.T) {
	var statusSeen atomic.Int64
	var nothingAnswers atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nothingAnswers.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/api/v3/system/status" {
			statusSeen.Add(1)
		}
		versioned(iv4, `{}`)(w, r)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "test-key", Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	var out any
	c.Probe(t.Context(), NewProbeRun(), "/api", &out)
	if s := c.State(); s.Epoch != 1 || s.AppVersion != iv4 {
		t.Fatalf("State = %+v, want epoch 1 at %s", s, iv4)
	}

	r := NewProbeRun()
	var once atomic.Bool
	c.afterCompare = func() {
		if once.CompareAndSwap(false, true) {
			storeFromOutside(c, iv5)
		}
	}
	c.Probe(t.Context(), r, "/api", &out)
	c.afterCompare = nil
	if epoch, ok := r.Anchor(); !ok || epoch != 1 {
		t.Errorf("Anchor() = (%d, %v), want the compared epoch 1", epoch, ok)
	}
	_, res := c.Probe(t.Context(), r, "/api/v3/system/status", &out)
	if res.Verdict != VerdictRefused || statusSeen.Load() != 0 {
		t.Errorf("status probe verdict %v, server saw %d status requests; want refused and none", res.Verdict, statusSeen.Load())
	}

	unidentified := NewProbeRun()
	nothingAnswers.Store(true)
	c.Probe(t.Context(), unidentified, "/api", &out)
	if _, ok := unidentified.Anchor(); ok {
		t.Error("a run whose answers named no server reports an anchor")
	}
	if unidentified.Answered() != "/api" {
		t.Errorf("Answered() = %q, want /api", unidentified.Answered())
	}
}

func TestObserveLostCASIsJudgedAgain(t *testing.T) {
	srv := httptest.NewServer(versioned(iv5, `[]`))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "test-key", Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	c.state.Store(&State{Epoch: 1, AppVersion: iv4})
	d := &Detection{Identity: Identity{App: "Sonarr", AppVersion: iv4}, Epoch: 1}
	var once atomic.Bool
	c.afterCompare = func() {
		if once.CompareAndSwap(false, true) {
			storeFromOutside(c, iv5)
		}
	}
	var out any
	c.GetJSON(t.Context(), d, "/api/v3/series", MaxListBytes, &out)
	if s := c.State(); s.Epoch != 2 || s.AppVersion != iv5 || s.Path != "/api/v3/tag" {
		t.Errorf("State = %+v, want the outside store kept: the answer agrees with it once judged again", s)
	}
}

type closeTracker struct {
	io.Reader
	closed atomic.Bool
}

func (b *closeTracker) Close() error { b.closed.Store(true); return nil }

func TestRefusedRequestBodyIsClosed(t *testing.T) {
	c, err := New("http://sonarr:8989", "test-key", Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	c.state.Store(&State{Epoch: 2, AppVersion: iv5})
	d := &Detection{Identity: Identity{App: "Sonarr", AppVersion: iv4}, Epoch: 1}
	body := &closeTracker{Reader: strings.NewReader(`{}`)}
	req, err := http.NewRequestWithContext(withTag(t.Context(), newTag("/api/v3/command", d, nil)), http.MethodPost, "http://sonarr:8989/api/v3/command", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := c.cmd.Transport.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if _, ok := err.(*notSentError); !ok || !body.closed.Load() {
		t.Errorf("RoundTrip = %v, body closed %v; want *notSentError with the body closed", err, body.closed.Load())
	}
}

func TestUntaggedRequestIsNeverSent(t *testing.T) {
	var seen atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { seen.Add(1) }))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "test-key", Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	body := &closeTracker{Reader: strings.NewReader(`{}`)}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/v3/command", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := c.data.Transport.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || seen.Load() != 0 || !body.closed.Load() {
		t.Errorf("RoundTrip = %v, server saw %d, body closed %v; want an error, nothing sent and the body closed", err, seen.Load(), body.closed.Load())
	}
}
