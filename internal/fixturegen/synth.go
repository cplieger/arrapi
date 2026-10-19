package fixturegen

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// public is the allowlist of member names whose strings are public metadata, kept as
// recorded.
var public = map[string]bool{
	"title": true, "titleSlug": true, "originalTitle": true, "name": true, "type": true,
	"seasonType": true, "airDate": true, "imdbId": true, "videoCodec": true,
	"audioLanguages": true, "language": true, "eventType": true, "appName": true,
	"version": true, "current": true, "deprecated": true,
}

const (
	syntheticSizeBase = 1_000_000_000
	syntheticSizeStep = 1_000_000
	pseudoText        = "text"
	pseudoFolder      = "folder"
	pseudoRoot        = "root"
)

var syntheticDateBase = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

var extension = regexp.MustCompile(`^\.[A-Za-z0-9]{1,5}$`)

// synth holds one run's maps from real values to synthetic ones. walk runs twice over
// every answer: the first pass collects the real values, assign orders them, and the
// second pass rewrites, so every map is ascending over the whole run.
type synth struct {
	rules    Rules
	carried  map[carry]int
	ids      map[string]map[int64]int64
	sizes    map[int64]int64
	dates    map[string]string
	pseudo   map[string]map[string]string
	dir      string
	file     string
	findings []string
	collect  bool
}

func newSynth(rules Rules, carried map[carry]int) *synth {
	return &synth{
		rules: rules, carried: carried,
		ids: map[string]map[int64]int64{}, sizes: map[int64]int64{},
		dates: map[string]string{}, pseudo: map[string]map[string]string{},
	}
}

// walk rewrites f.doc, which is Project's output, so every value's JSON kind fits its
// DTO type.
func (s *synth) walk(f file, collect bool) any {
	s.collect, s.dir, s.file = collect, dirOf(f.name), f.name
	return s.value(f.doc, f.typ, nil, "", map[string]int64{})
}

func (s *synth) assign() {
	for _, m := range s.ids {
		for i, real := range slices.Sorted(maps.Keys(m)) {
			m[real] = int64(i + 1)
		}
	}
	for i, real := range slices.Sorted(maps.Keys(s.sizes)) {
		s.sizes[real] = syntheticSizeBase + syntheticSizeStep*int64(i)
	}
	type stamp struct {
		t    time.Time
		real string
	}
	var stamps []stamp
	for real := range s.dates {
		t, err := time.Parse(time.RFC3339Nano, real)
		if err != nil {
			s.findings = append(s.findings, "a history time does not parse")
			continue
		}
		stamps = append(stamps, stamp{t, real})
	}
	slices.SortFunc(stamps, func(a, b stamp) int {
		return cmp.Or(a.t.Compare(b.t), strings.Compare(a.real, b.real))
	})
	for i, st := range stamps {
		s.dates[st.real] = syntheticDateBase.Add(time.Duration(i) * time.Hour).Format(time.RFC3339)
	}
	for role, m := range s.pseudo {
		for i, real := range slices.Sorted(maps.Keys(m)) {
			m[real] = role + "-" + strconv.Itoa(i+1)
		}
	}
}

// value rewrites v, of DTO type t, under the member's rule (nil for none); owners maps
// an ID role to the real id of the nearest enclosing object it identifies.
func (s *synth) value(v any, t reflect.Type, rule *Rule, member string, owners map[string]int64) any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch x := v.(type) {
	case nil:
		return nil
	case map[string]any:
		return s.object(x, t, owners)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = s.value(item, t.Elem(), rule, member, owners)
		}
		return out
	case json.Number:
		return s.number(x, rule, owners)
	case string:
		return s.str(x, rule, member)
	default:
		return v
	}
}

func (s *synth) object(obj map[string]any, t reflect.Type, owners map[string]int64) map[string]any {
	rules := s.rules[t]
	if r, ok := rules["id"]; ok && r.Kind == ID {
		if id, ok := Int(obj["id"]); ok {
			owners = maps.Clone(owners)
			owners[r.Role] = id
		}
	}
	types := fields(t)
	out := make(map[string]any, len(obj))
	for name, v := range obj {
		ft, declared := types[name]
		if !declared {
			out[name] = v
			continue
		}
		var rule *Rule
		if r, ok := rules[name]; ok {
			if r.Kind == RelPath || r.Kind == Path {
				continue
			}
			rule = &r
		}
		out[name] = s.value(v, ft, rule, name, owners)
	}
	s.paths(obj, out, rules)
	return out
}

// paths rewrites an object's RelPath and Path members together: both name the file by
// its synthetic id, and the folder is the real path with the relative path cut off.
func (s *synth) paths(obj, out map[string]any, rules map[string]Rule) {
	relName, absName, file := pathRules(rules)
	rel, _ := obj[relName].(string)
	abs, _ := obj[absName].(string)
	folder := folderOf(rel, abs)
	if s.collect {
		for _, p := range []string{rel, abs, folder} {
			s.addPseudo(privateRole, p)
		}
		s.addPseudo(pseudoFolder, folder)
		return
	}
	name := fileName(obj, out, cmp.Or(rel, abs), file.Role)
	setString(obj, out, relName, emptyOr(rel, name))
	setString(obj, out, absName, emptyOr(abs, file.Root+"/"+s.pseudo[pseudoFolder][folder]+"/"+name))
}

// pathRules returns the members a type's RelPath and Path rules name, and the rule
// naming its files: the RelPath Role (else the Path one) and the Path Root.
func pathRules(rules map[string]Rule) (rel, abs string, file Rule) {
	for name, r := range rules {
		switch r.Kind {
		case RelPath:
			rel, file.Role = name, r.Role
		case Path:
			abs, file.Root = name, r.Root
			file.Role = cmp.Or(file.Role, r.Role)
		default:
		}
	}
	return rel, abs, file
}

func folderOf(rel, abs string) string {
	switch {
	case abs == "":
		return ""
	case rel != "" && strings.HasSuffix(abs, "/"+rel):
		return strings.TrimSuffix(abs, "/"+rel)
	default:
		return path.Dir(abs)
	}
}

// fileName is [Season NN/]role-<synthetic id><ext>, the extension kept from the real
// path when it looks like one.
func fileName(obj, out map[string]any, orig, role string) string {
	ext := path.Ext(orig)
	if !extension.MatchString(ext) {
		ext = ".mkv"
	}
	id, _ := Int(out["id"])
	name := role + "-" + strconv.FormatInt(id, 10) + ext
	if season, ok := Int(obj["seasonNumber"]); ok {
		name = fmt.Sprintf("Season %02d/%s", season, name)
	}
	return name
}

// setString writes v as member name when the answer carried a string there, and
// copies a null or another scalar unchanged.
func setString(obj, out map[string]any, name, v string) {
	orig, ok := obj[name]
	if name == "" || !ok {
		return
	}
	if _, isString := orig.(string); isString {
		out[name] = v
		return
	}
	out[name] = orig
}

func emptyOr(orig, synthetic string) string {
	if orig == "" {
		return ""
	}
	return synthetic
}

// privateRole collects strings that are scanned for but never written.
const privateRole = "\x00private"

func (s *synth) addPseudo(role, orig string) {
	if orig == "" {
		return
	}
	if s.pseudo[role] == nil {
		s.pseudo[role] = map[string]string{}
	}
	s.pseudo[role][orig] = ""
}

func (s *synth) number(n json.Number, rule *Rule, owners map[string]int64) any {
	if rule == nil {
		return n
	}
	i, err := n.Int64()
	if err != nil {
		s.findings = append(s.findings, s.file+": a ruled number is not an integer")
		return n
	}
	if i == 0 {
		return n
	}
	switch rule.Kind {
	case ID:
		if s.collect {
			if s.ids[rule.Role] == nil {
				s.ids[rule.Role] = map[int64]int64{}
			}
			s.ids[rule.Role][i] = 0
			return n
		}
		return json.Number(strconv.FormatInt(s.ids[rule.Role][i], 10))
	case Size:
		if s.collect {
			s.sizes[i] = 0
			return n
		}
		return json.Number(strconv.FormatInt(s.sizes[i], 10))
	case FileCount:
		owner, ok := owners[rule.Role]
		if c, carried := s.carried[carry{s.dir, owner}]; ok && carried {
			return json.Number(strconv.Itoa(c))
		}
		return json.Number("1")
	default:
		s.findings = append(s.findings, s.file+": a number has a string rule")
		return n
	}
}

func (s *synth) str(v string, rule *Rule, member string) any {
	if v == "" {
		return v
	}
	if rule == nil {
		if public[member] {
			return v
		}
		rule = &Rule{Kind: Pseudo, Role: pseudoText}
	}
	switch rule.Kind {
	case Date:
		if s.collect {
			s.dates[v] = ""
			return v
		}
		return s.dates[v]
	case Pseudo, RootPath:
		role := rule.Role
		if rule.Kind == RootPath {
			role = pseudoRoot
		}
		if s.collect {
			s.addPseudo(role, v)
			s.addPseudo(privateRole, v)
			return v
		}
		if rule.Kind == RootPath {
			return rule.Root + "/" + s.pseudo[role][v]
		}
		return s.pseudo[role][v]
	default:
		s.findings = append(s.findings, s.file+": a string has a number rule")
		return v
	}
}

func (s *synth) counts() map[string]int {
	out := map[string]int{"size": len(s.sizes), "date": len(s.dates)}
	for role, m := range s.ids {
		out["id:"+role] = len(m)
	}
	for role, m := range s.pseudo {
		if role != privateRole {
			out["pseudo:"+role] = len(m)
		}
	}
	return out
}

// privateStrings are the real strings the run replaced.
func (s *synth) privateStrings() []string {
	n := 0
	for _, m := range s.pseudo {
		n += len(m)
	}
	out := make([]string, 0, n)
	for _, m := range s.pseudo {
		out = append(out, slices.Collect(maps.Keys(m))...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (s *synth) realSizes() []string {
	out := make([]string, 0, len(s.sizes))
	for real := range s.sizes {
		out = append(out, strconv.FormatInt(real, 10))
	}
	return out
}

func (s *synth) realDates() []string { return slices.Collect(maps.Keys(s.dates)) }
