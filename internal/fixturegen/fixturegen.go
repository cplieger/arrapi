// Package fixturegen turns live answers into committed test fixtures. A Recording
// projects each answer onto the wire DTO that decodes it, replaces every value that
// records the operator's library, activity or deployment with a synthetic one, and
// refuses to write anything while a rendered file still carries a secret or a
// deployment value. Check repeats the checks that need no live run over committed
// fixtures. Only the recorders and the fixture tests import it.
package fixturegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

// Kind says how Write rewrites a DTO member's value.
type Kind int

// The rewrites. A member with no rule keeps its number or bool, keeps a string whose
// member name is public metadata, and has any other string replaced as Pseudo "text".
const (
	ID        Kind = iota + 1 // an internal id or foreign key (or a list of them): Role's distinct ids, ascending, become 1, 2, 3, ...; 0 stays 0
	Size                      // a file size: the distinct sizes, ascending, become 1000000000 + 1000000*i; 0 stays 0
	Date                      // a history time: the distinct times, ascending, become 2001-01-01T00:00:00Z + i hours
	FileCount                 // a series' count of episodes with a file: the count its directory carries for the enclosing Role object (Carries), else 1; 0 stays 0
	Pseudo                    // a deployment string: Role's distinct values, ascending, become Role-1, Role-2, ...; "" stays ""
	RelPath                   // a file path relative to its folder: [Season NN/]Role-<synthetic id><ext>
	Path                      // an absolute file path: Root/folder-<n>/ and the RelPath form
	RootPath                  // a root folder: Root/root-<n>
)

// Rule is one member's rewrite.
type Rule struct {
	Role string // ID and FileCount: the id map; Pseudo, RelPath and Path: the synthetic name
	Root string // Path and RootPath: the synthetic root, "/tv" or "/movies"
	Kind Kind
}

// Rules maps a DTO type to its members' rules by JSON name.
type Rules map[reflect.Type]map[string]Rule

// Recording holds one run's answers in memory; nothing reaches disk before Write
// has passed every check.
type Recording struct {
	counts  map[string]int
	rules   Rules
	carried map[carry]int
	secrets []func(string) bool
	private []string
	refused []string // answers Project refused, one "file: rule" entry each
	files   []file
}

type carry struct {
	dir string
	id  int64
}

type file struct {
	doc     any
	typ     reflect.Type
	name    string // slash path under the root Write is given
	version string // a .headers file's X-Application-Version, "" for none
	headers bool
}

// errorRules replace both members of an error answer whatever they hold. Sonarr's
// exception pipeline copies the caught exception's text into message
// (src/Sonarr.Http/ErrorManagement/SonarrErrorPipeline.cs), and title is whatever the
// framework or a proxy put in a ProblemDetails, so neither is public metadata.
var errorRules = map[string]Rule{"message": {Kind: Pseudo, Role: "error"}, "title": {Kind: Pseudo, Role: "error"}}

// NewRecording returns an empty recording whose JSON answers are rewritten by rules,
// and whose transport.ErrorDoc answers have their message and title replaced.
func NewRecording(rules Rules) *Recording {
	all := Rules{}
	maps.Copy(all, rules)
	all[reflect.TypeFor[transport.ErrorDoc]()] = errorRules
	return &Recording{rules: all, carried: map[carry]int{}}
}

// AddJSON adds the answer v, generic JSON as Decode returns it, as the file name (a
// slash path), projected onto t; an answer Project refuses makes Write refuse.
func (r *Recording) AddJSON(name string, v any, t reflect.Type) {
	doc, err := project(v, t, "")
	if err != nil {
		r.refused = append(r.refused, name+": "+err.Error())
		return
	}
	r.files = append(r.files, file{name: name, doc: doc, typ: t})
}

// AddHeaders adds the file name holding only the answer's X-Application-Version, or
// nothing when appVersion is "".
func (r *Recording) AddHeaders(name, appVersion string) {
	r.files = append(r.files, file{name: name, version: appVersion, headers: true})
}

// Carries records that the files of directory dir carry n episodes with a file of the
// series whose real id is id, the FileCount that series gets there.
func (r *Recording) Carries(dir string, id int64, n int) { r.carried[carry{dir, id}] = n }

// Private adds strings the run saw that no written file may hold (an instance name,
// a host name).
func (r *Recording) Private(s ...string) { r.private = append(r.private, s...) }

// Secret adds a test that no written file may pass, such as holding the client's key.
func (r *Recording) Secret(contains func(string) bool) { r.secrets = append(r.secrets, contains) }

// Write synthesizes every answer, renders the files, scans them and, only when no scan
// finds anything and no file it does not write sits in a top-level directory it
// writes, writes them under root and returns how many it wrote. A refusal names each
// file and rule, never the value it found.
func (r *Recording) Write(root string) (int, error) {
	s := newSynth(r.rules, r.carried)
	for _, f := range r.files {
		if !f.headers {
			s.walk(f, true)
		}
	}
	s.assign()
	r.counts = s.counts()
	rendered, findings := r.render(s)
	findings = slices.Concat(r.refused, findings, s.findings)
	for _, name := range sortedNames(rendered) {
		findings = append(findings, r.scan(s, name, string(rendered[name]))...)
	}
	if refused, ok := errors.AsType[*RefusedError](Check(rendered)); ok {
		findings = append(findings, refused.Findings...)
	}
	stale, err := staleFiles(root, rendered)
	if err != nil {
		return 0, err
	}
	findings = append(findings, stale...)
	if len(findings) > 0 {
		return 0, &RefusedError{Findings: findings}
	}
	for _, name := range sortedNames(rendered) {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(p, rendered[name], 0o644); err != nil { //nolint:gosec // G306: committed fixtures are world-readable
			return 0, err
		}
	}
	return len(rendered), nil
}

// staleFiles lists every file under a top-level directory of root that the rendered
// set writes into and does not hold, so a re-record never keeps an answer it no longer
// takes.
func staleFiles(root string, rendered map[string][]byte) ([]string, error) {
	tops := map[string]bool{}
	for name := range rendered {
		top, _, _ := strings.Cut(name, "/")
		tops[top] = true
	}
	var out []string
	for _, top := range slices.Sorted(maps.Keys(tops)) {
		found, err := staleUnder(root, filepath.Join(root, top), rendered)
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	return out, nil
}

func staleUnder(root, dir string, rendered map[string][]byte) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil && p == dir && errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return err
		case d.IsDir():
			return nil
		default:
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if name := filepath.ToSlash(rel); !hasName(rendered, name) {
			out = append(out, name+": a stale fixture this recording does not write")
		}
		return nil
	})
	return out, err
}

// render rewrites and renders every file in memory.
func (r *Recording) render(s *synth) (rendered map[string][]byte, findings []string) {
	rendered = make(map[string][]byte, len(r.files))
	for _, f := range r.files {
		if _, dup := rendered[f.name]; dup {
			findings = append(findings, f.name+": added twice")
			continue
		}
		if f.headers {
			rendered[f.name] = headerFile(f.version)
			continue
		}
		b, err := renderJSON(s.walk(f, false))
		if err != nil {
			findings = append(findings, f.name+": does not render")
			continue
		}
		rendered[f.name] = b
	}
	return rendered, findings
}

// Counts reports, after Write, how many distinct real values each map replaced, keyed
// "id:<role>", "pseudo:<role>", "size" and "date"; never the values.
func (r *Recording) Counts() map[string]int { return r.counts }

// RefusedError lists what stopped a Write or a Check, one "file: rule" entry each.
type RefusedError struct{ Findings []string }

func (e *RefusedError) Error() string {
	return "fixturegen: refused: " + strings.Join(e.Findings, "; ")
}

// scan applies the run-dependent checks: the secrets, the private strings and every
// real size and time the run replaced.
func (r *Recording) scan(s *synth, name, body string) []string {
	var out []string
	for _, contains := range r.secrets {
		if contains(body) {
			out = append(out, name+": holds a secret")
			break
		}
	}
	for _, p := range slices.Concat(r.private, s.privateStrings()) {
		if holdsString(body, p) {
			out = append(out, name+": holds a deployment string")
			break
		}
	}
	for _, n := range s.realSizes() {
		if holdsNumber(body, n) {
			out = append(out, name+": holds a real file size")
			break
		}
	}
	for _, d := range s.realDates() {
		if strings.Contains(body, d) {
			out = append(out, name+": holds a real history time")
			break
		}
	}
	return out
}

// holdsString matches a long private string anywhere, and a short one, which could
// occur inside public text by chance, only as a whole JSON string.
func holdsString(body, p string) bool {
	if p == "" {
		return false
	}
	enc, _ := json.Marshal(p)
	quoted := string(enc)
	if len(p) >= 8 {
		return strings.Contains(body, p) || strings.Contains(body, quoted[1:len(quoted)-1])
	}
	return strings.Contains(body, quoted)
}

func holdsNumber(body, n string) bool {
	for i := 0; ; {
		j := strings.Index(body[i:], n)
		if j < 0 {
			return false
		}
		j += i
		end := j + len(n)
		if (j == 0 || !isDigit(body[j-1])) && (end == len(body) || !isDigit(body[end])) {
			return true
		}
		i = j + 1
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func headerFile(version string) []byte {
	if version == "" {
		return []byte{}
	}
	return []byte("X-Application-Version: " + version + "\n")
}

func renderJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func hasName(m map[string][]byte, name string) bool {
	_, ok := m[name]
	return ok
}

func sortedNames(m map[string][]byte) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Decode parses exactly one JSON value, followed by nothing but whitespace, with
// numbers kept as json.Number, so no id or size loses digits on the way to a fixture.
func Decode(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, errors.New("fixturegen: not JSON")
	}
	var rest any
	if err := dec.Decode(&rest); !errors.Is(err, io.EOF) {
		return nil, errors.New("fixturegen: more than one JSON value")
	}
	return v, nil
}

// Int returns a JSON integer's value.
func Int(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}

// Find returns the first object of list whose member equals want.
func Find(list []any, member string, want int64) (map[string]any, bool) {
	for _, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if n, ok := Int(obj[member]); ok && n == want {
			return obj, true
		}
	}
	return nil, false
}

// Sample keeps at most limit items of list, in their order: the first item of each
// distinct member value, then the earliest of the rest.
func Sample(list []any, member string, limit int) []any {
	if len(list) <= limit {
		return list
	}
	keep := make([]bool, len(list))
	seen := map[string]bool{}
	n := 0
	for i, item := range list {
		k := fmt.Sprint(memberOf(item, member))
		if !seen[k] && n < limit {
			seen[k], keep[i] = true, true
			n++
		}
	}
	for i := range list {
		if n == limit {
			break
		}
		if !keep[i] {
			keep[i] = true
			n++
		}
	}
	out := make([]any, 0, limit)
	for i, item := range list {
		if keep[i] {
			out = append(out, item)
		}
	}
	return out
}

func memberOf(v any, member string) any {
	if obj, ok := v.(map[string]any); ok {
		return obj[member]
	}
	return nil
}

func dirOf(name string) string { return path.Dir(name) }
