package specdrift

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testDoc = `{
  "info": {"description": "Example API docs - The v9 API docs apply to Example v9 only."},
  "paths": {
    "/api/v9/thing": {
      "parameters": [{"name": "shared", "in": "query", "schema": {"type": "boolean"}}],
      "get": {"parameters": [
        {"name": "flag", "in": "query", "schema": {"type": "boolean"}},
        {"name": "include", "in": "query", "schema": {"type": "array", "items": {"$ref": "#/components/schemas/Sub"}}},
        {"name": "kind", "in": "query", "schema": {"$ref": "#/components/schemas/Tok"}},
        {"name": "id", "in": "path", "schema": {"type": "integer"}}
      ]}
    },
    "/api/v9/thing/{id}": {"get": {"parameters": [{"name": "id", "in": "path"}]}, "post": {}},
    "/api/v9/thing/latest": {"get": {"parameters": [{"name": "only", "in": "query"}]}}
  },
  "components": {"schemas": {
    "Thing": {"type": "object", "properties": {
      "id": {"type": "integer"},
      "name": {"type": "string", "nullable": true},
      "when": {"type": "string", "format": "date-time"},
      "child": {"$ref": "#/components/schemas/Child"},
      "kids": {"type": "array", "items": {"$ref": "#/components/schemas/Child"}, "nullable": true},
      "inline": {"type": "object", "properties": {"n": {"type": "integer"}}},
      "token": {"$ref": "#/components/schemas/Tok"},
      "raw": {"type": "string"},
      "loose": {"type": "object"},
      "mixed": {"allOf": [{"$ref": "#/components/schemas/Child"}]}
    }},
    "Child": {"type": "object", "properties": {"n": {"type": "integer"}}},
    "Tok": {"enum": ["unknown", "a", "b"], "type": "string"},
    "Sub": {"enum": ["x", "y"], "type": "string"}
  }}
}`

func parse(t *testing.T) *Doc {
	t.Helper()
	d, err := decode([]byte(testDoc))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	d.contract = "Example v9 (API v9)"
	return d
}

// tok decodes through its own UnmarshalJSON, so its Go kind says nothing about the wire.
type tok int

func (*tok) UnmarshalJSON([]byte) error { return nil }

var _ json.Unmarshaler = (*tok)(nil)

type child struct {
	N int `json:"n"`
}

type badChild struct {
	N string `json:"n"`
}

type thing struct {
	When  time.Time       `json:"when"`
	Child *child          `json:"child"`
	Raw   json.RawMessage `json:"raw"`
	Name  string          `json:"name"`
	Kids  []child         `json:"kids"`
	ID    int             `json:"id"`
	Skip  int             `json:"-"`
	Plain int
}

func wantProblems(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("problems = %q, want %d naming %q", got, len(want), want)
	}
	for i, w := range want {
		if !strings.Contains(got[i], w) {
			t.Errorf("problem %d = %q, want it to contain %q", i, got[i], w)
		}
	}
}

func TestCheckDTO(t *testing.T) {
	d := parse(t)
	t.Run("a matching DTO", func(t *testing.T) {
		wantProblems(t, d.CheckDTO("Thing", reflect.TypeFor[thing](), nil))
	})
	t.Run("a tag the schema does not declare", func(t *testing.T) {
		type dto struct {
			Gone int `json:"gone"`
		}
		wantProblems(t, d.CheckDTO("Thing", reflect.TypeFor[dto](), nil),
			`Example v9 (API v9): dto.gone: Thing declares no property "gone"`)
	})
	t.Run("another wire kind", func(t *testing.T) {
		type dto struct {
			ID string `json:"id"`
		}
		wantProblems(t, d.CheckDTO("Thing", reflect.TypeFor[dto](), nil),
			`dto.id decodes JSON string but Thing.id is number`)
	})
	t.Run("a nested DTO is checked against the schema its property references", func(t *testing.T) {
		type dto struct {
			Child *badChild `json:"child"`
		}
		wantProblems(t, d.CheckDTO("Thing", reflect.TypeFor[dto](), nil),
			`dto.child.n decodes JSON string but Child.n is number`)
	})
	t.Run("an array element DTO is checked against its items' schema", func(t *testing.T) {
		type dto struct {
			Kids []badChild `json:"kids"`
		}
		wantProblems(t, d.CheckDTO("Thing", reflect.TypeFor[dto](), nil),
			`dto.kids.n decodes JSON string but Child.n is number`)
	})
	t.Run("an inline object schema is checked too", func(t *testing.T) {
		type dto struct {
			Inline badChild `json:"inline"`
		}
		wantProblems(t, d.CheckDTO("Thing", reflect.TypeFor[dto](), nil),
			`dto.inline.n decodes JSON string but Thing.inline.n is number`)
	})
	t.Run("a struct against an object with no properties", func(t *testing.T) {
		type dto struct {
			Loose child `json:"loose"`
		}
		wantProblems(t, d.CheckDTO("Thing", reflect.TypeFor[dto](), nil),
			`dto.loose: Thing.loose declares no properties`)
	})
	t.Run("a composed schema", func(t *testing.T) {
		type dto struct {
			Mixed child `json:"mixed"`
		}
		wantProblems(t, d.CheckDTO("Thing", reflect.TypeFor[dto](), nil),
			`dto.mixed decodes JSON object but Thing.mixed is composed`)
	})
	t.Run("a custom decoder whose kind is not named", func(t *testing.T) {
		type dto struct {
			Token tok `json:"token"`
		}
		wantProblems(t, d.CheckDTO("Thing", reflect.TypeFor[dto](), nil),
			`dto.token decodes through specdrift.tok's UnmarshalJSON: name the JSON kind it reads`)
	})
	t.Run("a custom decoder named with its kind", func(t *testing.T) {
		type dto struct {
			Token tok `json:"token"`
		}
		kinds := Kinds{reflect.TypeFor[tok](): "string"}
		wantProblems(t, d.CheckDTO("Thing", reflect.TypeFor[dto](), kinds))
		kinds[reflect.TypeFor[tok]()] = "number"
		wantProblems(t, d.CheckDTO("Thing", reflect.TypeFor[dto](), kinds),
			`dto.token decodes JSON number but Thing.token is string`)
	})
	t.Run("a schema the document does not declare", func(t *testing.T) {
		wantProblems(t, d.CheckDTO("Gone", reflect.TypeFor[thing](), nil),
			`Example v9 (API v9) declares no schema "Gone"`)
	})
	t.Run("a DTO without tags", func(t *testing.T) {
		type dto struct{ Plain int }
		wantProblems(t, d.CheckDTO("Thing", reflect.TypeFor[dto](), nil), `dto declares no JSON tags`)
	})
}

func TestCheckRequest(t *testing.T) {
	d := parse(t)
	for name, tc := range map[string]struct {
		method, uri string
		want        []string
	}{
		"declared parameters":             {"GET", "/api/v9/thing?flag=true&shared=1&kind=a", nil},
		"an enum value of an array param": {"GET", "/api/v9/thing?include=x&include=y", nil},
		"a templated path":                {"GET", "/api/v9/thing/41", nil},
		"a literal path beats a template": {"GET", "/api/v9/thing/latest?only=1", nil},
		"a full URL":                      {"GET", "http://host.test/api/v9/thing/41", nil},
		"an undeclared parameter":         {"GET", "/api/v9/thing?other=1", []string{`declares no "other" query parameter on GET /api/v9/thing`}},
		"a path parameter used as query":  {"GET", "/api/v9/thing?id=1", []string{`declares no "id" query parameter`}},
		"a value outside the enum":        {"GET", "/api/v9/thing?include=z", []string{`include=z is not a value of Sub: [x y]`}},
		"a value outside a ref enum":      {"GET", "/api/v9/thing?kind=c", []string{`kind=c is not a value of Tok`}},
		"an undeclared method":            {"POST", "/api/v9/thing/latest", []string{`declares no POST on /api/v9/thing/latest`}},
		"an undeclared path":              {"GET", "/api/v9/other", []string{`Example v9 (API v9) declares no path matching /api/v9/other`}},
		"a longer path":                   {"GET", "/api/v9/thing/41/x", []string{`declares no path matching`}},
	} {
		t.Run(name, func(t *testing.T) {
			wantProblems(t, d.CheckRequest(tc.method, tc.uri), tc.want...)
		})
	}
}

func TestPropertyEnum(t *testing.T) {
	d := parse(t)
	got, err := d.PropertyEnum("Thing", "token")
	if err != nil || !slices.Equal(got, []string{"unknown", "a", "b"}) {
		t.Errorf("PropertyEnum(Thing.token) = (%q, %v), want [unknown a b]", got, err)
	}
	for _, c := range [][2]string{{"Thing", "name"}, {"Thing", "gone"}, {"Gone", "token"}} {
		if got, err := d.PropertyEnum(c[0], c[1]); err == nil {
			t.Errorf("PropertyEnum(%s.%s) = %q, want an error", c[0], c[1], got)
		}
	}
}

func TestQueryParamsAndDeclares(t *testing.T) {
	d := parse(t)
	got, err := d.QueryParams("GET", "/api/v9/thing")
	if err != nil || !got["shared"] || !got["flag"] || !got["include"] || got["id"] {
		t.Errorf("QueryParams = (%v, %v), want shared, flag, include and kind, never the path id", got, err)
	}
	if _, err := d.QueryParams("POST", "/api/v9/thing"); err == nil {
		t.Error("QueryParams(POST) found an undeclared operation")
	}
	if _, err := d.QueryParams("GET", "/api/v9/gone"); err == nil {
		t.Error("QueryParams found an undeclared path")
	}
	if ok, err := d.Declares("Thing", "name"); err != nil || !ok {
		t.Errorf("Declares(Thing.name) = (%v, %v), want true", ok, err)
	}
	if ok, err := d.Declares("Thing", "gone"); err != nil || ok {
		t.Errorf("Declares(Thing.gone) = (%v, %v), want false", ok, err)
	}
	if _, err := d.Declares("Gone", "name"); err == nil {
		t.Error("Declares on an undeclared schema returned no error")
	}
	if !strings.Contains(d.Description(), "Example v9 only") {
		t.Errorf("Description = %q", d.Description())
	}
}

// server answers each request with the next of codes (repeating the last) and body.
func server(t *testing.T, body string, codes ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := int(n.Add(1)) - 1
		w.WriteHeader(codes[min(i, len(codes)-1)])
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s, &n
}

func TestFetchDoc(t *testing.T) {
	const name = "Example v9 (API v9)"
	t.Run("upstream answers", func(t *testing.T) {
		up, _ := server(t, testDoc, 200)
		mirror, hits := server(t, testDoc, 200)
		d, warning, err := fetchDoc(t.Context(), name, up.URL, mirror.URL, 0)
		if err != nil || d == nil || warning != "" || hits.Load() != 0 {
			t.Fatalf("fetchDoc = (%v, %q, %v), mirror hits %d; want the upstream document, no warning", d, warning, err, hits.Load())
		}
		if ok, _ := d.Declares("Thing", "id"); !ok || d.contract != name {
			t.Errorf("document not parsed or not named: %q", d.contract)
		}
	})
	t.Run("upstream fails, the mirror answers, loudly", func(t *testing.T) {
		up, upHits := server(t, "", 500)
		mirror, _ := server(t, testDoc, 200)
		d, warning, err := fetchDoc(t.Context(), name, up.URL, mirror.URL, 0)
		if err != nil || d == nil {
			t.Fatalf("fetchDoc = (%v, %v), want the mirror's document", d, err)
		}
		if upHits.Load() != 3 {
			t.Errorf("upstream asked %d times, want 3", upHits.Load())
		}
		for _, w := range []string{"WARNING", name, up.URL, "schema-mirror"} {
			if !strings.Contains(warning, w) {
				t.Errorf("warning %q does not name %q", warning, w)
			}
		}
	})
	t.Run("a transient upstream failure is retried", func(t *testing.T) {
		up, upHits := server(t, testDoc, 503, 200)
		mirror, _ := server(t, "", 500)
		if _, warning, err := fetchDoc(t.Context(), name, up.URL, mirror.URL, 0); err != nil || warning != "" || upHits.Load() != 2 {
			t.Errorf("fetchDoc = (%q, %v) after %d upstream requests, want the second answer", warning, err, upHits.Load())
		}
	})
	t.Run("an answer that is no OpenAPI document falls back", func(t *testing.T) {
		for body, want := range map[string]string{
			"<!doctype html><p>moved</p>":              "parse",
			`{"paths":{},"components":{"schemas":{}}}`: "no schemas or paths",
		} {
			up, _ := server(t, body, 200)
			mirror, _ := server(t, testDoc, 200)
			if d, warning, err := fetchDoc(t.Context(), name, up.URL, mirror.URL, 0); err != nil || d == nil || !strings.Contains(warning, want) {
				t.Errorf("body %q: fetchDoc = (%q, %v), want the mirror with a warning naming %q", body, warning, err, want)
			}
		}
	})
	t.Run("an oversized document is refused", func(t *testing.T) {
		up, _ := server(t, strings.Repeat(" ", maxSpecBytes+1), 200)
		mirror, _ := server(t, "", 404)
		_, _, err := fetchDoc(t.Context(), name, up.URL, mirror.URL, 0)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Errorf("err = %v, want an oversize refusal", err)
		}
	})
	t.Run("both fail", func(t *testing.T) {
		up, _ := server(t, "", 500)
		mirror, _ := server(t, "", 404)
		_, _, err := fetchDoc(t.Context(), name, up.URL, mirror.URL, 0)
		if err == nil {
			t.Fatal("fetchDoc returned no error")
		}
		for _, w := range []string{name, up.URL, mirror.URL, "status 500", "status 404"} {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("err %q does not name %q", err, w)
			}
		}
	})
}

// The workflow's run script calls mirror() once per contract as a bare line
// (schema-mirror.yaml), and the guard holds it to that form: exactly one uncommented line
// equal to "mirror <file> <repo> <branch> <path>" once surrounding whitespace is trimmed,
// so a commented-out, edited, extended or repeated invocation fails.
func TestCheckMirrored(t *testing.T) {
	a := &Contract{Name: "Example v9 (API v9)", Repo: "Owner/Repo", Branch: "main-line", Path: "src/api/openapi.json", Mirror: "a.json"}
	b := &Contract{Name: "Other (API v1)", Repo: "Other/Repo", Branch: "develop", Path: "src/other.json", Mirror: "b.json"}
	const lineA = "mirror a.json Owner/Repo main-line src/api/openapi.json"
	const lineB = "mirror b.json Other/Repo develop src/other.json"
	if got := a.MirrorInvocation(); got != lineA {
		t.Errorf("MirrorInvocation = %q, want %q", got, lineA)
	}
	for _, tc := range []struct {
		name     string
		workflow string
		failing  []string // the contract names CheckMirrored must report
	}{
		{"both active and indented", "run: |\n  mirror() { :; }\n          " + lineA + "\n          " + lineB + "\n", nil},
		{"commented out", "# " + lineA + "\n" + lineB + "\n", []string{a.Name}},
		{"commented out after indentation", "          #" + lineA + "\n" + lineB + "\n", []string{a.Name}},
		{"file suffixed", lineA + ".old\n" + lineB + "\n", []string{a.Name}},
		{"inside another command", "echo " + lineA + "\n" + lineB + "\n", []string{a.Name}},
		{"trailing argument", lineA + " extra\n" + lineB + "\n", []string{a.Name}},
		{"absent", lineB + "\n", []string{a.Name}},
		{"twice", lineA + "\n" + lineA + "\n" + lineB + "\n", []string{a.Name}},
		{"both missing", "steps: []\n", []string{a.Name, b.Name}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := CheckMirrored(tc.workflow, a, b)
			if len(problems) != len(tc.failing) {
				t.Fatalf("problems = %q, want one naming each of %q", problems, tc.failing)
			}
			for i, name := range tc.failing {
				if !strings.Contains(problems[i], name) {
					t.Errorf("problem %d = %q, want it to name %q", i, problems[i], name)
				}
			}
		})
	}
}

func TestContractURLs(t *testing.T) {
	c := Contract{Name: "x", Repo: "Owner/Repo", Branch: "main-line", Path: "src/api/openapi.json", Mirror: "repo-openapi.json"}
	if got, want := c.URL(), "https://raw.githubusercontent.com/Owner/Repo/main-line/src/api/openapi.json"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	if got, want := c.MirrorURL(), "https://raw.githubusercontent.com/cplieger/arrapi/schema-mirror/repo-openapi.json"; got != want {
		t.Errorf("MirrorURL = %q, want %q", got, want)
	}
}
