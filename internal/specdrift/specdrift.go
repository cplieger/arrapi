// Package specdrift checks arrapi's wire DTOs and the requests its engines send against
// the OpenAPI document each upstream project generates from its own controllers. Go's
// JSON decoding ignores a missing field, so an upstream rename would otherwise decode to
// a zero value instead of failing. Load fetches a Contract's document, falling back to
// this repository's schema-mirror copy with a logged WARNING (shown under -v or when a
// test fails); CheckDTO pins tag presence and wire kind, recursing through each $ref;
// CheckRequest pins path, method, query keys and enum values; CheckMirrored pins the
// workflow line refreshing each mirror copy. Only the service packages' drift tests
// import it.
package specdrift

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// maxSpecBytes bounds one document download; the documents are about 350 KB.
const maxSpecBytes = 16 << 20

const fetchAttempts = 3

// The JSON kinds wireKind and kind name; an array is arrayOf followed by its element's.
const (
	kindString   = "string"
	kindNumber   = "number"
	kindBoolean  = "boolean"
	kindObject   = "object"
	kindAny      = "any"
	kindComposed = "composed"
	arrayOf      = "array of "
	decoderOf    = "decoder "
)

const refPrefix = "#/components/schemas/"

// Contract names one upstream OpenAPI document and its copy on the schema-mirror branch,
// which a mirror line in .github/workflows/schema-mirror.yaml refreshes.
type Contract struct {
	Name   string // the server shape, in every failure: "Sonarr v4 (API v3)"
	Repo   string // owner/repo on GitHub
	Branch string // a named branch; HEAD would follow whatever the default branch is
	Path   string // the document's path in Repo
	Mirror string // the file name on cplieger/arrapi's schema-mirror branch
}

// URL is the document on its branch.
func (c *Contract) URL() string {
	return "https://raw.githubusercontent.com/" + c.Repo + "/" + c.Branch + "/" + c.Path
}

// MirrorURL is the last-known-good copy the mirror workflow keeps.
func (c *Contract) MirrorURL() string {
	return "https://raw.githubusercontent.com/cplieger/arrapi/schema-mirror/" + c.Mirror
}

// MirrorInvocation is the schema-mirror.yaml run-script line that refreshes c's mirror copy.
func (c *Contract) MirrorInvocation() string {
	return "mirror " + c.Mirror + " " + c.Repo + " " + c.Branch + " " + c.Path
}

// CheckMirrored reports, in argument order, each contract whose MirrorInvocation is not
// exactly one line of workflow once surrounding whitespace is trimmed, so a commented-out,
// edited or repeated invocation fails.
func CheckMirrored(workflow string, contracts ...*Contract) []string {
	var problems []string
	lines := strings.Split(workflow, "\n")
	for _, c := range contracts {
		want := c.MirrorInvocation()
		n := 0
		for _, l := range lines {
			if strings.TrimSpace(l) == want {
				n++
			}
		}
		if n != 1 {
			problems = append(problems, fmt.Sprintf("%s: schema-mirror.yaml has %d active lines %q, want exactly 1", c.Name, n, want))
		}
	}
	return problems
}

// Kinds names the JSON kind each type that decodes through its own UnmarshalJSON reads,
// since its Go kind says nothing about the wire. time.Time and json.RawMessage need no
// entry.
type Kinds map[reflect.Type]string

// Doc is the slice of an OpenAPI 3.0 document the checks read.
type Doc struct {
	Paths      map[string]*pathItem `json:"paths"`
	Components struct {
		Schemas map[string]*schema `json:"schemas"`
	} `json:"components"`
	Info struct {
		Description string `json:"description"`
	} `json:"info"`
	contract string
}

type pathItem struct {
	Get        *operation  `json:"get"`
	Put        *operation  `json:"put"`
	Post       *operation  `json:"post"`
	Delete     *operation  `json:"delete"`
	Parameters []parameter `json:"parameters"`
}

type operation struct {
	Parameters []parameter `json:"parameters"`
}

type parameter struct {
	Schema *schema `json:"schema"`
	Name   string  `json:"name"`
	In     string  `json:"in"`
}

type schema struct {
	Items      *schema            `json:"items"`
	Properties map[string]*schema `json:"properties"`
	Ref        string             `json:"$ref"`
	Type       string             `json:"type"`
	Enum       []any              `json:"enum"`
	AllOf      []*schema          `json:"allOf"`
	OneOf      []*schema          `json:"oneOf"`
	AnyOf      []*schema          `json:"anyOf"`
}

var cache = struct {
	docs map[Contract]*Doc
	errs map[Contract]error
	mu   sync.Mutex
}{docs: map[Contract]*Doc{}, errs: map[Contract]error{}}

// Load returns c's document, downloaded once per test binary: upstream first, then the
// mirror with a logged WARNING. It skips t under -short and fails it only when both
// sources fail.
func Load(t testing.TB, c *Contract) *Doc {
	t.Helper()
	if testing.Short() {
		t.Skip("the drift guard downloads the upstream OpenAPI documents; skipped with -short")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if d, ok := cache.docs[*c]; ok {
		return d
	}
	if err, ok := cache.errs[*c]; ok {
		t.Fatal(err)
	}
	d, warning, err := fetchDoc(t.Context(), c.Name, c.URL(), c.MirrorURL(), 2*time.Second)
	if warning != "" {
		t.Log(warning)
	}
	if err != nil {
		cache.errs[*c] = err
		t.Fatal(err)
	}
	cache.docs[*c] = d
	return d
}

// fetchDoc tries upstream, then mirror, each up to fetchAttempts times with a linear
// backoff, and returns the warning to log when it fell back.
func fetchDoc(ctx context.Context, name, upstream, mirror string, backoff time.Duration) (*Doc, string, error) {
	d, err := download(ctx, upstream, backoff)
	if err == nil {
		d.contract = name
		return d, "", nil
	}
	warning := fmt.Sprintf("WARNING: %s document %s unavailable (%v); using the schema-mirror copy, which may lag upstream", name, upstream, err)
	d, mirrorErr := download(ctx, mirror, backoff)
	if mirrorErr != nil {
		return nil, warning, fmt.Errorf("%s document unavailable: upstream %s: %w; mirror %s: %w", name, upstream, err, mirror, mirrorErr)
	}
	d.contract = name
	return d, warning, nil
}

func download(ctx context.Context, src string, backoff time.Duration) (*Doc, error) {
	client := &http.Client{Timeout: time.Minute}
	var err error
	for attempt := range fetchAttempts {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt) * backoff)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}
		var d *Doc
		if d, err = fetchOnce(ctx, client, src); err == nil {
			return d, nil
		}
	}
	return nil, err
}

func fetchOnce(ctx context.Context, client *http.Client, src string) (*Doc, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "arrapi-schemadrift-test (+https://github.com/cplieger/arrapi)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSpecBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSpecBytes {
		return nil, fmt.Errorf("document exceeds %d bytes", maxSpecBytes)
	}
	return decode(body)
}

func decode(body []byte) (*Doc, error) {
	var d Doc
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(d.Components.Schemas) == 0 || len(d.Paths) == 0 {
		return nil, errors.New("document carries no schemas or paths; upstream moved or emptied it")
	}
	return &d, nil
}

// Description is the document's info.description.
func (d *Doc) Description() string { return d.Info.Description }

// Declares reports whether schemaName declares property; an undeclared schema is an error.
func (d *Doc) Declares(schemaName, property string) (bool, error) {
	s, ok := d.Components.Schemas[schemaName]
	if !ok {
		return false, fmt.Errorf("%s declares no schema %q", d.contract, schemaName)
	}
	_, ok = s.Properties[property]
	return ok, nil
}

// PropertyEnum returns the string enum property of schemaName declares, through $ref.
func (d *Doc) PropertyEnum(schemaName, property string) ([]string, error) {
	s, ok := d.Components.Schemas[schemaName]
	if !ok {
		return nil, fmt.Errorf("%s declares no schema %q", d.contract, schemaName)
	}
	p, ok := s.Properties[property]
	if !ok {
		return nil, fmt.Errorf("%s: %s declares no property %q", d.contract, schemaName, property)
	}
	if _, values := d.enum(p); values != nil {
		return values, nil
	}
	return nil, fmt.Errorf("%s: %s.%s declares no enum", d.contract, schemaName, property)
}

// QueryParams returns the query parameter names declared for method on the path
// template, path-level and operation-level merged.
func (d *Doc) QueryParams(method, path string) (map[string]bool, error) {
	item, ok := d.Paths[path]
	if !ok {
		return nil, fmt.Errorf("%s declares no path %q", d.contract, path)
	}
	params, err := d.queryParams(item, method, path)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(params))
	for name := range params {
		names[name] = true
	}
	return names, nil
}

func (d *Doc) queryParams(item *pathItem, method, path string) (map[string]*schema, error) {
	var op *operation
	switch method {
	case http.MethodGet:
		op = item.Get
	case http.MethodPut:
		op = item.Put
	case http.MethodPost:
		op = item.Post
	case http.MethodDelete:
		op = item.Delete
	}
	if op == nil {
		return nil, fmt.Errorf("%s declares no %s on %s: the upstream method changed", d.contract, method, path)
	}
	params := map[string]*schema{}
	for _, p := range slices.Concat(item.Parameters, op.Parameters) {
		if p.In == "query" {
			params[p.Name] = p.Schema
		}
	}
	return params, nil
}

// CheckRequest reports how a request arrapi sends (a path with its query, or a full URL)
// departs from the document: no path template matches it, the template declares no such
// method or query key, or a value is outside the enum its parameter declares.
func (d *Doc) CheckRequest(method, uri string) []string {
	u, err := url.Parse(uri)
	if err != nil {
		return []string{fmt.Sprintf("%s: request %q does not parse: %v", d.contract, uri, err)}
	}
	path, item := d.match(u.Path)
	if item == nil {
		return []string{fmt.Sprintf("%s declares no path matching %s: the upstream endpoint moved", d.contract, u.Path)}
	}
	params, err := d.queryParams(item, method, path)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	query := u.Query()
	for _, key := range slices.Sorted(maps.Keys(query)) {
		s, ok := params[key]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s declares no %q query parameter on %s %s: the upstream parameter was renamed or removed", d.contract, key, method, path))
			continue
		}
		name, values := d.enum(s)
		if values == nil {
			continue
		}
		for _, v := range query[key] {
			if !slices.Contains(values, v) {
				problems = append(problems, fmt.Sprintf("%s: %s %s: %s=%s is not a value of %s: %v", d.contract, method, path, key, v, name, values))
			}
		}
	}
	return problems
}

// match returns the path template matching concrete, preferring the most literal
// segments, so /thing/latest beats /thing/{id}.
func (d *Doc) match(concrete string) (string, *pathItem) {
	want := strings.Split(concrete, "/")
	best, bestScore := "", -1
	for _, tmpl := range slices.Sorted(maps.Keys(d.Paths)) {
		segs := strings.Split(tmpl, "/")
		if len(segs) != len(want) {
			continue
		}
		score := 0
		for i, s := range segs {
			switch {
			case s == want[i]:
				score++
			case strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") && want[i] != "":
			default:
				score = -1
			}
			if score < 0 {
				break
			}
		}
		if score > bestScore {
			best, bestScore = tmpl, score
		}
	}
	if bestScore < 0 {
		return "", nil
	}
	return best, d.Paths[best]
}

// enum resolves s through $ref and array items to a string enum and the name of the
// schema declaring it; values is nil when there is none.
func (d *Doc) enum(s *schema) (name string, values []string) {
	for seen := map[string]bool{}; s != nil; {
		switch {
		case s.Items != nil:
			s = s.Items
		case s.Ref != "":
			name = strings.TrimPrefix(s.Ref, refPrefix)
			if seen[name] {
				return "", nil
			}
			seen[name] = true
			s = d.Components.Schemas[name]
		case len(s.Enum) > 0:
			values = make([]string, 0, len(s.Enum))
			for _, v := range s.Enum {
				values = append(values, fmt.Sprint(v))
			}
			return name, values
		default:
			return "", nil
		}
	}
	return "", nil
}

// CheckDTO reports every JSON tag of dto, and of each struct it reaches through a field,
// that schemaName (or the schema the property references) does not declare, or declares
// with another JSON kind. The reverse is unchecked: a DTO decodes a subset by design.
func (d *Doc) CheckDTO(schemaName string, dto reflect.Type, kinds Kinds) []string {
	s, ok := d.Components.Schemas[schemaName]
	if !ok {
		return []string{fmt.Sprintf("%s declares no schema %q: the upstream resource was renamed or removed", d.contract, schemaName)}
	}
	c := checker{d: d, kinds: kinds, seen: map[visit]bool{}}
	c.check(dto.Name(), schemaName, s, dto)
	return c.problems
}

type visit struct {
	typ    reflect.Type
	schema *schema
}

type checker struct {
	d        *Doc
	kinds    Kinds
	seen     map[visit]bool
	problems []string
}

func (c *checker) fail(format string, args ...any) {
	c.problems = append(c.problems, c.d.contract+": "+fmt.Sprintf(format, args...))
}

func (c *checker) check(at, schemaName string, s *schema, typ reflect.Type) {
	if c.seen[visit{typ, s}] {
		return
	}
	c.seen[visit{typ, s}] = true
	fields := jsonFields(typ)
	if len(fields) == 0 {
		c.fail("%s declares no JSON tags", at)
		return
	}
	for _, tag := range slices.Sorted(maps.Keys(fields)) {
		ft, where := fields[tag], at+"."+tag
		prop, ok := s.Properties[tag]
		if !ok {
			c.fail("%s: %s declares no property %q: the upstream schema renamed or removed it", where, schemaName, tag)
			continue
		}
		local, spec := c.wireKind(ft), c.d.kind(prop, map[string]bool{})
		if decoder, ok := strings.CutPrefix(local, decoderOf); ok {
			c.fail("%s decodes through %s's UnmarshalJSON: name the JSON kind it reads in Kinds", where, decoder)
			continue
		}
		if !compatible(local, spec) {
			c.fail("%s decodes JSON %s but %s.%s is %s: the upstream field changed type", where, local, schemaName, tag, spec)
			continue
		}
		c.descend(where, schemaName+"."+tag, prop, ft)
	}
}

// descend checks the struct a field of type ft decodes, if any, against the object
// schema prop resolves to; inline names an inline object schema in failures.
func (c *checker) descend(where, inline string, prop *schema, ft reflect.Type) {
	elem, ok := c.structElem(ft)
	if !ok {
		return
	}
	name, target := c.d.object(prop)
	if name == "" {
		name = inline
	}
	if len(target.Properties) == 0 {
		c.fail("%s: %s declares no properties to check %s against", where, name, elem.Name())
		return
	}
	c.check(where, name, target, elem)
}

// object resolves prop through array items and $ref to the object schema a nested DTO
// decodes, and the component name when it is one.
func (d *Doc) object(prop *schema) (string, *schema) {
	name := ""
	for seen := map[string]bool{}; ; {
		switch {
		case prop.Items != nil:
			prop = prop.Items
		case prop.Ref != "":
			name = strings.TrimPrefix(prop.Ref, refPrefix)
			target, ok := d.Components.Schemas[name]
			if !ok || seen[name] {
				return name, &schema{}
			}
			seen[name] = true
			prop = target
		default:
			return name, prop
		}
	}
}

func jsonFields(typ reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	for f := range typ.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			fields[name] = f.Type
		}
	}
	return fields
}

var unmarshaler = reflect.TypeFor[json.Unmarshaler]()

func (c *checker) custom(t reflect.Type) (string, bool) {
	if k, ok := c.kinds[t]; ok {
		return k, true
	}
	switch t {
	case reflect.TypeFor[time.Time]():
		return kindString, true
	case reflect.TypeFor[json.RawMessage]():
		return kindAny, true
	}
	if t.Implements(unmarshaler) || reflect.PointerTo(t).Implements(unmarshaler) {
		return decoderOf + t.String(), true
	}
	return "", false
}

// wireKind names the JSON kind t decodes from: string, number, boolean, object, any, or
// "array of" one of them.
func (c *checker) wireKind(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if k, ok := c.custom(t); ok {
		return k
	}
	switch t.Kind() {
	case reflect.Bool:
		return kindBoolean
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return kindNumber
	case reflect.String:
		return kindString
	case reflect.Slice, reflect.Array:
		return arrayOf + c.wireKind(t.Elem())
	case reflect.Map, reflect.Struct:
		return kindObject
	case reflect.Interface:
		return kindAny
	default:
		return t.Kind().String()
	}
}

// structElem returns the struct a field of type t decodes element by element, unless a
// decoder of its own or a stdlib type decodes it.
func (c *checker) structElem(t reflect.Type) (reflect.Type, bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if _, custom := c.custom(t); custom || t.Kind() != reflect.Struct {
		return nil, false
	}
	return t, true
}

// kind names the JSON kind s describes, resolving $ref; composed is a schema built from
// allOf, oneOf or anyOf, which the checks cannot follow.
func (d *Doc) kind(s *schema, seen map[string]bool) string {
	if s == nil {
		return kindAny
	}
	if len(s.AllOf)+len(s.OneOf)+len(s.AnyOf) > 0 {
		return kindComposed
	}
	if s.Ref != "" {
		name := strings.TrimPrefix(s.Ref, refPrefix)
		target, ok := d.Components.Schemas[name]
		if !ok || seen[name] {
			return kindObject
		}
		seen[name] = true
		return d.kind(target, seen)
	}
	switch s.Type {
	case "string":
		return kindString
	case "integer", "number":
		return kindNumber
	case "boolean":
		return kindBoolean
	case "array":
		return arrayOf + d.kind(s.Items, seen)
	case "object":
		return kindObject
	}
	if len(s.Properties) > 0 {
		return kindObject
	}
	return kindAny
}

// compatible reports whether local and spec decode the same JSON kind; "any" on either
// side matches every kind, element-wise for arrays.
func compatible(local, spec string) bool {
	for {
		if local == kindAny || spec == kindAny {
			return true
		}
		l, lArray := strings.CutPrefix(local, arrayOf)
		s, sArray := strings.CutPrefix(spec, arrayOf)
		if lArray != sArray {
			return false
		}
		if !lArray {
			return local == spec
		}
		local, spec = l, s
	}
}
