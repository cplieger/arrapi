package fixturegen

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	urlPattern     = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://`)
	hexPattern     = regexp.MustCompile(`(?i)(^|[^0-9a-f])[0-9a-f]{40}($|[^0-9a-f])`)
	headerPattern  = regexp.MustCompile(`^X-Application-Version: \d[0-9.]*$`)
	syntheticRoots = []string{"/tv/", "/movies/"}
)

// statusMembers are the only members a recorded system-status.json may hold.
var statusMembers = map[string]bool{"appName": true, "version": true, UnknownField: true}

// CheckTree runs Check over every file under root.
func CheckTree(root string) error {
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec // G304: walking the fixture tree is the job
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		return err
	}
	return Check(files)
}

// Check applies the checks that need no live run to fixture files keyed by slash
// path: no URL, 40-hex hash or absolute path outside /tv/ and /movies/ in any string;
// no history data member; no system-status.json member beyond appName, version and
// UnknownField; no .headers line but X-Application-Version; every date in 2001 and
// every size 0 or synthetic. Its error names each file and rule, never the value.
func Check(files map[string][]byte) error {
	var findings []string
	for _, name := range sortedNames(files) {
		for _, f := range checkFile(name, files[name]) {
			findings = append(findings, name+": "+f)
		}
	}
	if len(findings) > 0 {
		return &RefusedError{Findings: findings}
	}
	return nil
}

func checkFile(name string, b []byte) []string {
	switch path.Ext(name) {
	case ".headers":
		for line := range strings.SplitSeq(strings.TrimSuffix(string(b), "\n"), "\n") {
			if line != "" && !headerPattern.MatchString(line) {
				return []string{"a header line other than X-Application-Version"}
			}
		}
		return nil
	case ".json":
	default:
		return []string{"an unexpected file"}
	}
	v, err := Decode(b)
	if err != nil {
		return []string{"not one JSON value"}
	}
	c := &checker{history: strings.HasPrefix(path.Base(name), "history")}
	if path.Base(name) == "system-status.json" {
		obj, _ := v.(map[string]any)
		for k := range obj {
			if !statusMembers[k] {
				c.add("a system/status member beyond appName and version")
			}
		}
	}
	c.walk(v, "")
	return c.findings
}

type checker struct {
	seen     map[string]bool
	findings []string
	history  bool
}

func (c *checker) add(f string) {
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	if !c.seen[f] {
		c.seen[f] = true
		c.findings = append(c.findings, f)
	}
}

func (c *checker) walk(v any, member string) {
	switch x := v.(type) {
	case map[string]any:
		c.object(x)
	case []any:
		for _, item := range x {
			c.walk(item, member)
		}
	case string:
		c.text(x)
		if member == "date" && !strings.HasPrefix(x, "2001-") {
			c.add("a date outside the synthetic year 2001")
		}
	case json.Number:
		if member == "size" && !syntheticSize(x) {
			c.add("a size neither 0 nor synthetic")
		}
	default:
	}
}

func (c *checker) object(obj map[string]any) {
	for k, v := range obj {
		if c.history && k == "data" {
			c.add("a history data member")
		}
		c.walk(v, k)
	}
}

func (c *checker) text(s string) {
	if urlPattern.MatchString(s) {
		c.add("a URL")
	}
	if hexPattern.MatchString(s) {
		c.add("a 40-hex hash")
	}
	if hasForeignPath(s) {
		c.add("an absolute path outside the synthetic roots")
	}
}

// hasForeignPath finds a "/" or "\" that starts a path token outside the synthetic
// roots. One after a letter, digit or name character sits inside a word or a relative
// path ("Fate/Zero", "Season 01\x.mkv") and starts none; any other rune before it, a
// line break, a control, a symbol or a non-ASCII space included, starts one. A "\"
// there is always a Windows root (C:\, \\nas, \\?\, \\.\); a "/" is one unless a space
// or the end follows it, as in Radarr's "English / Japanese". A synthetic root counts
// only when no ".." segment follows it in the rest of the string.
func hasForeignPath(s string) bool {
	for i, r := range s {
		if r != '/' && r != '\\' {
			continue
		}
		if prev, _ := utf8.DecodeLastRuneInString(s[:i]); i > 0 && isNameRune(prev) {
			continue
		}
		if r == '\\' {
			return true
		}
		if next, _ := utf8.DecodeRuneInString(s[i+1:]); i+1 == len(s) || unicode.IsSpace(next) {
			continue
		}
		rest := s[i:]
		if !slices.ContainsFunc(syntheticRoots, func(root string) bool { return strings.HasPrefix(rest, root) }) || climbs(rest) {
			return true
		}
	}
	return false
}

func climbs(p string) bool {
	return slices.Contains(strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }), "..")
}

func isNameRune(r rune) bool {
	return r == '.' || r == '_' || r == '-' || r == '~' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func syntheticSize(n json.Number) bool {
	i, err := n.Int64()
	if err != nil {
		return false
	}
	return i == 0 || (i >= syntheticSizeBase && (i-syntheticSizeBase)%syntheticSizeStep == 0)
}
