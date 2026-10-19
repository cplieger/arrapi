package fixturegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// Get reads path on d in one attempt, judged by Decide like any data read, and
// returns the answer as Decode returns it.
func Get(ctx context.Context, t *transport.Client, d *transport.Detection, path string, limit int64) (any, error) {
	var raw json.RawMessage
	r := t.GetJSON(ctx, d, path, limit, &raw)
	if r.Err != nil {
		return nil, r.Err
	}
	if !r.Accepted() {
		return nil, errors.New("fixturegen: a data read was not accepted")
	}
	return Decode(raw)
}

// ErrorAnswer is a recorded error answer.
type ErrorAnswer struct {
	Body       any    // the captured body as Decode returns it; nil when empty
	AppVersion string // its X-Application-Version; "" when it had none
	Code       int
}

// GetError reads path on d as a probe of a run anchored at d, so Decide and the send
// check judge it, and requires an error answer from d's server whose header presence
// is header: an error path that starts or stops sending X-Application-Version fails
// the recording instead of refusing a later read. A header-less answer ends d.
func GetError(ctx context.Context, t *transport.Client, d *transport.Detection, path string, header bool) (ErrorAnswer, error) {
	var discard json.RawMessage
	meta, r := t.Probe(ctx, transport.AnchoredRun(d), path, &discard)
	if r.Verdict != transport.VerdictStatus && r.Verdict != transport.VerdictUnidentified {
		if r.Err != nil {
			return ErrorAnswer{}, r.Err
		}
		return ErrorAnswer{}, errors.New("fixturegen: an error read was answered with a success")
	}
	se, ok := errors.AsType[*arrapi.StatusError](r.Err)
	if !ok {
		return ErrorAnswer{}, r.Err
	}
	if (meta.AppVersion != "") != header {
		return ErrorAnswer{}, fmt.Errorf("fixturegen: %s: X-Application-Version present is %t, the recording expects %t", se.Path, meta.AppVersion != "", header)
	}
	a := ErrorAnswer{AppVersion: meta.AppVersion, Code: se.Code}
	if se.Body != "" {
		body, err := Decode([]byte(se.Body))
		if err != nil {
			return ErrorAnswer{}, fmt.Errorf("fixturegen: %s: the error body is not JSON", se.Path)
		}
		a.Body = body
	}
	return a, nil
}

// Gitleaks scans dir with gitleaks and returns how many findings it reported. The
// report is written under tmp, which the caller removes, and is never returned.
func Gitleaks(ctx context.Context, dir, tmp string) (int, error) {
	report := filepath.Join(tmp, "gitleaks.json")
	cmd := exec.CommandContext(ctx, "gitleaks", "dir", dir, "--no-banner", "--redact", "--exit-code", "1",
		"--report-format", "json", "--report-path", report)
	runErr := cmd.Run()
	var exit *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exit) {
		return 0, fmt.Errorf("fixturegen: gitleaks did not run: %w", runErr)
	}
	b, err := os.ReadFile(report)
	if err != nil {
		return 0, err
	}
	var findings []json.RawMessage
	if err := json.Unmarshal(b, &findings); err != nil {
		return 0, errors.New("fixturegen: gitleaks report is not a JSON list")
	}
	if runErr != nil && len(findings) == 0 {
		return 0, errors.New("fixturegen: gitleaks failed without a finding")
	}
	return len(findings), nil
}

// Reader reads one fixture directory's answers on one detection into a Recording. The
// first failure stops it: every later call does nothing, and Err reports it.
type Reader struct {
	Err error
	rec *Recording
	t   *transport.Client
	d   *transport.Detection
	dir string
}

// NewReader returns a Reader adding files under dir, reading on d through t.
func NewReader(rec *Recording, t *transport.Client, d *transport.Detection, dir string) *Reader {
	return &Reader{rec: rec, t: t, d: d, dir: dir}
}

// Get reads path (Get's rules) and returns the answer; nil once the Reader failed.
func (r *Reader) Get(ctx context.Context, path string, limit int64) any {
	if r.Err != nil {
		return nil
	}
	v, err := Get(ctx, r.t, r.d, path, limit)
	r.Err = err
	return v
}

// List reads the list at path; nil once the Reader failed.
func (r *Reader) List(ctx context.Context, path string) []any {
	v := r.Get(ctx, path, transport.MaxListBytes)
	if r.Err != nil {
		return nil
	}
	l, ok := v.([]any)
	if !ok {
		r.Err = errors.New("fixturegen: a list read was not answered with a list")
	}
	return l
}

// Add adds v as the file name of the Reader's directory, projected onto t.
func (r *Reader) Add(name string, v any, t reflect.Type) {
	if r.Err == nil {
		r.rec.AddJSON(r.dir+"/"+name, v, t)
	}
}

// Read reads path and adds its answer as name, projected onto t.
func (r *Reader) Read(ctx context.Context, name, path string, limit int64, t reflect.Type) any {
	v := r.Get(ctx, path, limit)
	r.Add(name, v, t)
	return v
}

// Error reads path with GetError and adds name.headers, plus name.json projected onto
// ErrorDoc when the answer has a body.
func (r *Reader) Error(ctx context.Context, name, path string, header bool) {
	if r.Err != nil {
		return
	}
	a, err := GetError(ctx, r.t, r.d, path, header)
	if err != nil {
		r.Err = err
		return
	}
	r.rec.AddHeaders(r.dir+"/"+name+".headers", a.AppVersion)
	if a.Body != nil {
		r.rec.AddJSON(r.dir+"/"+name+".json", a.Body, reflect.TypeFor[transport.ErrorDoc]())
	}
}

// Find returns the object of list whose member equals want; a miss fails the Reader.
func (r *Reader) Find(list []any, member string, want int64) map[string]any {
	if r.Err != nil {
		return nil
	}
	obj, ok := Find(list, member, want)
	if !ok {
		r.Err = fmt.Errorf("fixturegen: %s: no item has %s %d", r.dir, member, want)
	}
	return obj
}
