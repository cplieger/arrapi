package arrapi_test

// Schema-drift guard.
//
// arrapi hand-curates its DTO field subsets (types.go, history.go, command.go)
// against the Sonarr/Radarr v3 wire format, and Go's JSON decoding ignores
// unknown or missing fields — so an upstream field rename or removal would
// silently decode to a zero value instead of failing. These tests pin the
// curated surface against the OFFICIAL OpenAPI documents both projects
// generate from their own controllers and commit in their repositories
// (src/{Sonarr,Radarr}.Api.V3/openapi.json), in three layers:
//
//  1. Tag presence (TestUpstreamSchemaDrift): every curated JSON tag must
//     still exist in the official schema — a dropped or renamed field fails
//     the next CI run instead of decoding silently to a zero value.
//  2. Wire-kind compatibility (TestUpstreamSchemaDrift): the curated field
//     and the official schema property must decode the same JSON wire kind
//     (string, number, boolean, array, object) — an upstream type change
//     fails the same run instead of first surfacing at runtime as an
//     *json.UnmarshalTypeError*. Deliberate divergences are recorded in
//     wireKindExceptions with a reason.
//  3. Endpoint pins (TestUpstreamEndpointDrift): the request paths and query
//     parameter names arrapi hand-builds are pinned against the official
//     documents' paths section — an upstream endpoint move or parameter
//     rename fails too.
//
// The documents are downloaded at test time from each project's default
// branch (raw.githubusercontent.com HEAD, which survives a branch rename), so
// the guard always checks the LATEST upstream contract: no committed
// snapshot, no third-party generated client, and nothing for Renovate to
// track. HEAD follows upstream development, so a rename is seen before it
// ships in a release. When the upstream fetch fails after retries (an outage,
// an upstream file move), the guard falls back — loudly — to the
// last-known-good copies on this repository's schema-mirror branch (refreshed
// daily by .github/workflows/schema-mirror.yaml), so upstream unavailability
// never reddens CI over a contract that has not actually changed. The cost is
// a network dependency: `go test -short` skips these tests for offline runs,
// and only when BOTH sources fail does the suite fail loudly rather than pass
// vacuously.
//
// The reverse direction is deliberately unchecked: the official schemas carry
// the full upstream resource, and arrapi's subsets are curation, not drift.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/arrapi"
)

// specURLs locates the OpenAPI document each project generates and commits in
// its own repository. HEAD resolves to the default branch on
// raw.githubusercontent.com, so the URLs keep working across upstream branch
// renames (Sonarr's default moved from develop to v5-develop; the v3 API
// document applies to v3/v4/v5 per its own description).
var specURLs = map[string]string{
	"sonarr": "https://raw.githubusercontent.com/Sonarr/Sonarr/HEAD/src/Sonarr.Api.V3/openapi.json",
	"radarr": "https://raw.githubusercontent.com/Radarr/Radarr/HEAD/src/Radarr.Api.V3/openapi.json",
}

// mirrorURLs locates the last-known-good copy of each document on this
// repository's machine-managed schema-mirror branch (see
// .github/workflows/schema-mirror.yaml). Consulted only after the upstream
// fetch fails, so the guard checks the latest contract whenever upstream is
// reachable and degrades to the newest mirrored contract when it is not.
var mirrorURLs = map[string]string{
	"sonarr": "https://raw.githubusercontent.com/cplieger/arrapi/schema-mirror/sonarr-openapi.json",
	"radarr": "https://raw.githubusercontent.com/cplieger/arrapi/schema-mirror/radarr-openapi.json",
}

// maxSpecBytes bounds the spec download (the documents are ~300 KB today).
const maxSpecBytes = 16 << 20

// specDoc is the minimal OpenAPI 3.0 slice these tests consume.
type specDoc struct {
	Paths      map[string]*specPathItem `json:"paths"`
	Components struct {
		Schemas map[string]*specSchema `json:"schemas"`
	} `json:"components"`
}

// specPathItem models one paths entry: the operations arrapi pins plus the
// path-level parameters shared by all of them.
type specPathItem struct {
	Get        *specOperation  `json:"get"`
	Put        *specOperation  `json:"put"`
	Post       *specOperation  `json:"post"`
	Delete     *specOperation  `json:"delete"`
	Parameters []specParameter `json:"parameters"`
}

func (p *specPathItem) operation(method string) *specOperation {
	switch method {
	case http.MethodGet:
		return p.Get
	case http.MethodPut:
		return p.Put
	case http.MethodPost:
		return p.Post
	case http.MethodDelete:
		return p.Delete
	default:
		return nil
	}
}

// queryParams returns the names of the query parameters declared for method,
// merging path-level and operation-level declarations.
func (p *specPathItem) queryParams(method string) map[string]bool {
	names := make(map[string]bool)
	for _, param := range p.Parameters {
		if param.In == "query" {
			names[param.Name] = true
		}
	}
	if op := p.operation(method); op != nil {
		for _, param := range op.Parameters {
			if param.In == "query" {
				names[param.Name] = true
			}
		}
	}
	return names
}

type specOperation struct {
	Parameters []specParameter `json:"parameters"`
}

type specParameter struct {
	Name string `json:"name"`
	In   string `json:"in"`
}

// specSchema is the minimal JSON-schema slice the wire-kind mapping needs.
// The arr documents use only plain types, $ref, and array items on the
// properties arrapi curates (no allOf/oneOf composition).
type specSchema struct {
	Ref        string                 `json:"$ref"`
	Type       string                 `json:"type"`
	Items      *specSchema            `json:"items"`
	Properties map[string]*specSchema `json:"properties"`
}

// specCache shares one download per service across both tests. err is
// remembered so a failed download is reported once per test, not re-fetched.
var specCache = struct {
	sync.Mutex
	docs map[string]*specDoc
	errs map[string]error
}{
	docs: make(map[string]*specDoc),
	errs: make(map[string]error),
}

// openapiSpec returns the parsed official OpenAPI document for svc,
// downloading it on first use: upstream HEAD first, then the schema-mirror
// fallback with a logged warning. Skips under -short (offline runs); fails
// the test only when both sources fail after retries.
func openapiSpec(t *testing.T, svc string) *specDoc {
	t.Helper()
	if testing.Short() {
		t.Skip("drift guard downloads the official OpenAPI documents; skipped with -short")
	}

	specCache.Lock()
	defer specCache.Unlock()
	if doc, ok := specCache.docs[svc]; ok {
		return doc
	}
	if err, ok := specCache.errs[svc]; ok {
		t.Fatalf("official %s OpenAPI document unavailable: %v", svc, err)
	}

	doc, err := downloadSpec(t.Context(), specURLs[svc])
	if err != nil {
		t.Logf("WARNING: upstream %s OpenAPI document %s unavailable (%v); falling back to the schema-mirror last-known-good copy, which may lag upstream HEAD", svc, specURLs[svc], err)
		var mirrorErr error
		doc, mirrorErr = downloadSpec(t.Context(), mirrorURLs[svc])
		if mirrorErr != nil {
			combined := fmt.Errorf("upstream %s: %w; mirror %s: %v", specURLs[svc], err, mirrorURLs[svc], mirrorErr)
			specCache.errs[svc] = combined
			t.Fatalf("official %s OpenAPI document unavailable: %v", svc, combined)
		}
	}
	specCache.docs[svc] = doc
	return doc
}

// downloadSpec fetches and parses one OpenAPI document with bounded reads and
// three attempts, so a transient GitHub hiccup does not fail the suite.
func downloadSpec(ctx context.Context, url string) (*specDoc, error) {
	client := &http.Client{}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			select {
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		doc, err := fetchSpecOnce(ctx, client, url)
		if err == nil {
			return doc, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func fetchSpecOnce(ctx context.Context, client *http.Client, url string) (*specDoc, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "arrapi-schemadrift-test (+https://github.com/cplieger/arrapi)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
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

	var doc specDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(doc.Components.Schemas) == 0 || len(doc.Paths) == 0 {
		return nil, fmt.Errorf("document carries no schemas/paths; upstream moved or emptied it")
	}
	return &doc, nil
}

// specRef names one schema inside one service's document.
type specRef struct {
	svc  string
	name string
}

func (r specRef) String() string { return r.svc + "." + r.name }

// schema resolves the referenced schema, failing the test when the upstream
// document no longer declares it (a renamed or removed resource).
func (r specRef) schema(t *testing.T) (*specSchema, *specDoc) {
	t.Helper()
	doc := openapiSpec(t, r.svc)
	s, ok := doc.Components.Schemas[r.name]
	if !ok {
		t.Fatalf("official %s document declares no schema %q: the upstream resource was renamed or removed", r.svc, r.name)
	}
	return s, doc
}

// jsonTagFields returns the JSON field name → Go field type mapping declared
// by a struct type's tags, excluding untagged fields and `json:"-"`.
func jsonTagFields(typ reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type, typ.NumField())
	for f := range typ.Fields() {
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			fields[name] = f.Type
		}
	}
	return fields
}

// wireKindExceptions lists curated fields whose JSON wire kind DELIBERATELY
// diverges from the official schema's declaration, keyed "LocalType.jsonTag".
// Each entry is a place where arrapi models the observed wire rather than the
// documented schema; removing one requires re-verifying the wire behavior.
var wireKindExceptions = map[string]string{
	// The official documents declare eventType as a string enum
	// (EpisodeHistoryEventType / MovieHistoryEventType), but Sonarr sends the
	// enum as an INTEGER on the wire; Radarr sends the string. arrapi's
	// EventType accepts both — see EventType.UnmarshalJSON.
	"HistoryRecord.eventType": "int-or-string decode; the documented enum is string-only but Sonarr sends an integer",
}

// wireKind classifies a Go type by the JSON wire kind it decodes from,
// unwrapping pointers. time.Time decodes from a JSON string (RFC 3339).
func wireKind(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeFor[time.Time]() {
		return "string"
	}
	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Slice, reflect.Array:
		return "array of " + wireKind(t.Elem())
	case reflect.Map, reflect.Struct:
		return "object"
	case reflect.Interface:
		return "any"
	default:
		return t.Kind().String()
	}
}

// specKind classifies an OpenAPI schema by the JSON wire kind it describes,
// resolving $ref chains against doc (a reference cycle reads as "object").
func specKind(s *specSchema, doc *specDoc, seen map[string]bool) string {
	if s == nil {
		return "any"
	}
	if s.Ref != "" {
		name := strings.TrimPrefix(s.Ref, "#/components/schemas/")
		if seen[name] {
			return "object"
		}
		target, ok := doc.Components.Schemas[name]
		if !ok {
			return "any"
		}
		seen[name] = true
		return specKind(target, doc, seen)
	}
	switch s.Type {
	case "string":
		return "string"
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	case "array":
		return "array of " + specKind(s.Items, doc, seen)
	case "object":
		return "object"
	default:
		if len(s.Properties) > 0 {
			return "object"
		}
		return "any"
	}
}

// wireKindCompatible reports whether a curated field and an official schema
// property decode the same JSON wire kind. An untyped schema ("any") carries
// any kind, element-wise for arrays.
func wireKindCompatible(local, spec string) bool {
	for {
		if spec == "any" {
			return true
		}
		l, lok := strings.CutPrefix(local, "array of ")
		s, sok := strings.CutPrefix(spec, "array of ")
		if lok != sok {
			return false
		}
		if !lok {
			return local == spec
		}
		local, spec = l, s
	}
}

func TestUpstreamSchemaDrift(t *testing.T) {
	tests := []struct {
		// eachOf: every arrapi JSON tag must exist (kind-compatibly) in EVERY
		// listed official schema. Used for types decoded from both services
		// (a rename on either wire breaks the shared struct there).
		eachOf []specRef
		// anyOf: every arrapi JSON tag must exist (kind-compatibly) in AT
		// LEAST ONE listed schema. Used only for HistoryRecord, a deliberate
		// union type (seriesId and episodeId are Sonarr-only, movieId is
		// Radarr-only).
		anyOf []specRef
		local any
	}{
		{local: arrapi.Series{}, eachOf: []specRef{{"sonarr", "SeriesResource"}}},
		{local: arrapi.Season{}, eachOf: []specRef{{"sonarr", "SeasonResource"}}},
		{local: arrapi.SeasonStatistics{}, eachOf: []specRef{{"sonarr", "SeasonStatisticsResource"}}},
		{local: arrapi.SeriesStatistics{}, eachOf: []specRef{{"sonarr", "SeriesStatisticsResource"}}},
		{local: arrapi.Episode{}, eachOf: []specRef{{"sonarr", "EpisodeResource"}}},
		{local: arrapi.EpisodeFile{}, eachOf: []specRef{{"sonarr", "EpisodeFileResource"}}},
		{local: arrapi.Movie{}, eachOf: []specRef{{"radarr", "MovieResource"}}},
		{local: arrapi.MovieFile{}, eachOf: []specRef{{"radarr", "MovieFileResource"}}},
		{local: arrapi.MediaInfo{}, eachOf: []specRef{{"sonarr", "MediaInfoResource"}, {"radarr", "MediaInfoResource"}}},
		{local: arrapi.AlternateTitle{}, eachOf: []specRef{{"sonarr", "AlternateTitleResource"}, {"radarr", "AlternativeTitleResource"}}},
		{local: arrapi.Language{}, eachOf: []specRef{{"sonarr", "Language"}, {"radarr", "Language"}}},
		{local: arrapi.Tag{}, eachOf: []specRef{{"sonarr", "TagResource"}, {"radarr", "TagResource"}}},
		{local: arrapi.SystemStatus{}, eachOf: []specRef{{"sonarr", "SystemResource"}, {"radarr", "SystemResource"}}},
		{local: arrapi.QualityProfile{}, eachOf: []specRef{{"sonarr", "QualityProfileResource"}, {"radarr", "QualityProfileResource"}}},
		{local: arrapi.RootFolder{}, eachOf: []specRef{{"sonarr", "RootFolderResource"}, {"radarr", "RootFolderResource"}}},
		{local: arrapi.Command{}, eachOf: []specRef{{"sonarr", "CommandResource"}, {"radarr", "CommandResource"}}},
		{local: arrapi.HistoryPage{}, eachOf: []specRef{{"sonarr", "HistoryResourcePagingResource"}, {"radarr", "HistoryResourcePagingResource"}}},
		{local: arrapi.HistoryRecord{}, anyOf: []specRef{{"sonarr", "HistoryResource"}, {"radarr", "HistoryResource"}}},
	}

	for _, tc := range tests {
		localType := reflect.TypeOf(tc.local)
		t.Run(localType.Name(), func(t *testing.T) {
			localFields := jsonTagFields(localType)
			if len(localFields) == 0 {
				t.Fatalf("arrapi.%s declares no JSON tags; table entry is pointless", localType.Name())
			}

			for _, ref := range tc.eachOf {
				schema, doc := ref.schema(t)
				for tag, localFT := range localFields {
					prop, ok := schema.Properties[tag]
					if !ok {
						t.Errorf("arrapi.%s tag %q is not carried by %s: the upstream schema renamed or removed it",
							localType.Name(), tag, ref)
						continue
					}
					if _, exempt := wireKindExceptions[localType.Name()+"."+tag]; exempt {
						continue
					}
					if got := specKind(prop, doc, map[string]bool{}); !wireKindCompatible(wireKind(localFT), got) {
						t.Errorf("arrapi.%s tag %q decodes wire kind %q but %s declares wire kind %q: the upstream field changed type",
							localType.Name(), tag, wireKind(localFT), ref, got)
					}
				}
			}

			if len(tc.anyOf) == 0 {
				return
			}
			names := make([]string, 0, len(tc.anyOf))
			for _, ref := range tc.anyOf {
				names = append(names, ref.String())
			}
			for tag, localFT := range localFields {
				_, exempt := wireKindExceptions[localType.Name()+"."+tag]
				found, compatible := false, false
				for _, ref := range tc.anyOf {
					schema, doc := ref.schema(t)
					prop, ok := schema.Properties[tag]
					if !ok {
						continue
					}
					found = true
					if exempt || wireKindCompatible(wireKind(localFT), specKind(prop, doc, map[string]bool{})) {
						compatible = true
					}
				}
				switch {
				case !found:
					t.Errorf("arrapi.%s tag %q is not carried by any of %s: the upstream schema renamed or removed it",
						localType.Name(), tag, strings.Join(names, ", "))
				case !compatible:
					t.Errorf("arrapi.%s tag %q decodes wire kind %q but no carrier among %s agrees: the upstream field changed type",
						localType.Name(), tag, wireKind(localFT), strings.Join(names, ", "))
				}
			}
		})
	}
}

// TestUpstreamEndpointDrift pins every request path and query parameter name
// arrapi hand-builds (client.go, sonarr.go, radarr.go, profiles.go, tags.go,
// history.go, command.go) against the official documents' paths section. If
// an upstream API move renames an endpoint or a parameter, the document stops
// declaring the pinned shape and this test fails. Path templates use the
// documents' own {id} placeholders where arrapi interpolates an ID.
func TestUpstreamEndpointDrift(t *testing.T) {
	pins := []struct {
		svcs   []string
		method string
		path   string
		query  []string
	}{
		// Sonarr-only read surface (sonarr.go).
		{svcs: []string{"sonarr"}, method: http.MethodGet, path: "/api/v3/series"},
		{svcs: []string{"sonarr"}, method: http.MethodGet, path: "/api/v3/series/{id}"},
		{
			svcs: []string{"sonarr"}, method: http.MethodGet, path: "/api/v3/episode",
			query: []string{"seriesId", "includeEpisodeFile"},
		},
		{svcs: []string{"sonarr"}, method: http.MethodGet, path: "/api/v3/episode/{id}"},
		{
			svcs: []string{"sonarr"}, method: http.MethodGet, path: "/api/v3/episodefile",
			query: []string{"seriesId"},
		},

		// Radarr-only read surface (radarr.go).
		{svcs: []string{"radarr"}, method: http.MethodGet, path: "/api/v3/movie"},
		{svcs: []string{"radarr"}, method: http.MethodGet, path: "/api/v3/movie/{id}"},

		// Shared surface (client.go, tags.go, profiles.go, history.go,
		// command.go) — pinned against BOTH services.
		{svcs: []string{"sonarr", "radarr"}, method: http.MethodGet, path: "/api/v3/tag"},
		{svcs: []string{"sonarr", "radarr"}, method: http.MethodGet, path: "/api/v3/qualityprofile"},
		{svcs: []string{"sonarr", "radarr"}, method: http.MethodGet, path: "/api/v3/rootfolder"},
		{svcs: []string{"sonarr", "radarr"}, method: http.MethodGet, path: "/api/v3/system/status"},
		{
			// GetHistorySince sends the include* trio to both services; each
			// service is pinned only on the parameters it declares (the other
			// side's flag rides along and is ignored by model binding).
			svcs: []string{"sonarr"}, method: http.MethodGet, path: "/api/v3/history/since",
			query: []string{"date", "includeSeries", "includeEpisode"},
		},
		{
			svcs: []string{"radarr"}, method: http.MethodGet, path: "/api/v3/history/since",
			query: []string{"date", "includeMovie"},
		},
		{
			svcs: []string{"sonarr", "radarr"}, method: http.MethodGet, path: "/api/v3/history",
			query: []string{"page", "pageSize", "sortKey", "sortDirection"},
		},
		{svcs: []string{"sonarr", "radarr"}, method: http.MethodPost, path: "/api/v3/command"},
		{svcs: []string{"sonarr", "radarr"}, method: http.MethodGet, path: "/api/v3/command/{id}"},
	}

	for _, tc := range pins {
		for _, svc := range tc.svcs {
			t.Run(svc+" "+tc.method+" "+tc.path, func(t *testing.T) {
				doc := openapiSpec(t, svc)
				item, ok := doc.Paths[tc.path]
				if !ok {
					t.Fatalf("official %s document declares no path %q: the upstream endpoint moved", svc, tc.path)
				}
				if item.operation(tc.method) == nil {
					t.Fatalf("official %s document declares no %s on %q: the upstream method changed", svc, tc.method, tc.path)
				}
				declared := item.queryParams(tc.method)
				for _, key := range tc.query {
					if !declared[key] {
						t.Errorf("official %s document declares no %q query parameter on %s %s: the upstream parameter was renamed or removed",
							svc, key, tc.method, tc.path)
					}
				}
			})
		}
	}
}
