package fixturegen

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Route names the recorded answer a Replay serves for one request: either File, a
// data answer, or Role with Code, an error answer.
type Route struct {
	// File is served 200 with the directory's system-status.json version, the header
	// every recorded data answer carried, since Decide accepted it.
	File string
	// Role is an error answer recorded as Role.headers and, when it had a body,
	// Role.json, served with Code and the version Role.headers holds, or none.
	Role string
	Code int
}

// Replay serves one fixture directory's recorded answers by request, so a test reads
// them through a real client. A request no route names is answered 404 without
// X-Application-Version and kept as a miss. It is safe for concurrent use.
type Replay struct {
	answers map[string]answer
	log     []string
	misses  []string
	mu      sync.Mutex
}

type answer struct {
	version string
	body    []byte
	code    int
}

// NewReplay loads every file routes names under dir. A route key is the request as
// the client sends it, "GET <path>[?<query>]". The error names the file or route it
// refuses: a missing file, a .headers line other than X-Application-Version, a route
// naming both or neither of File and Role, an error route without a Code, or a
// directory whose system-status.json records no version.
func NewReplay(dir string, routes map[string]Route) (*Replay, error) {
	version, err := recordedVersion(dir)
	if err != nil {
		return nil, err
	}
	p := &Replay{answers: map[string]answer{}}
	for key, rt := range routes {
		switch {
		case (rt.File == "") == (rt.Role == ""):
			return nil, fmt.Errorf("fixturegen: route %q names both or neither of a file and an error role", key)
		case rt.File != "":
			body, err := os.ReadFile(filepath.Join(dir, rt.File))
			if err != nil {
				return nil, fmt.Errorf("fixturegen: %s: %w", rt.File, err)
			}
			p.answers[key] = answer{version: version, body: body, code: http.StatusOK}
		case rt.Code == 0:
			return nil, fmt.Errorf("fixturegen: error role %s has no status code", rt.Role)
		default:
			a, err := errorAnswer(dir, rt.Role)
			if err != nil {
				return nil, err
			}
			a.code = rt.Code
			p.answers[key] = a
		}
	}
	return p, nil
}

// recordedVersion is the version dir's system-status.json records.
func recordedVersion(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "system-status.json"))
	if err != nil {
		return "", fmt.Errorf("fixturegen: system-status.json: %w", err)
	}
	v, err := Decode(b)
	status, _ := v.(map[string]any)
	version, _ := status["version"].(string)
	if err != nil || version == "" {
		return "", errors.New("fixturegen: system-status.json records no version")
	}
	return version, nil
}

func errorAnswer(dir, role string) (answer, error) {
	h, err := os.ReadFile(filepath.Join(dir, role+".headers"))
	if err != nil {
		return answer{}, fmt.Errorf("fixturegen: %s.headers: %w", role, err)
	}
	var a answer
	if line := strings.TrimSuffix(string(h), "\n"); line != "" {
		if !headerPattern.MatchString(line) {
			return answer{}, fmt.Errorf("fixturegen: %s.headers holds a line other than X-Application-Version", role)
		}
		a.version = strings.TrimPrefix(line, "X-Application-Version: ")
	}
	body, err := os.ReadFile(filepath.Join(dir, role+".json"))
	switch {
	case err == nil:
		a.body = body
	case !errors.Is(err, os.ErrNotExist):
		return answer{}, fmt.Errorf("fixturegen: %s.json: %w", role, err)
	}
	return a, nil
}

// ServeHTTP answers r with its route's recorded answer.
func (p *Replay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.RequestURI()
	p.mu.Lock()
	p.log = append(p.log, key)
	a, ok := p.answers[key]
	if !ok {
		p.misses = append(p.misses, key)
	}
	p.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if a.version != "" {
		w.Header().Set("X-Application-Version", a.version)
	}
	if len(a.body) > 0 {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(a.code)
	_, _ = w.Write(a.body)
}

// Requests returns every request served, routed or not, in arrival order.
func (p *Replay) Requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.log)
}

// Misses returns the requests no route named, in arrival order.
func (p *Replay) Misses() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.misses)
}
