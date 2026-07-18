package arrapi_test

// Schema-drift guard.
//
// arrapi hand-curates its DTO field subsets (types.go, history.go, command.go)
// against the Sonarr/Radarr v3 wire format, and Go's JSON decoding ignores
// unknown or missing fields — so an upstream field rename or removal would
// silently decode to a zero value instead of failing. These tests pin the
// curated surface against the devopsarr OpenAPI-generated clients (test-only
// dependencies, regenerated upstream from the arr teams' published API specs)
// in three layers:
//
//  1. Tag presence (TestUpstreamSchemaDrift): every curated JSON tag must
//     still exist in the generated models — a dropped or renamed field fails
//     the Renovate bump PR instead of decoding silently to a zero value.
//  2. Wire-kind compatibility (TestUpstreamSchemaDrift): the curated field and
//     the generated field must decode the same JSON wire kind (string, number,
//     boolean, array, object) — an upstream type change fails the same bump PR
//     instead of first surfacing at runtime as an *json.UnmarshalTypeError*.
//     Deliberate divergences are recorded in wireKindExceptions with a reason.
//  3. Endpoint pins (TestUpstreamEndpointDrift): the request paths and query
//     parameter names arrapi hand-builds are pinned against the generated
//     clients' own request construction, captured via httptest — an upstream
//     endpoint move or parameter rename fails the bump PR too. A generated
//     method rename surfaces even earlier, as a compile error in this file.
//
// The reverse direction is deliberately unchecked: the generated models carry
// the full upstream resource, and arrapi's subsets are curation, not drift.
// Detection latency is bounded by devopsarr's release cadence (their tags can
// trail upstream by months); this guard trades that latency for zero runtime
// dependencies and no facade.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/devopsarr/radarr-go/radarr"
	"github.com/devopsarr/sonarr-go/sonarr"
)

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
// diverges from the generated model's declaration, keyed "LocalType.jsonTag".
// Each entry is a place where arrapi models the observed wire rather than the
// generated schema; removing one requires re-verifying the wire behavior.
var wireKindExceptions = map[string]string{
	// Sonarr sends its HistoryEventType enum as an INTEGER on the wire, which
	// the generated string-only EpisodeHistoryEventType cannot decode; Radarr
	// sends a string. arrapi's EventType accepts both — see
	// EventType.UnmarshalJSON.
	"HistoryRecord.eventType": "int-or-string decode; the generated enum is string-only",
}

// wireKind classifies a Go type by the JSON wire kind it decodes from,
// unwrapping pointers and the generated Nullable* wrappers (whose Get method
// names the wrapped type). time.Time decodes from a JSON string (RFC 3339).
func wireKind(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if strings.HasPrefix(t.Name(), "Nullable") {
		if get, ok := reflect.PointerTo(t).MethodByName("Get"); ok && get.Type.NumOut() == 1 {
			return wireKind(get.Type.Out(0))
		}
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

// wireKindCompatible reports whether a curated field and a generated field
// decode the same JSON wire kind. A generated interface{} carries any kind.
func wireKindCompatible(local, generated reflect.Type) bool {
	l, g := wireKind(local), wireKind(generated)
	return l == g || g == "any"
}

func TestUpstreamSchemaDrift(t *testing.T) {
	tests := []struct {
		// eachOf: every arrapi JSON tag must exist (kind-compatibly) in EVERY
		// listed generated model. Used for types decoded from both services (a
		// rename on either wire breaks the shared struct there).
		eachOf []any
		// anyOf: every arrapi JSON tag must exist (kind-compatibly) in AT
		// LEAST ONE listed model. Used only for HistoryRecord, a deliberate
		// union type (seriesId and episodeId are Sonarr-only, movieId is
		// Radarr-only).
		anyOf []any
		local any
	}{
		{local: arrapi.Series{}, eachOf: []any{sonarr.SeriesResource{}}},
		{local: arrapi.Season{}, eachOf: []any{sonarr.SeasonResource{}}},
		{local: arrapi.SeasonStatistics{}, eachOf: []any{sonarr.SeasonStatisticsResource{}}},
		{local: arrapi.SeriesStatistics{}, eachOf: []any{sonarr.SeriesStatisticsResource{}}},
		{local: arrapi.Episode{}, eachOf: []any{sonarr.EpisodeResource{}}},
		{local: arrapi.EpisodeFile{}, eachOf: []any{sonarr.EpisodeFileResource{}}},
		{local: arrapi.Movie{}, eachOf: []any{radarr.MovieResource{}}},
		{local: arrapi.MovieFile{}, eachOf: []any{radarr.MovieFileResource{}}},
		{local: arrapi.MediaInfo{}, eachOf: []any{sonarr.MediaInfoResource{}, radarr.MediaInfoResource{}}},
		{local: arrapi.AlternateTitle{}, eachOf: []any{sonarr.AlternateTitleResource{}, radarr.AlternativeTitleResource{}}},
		{local: arrapi.Language{}, eachOf: []any{sonarr.Language{}, radarr.Language{}}},
		{local: arrapi.Tag{}, eachOf: []any{sonarr.TagResource{}, radarr.TagResource{}}},
		{local: arrapi.SystemStatus{}, eachOf: []any{sonarr.SystemResource{}, radarr.SystemResource{}}},
		{local: arrapi.QualityProfile{}, eachOf: []any{sonarr.QualityProfileResource{}, radarr.QualityProfileResource{}}},
		{local: arrapi.RootFolder{}, eachOf: []any{sonarr.RootFolderResource{}, radarr.RootFolderResource{}}},
		{local: arrapi.Command{}, eachOf: []any{sonarr.CommandResource{}, radarr.CommandResource{}}},
		{local: arrapi.HistoryPage{}, eachOf: []any{sonarr.HistoryResourcePagingResource{}, radarr.HistoryResourcePagingResource{}}},
		{local: arrapi.HistoryRecord{}, anyOf: []any{sonarr.HistoryResource{}, radarr.HistoryResource{}}},
	}

	for _, tc := range tests {
		localType := reflect.TypeOf(tc.local)
		t.Run(localType.Name(), func(t *testing.T) {
			localFields := jsonTagFields(localType)
			if len(localFields) == 0 {
				t.Fatalf("arrapi.%s declares no JSON tags; table entry is pointless", localType.Name())
			}

			for _, model := range tc.eachOf {
				modelType := reflect.TypeOf(model)
				modelFields := jsonTagFields(modelType)
				for tag, localFT := range localFields {
					genFT, ok := modelFields[tag]
					if !ok {
						t.Errorf("arrapi.%s tag %q is not carried by %s: the upstream schema renamed or removed it",
							localType.Name(), tag, modelType)
						continue
					}
					if _, exempt := wireKindExceptions[localType.Name()+"."+tag]; exempt {
						continue
					}
					if !wireKindCompatible(localFT, genFT) {
						t.Errorf("arrapi.%s tag %q decodes wire kind %q but %s declares %s (wire kind %q): the upstream field changed type",
							localType.Name(), tag, wireKind(localFT), modelType, genFT, wireKind(genFT))
					}
				}
			}

			if len(tc.anyOf) == 0 {
				return
			}
			names := make([]string, 0, len(tc.anyOf))
			models := make([]map[string]reflect.Type, 0, len(tc.anyOf))
			for _, model := range tc.anyOf {
				modelType := reflect.TypeOf(model)
				names = append(names, modelType.String())
				models = append(models, jsonTagFields(modelType))
			}
			for tag, localFT := range localFields {
				_, exempt := wireKindExceptions[localType.Name()+"."+tag]
				found, compatible := false, false
				for _, modelFields := range models {
					genFT, ok := modelFields[tag]
					if !ok {
						continue
					}
					found = true
					if exempt || wireKindCompatible(localFT, genFT) {
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

// capturedRequest records the one request a pinned generated-client call
// issued against the recording server.
type capturedRequest struct {
	query  url.Values
	method string
	path   string
}

// newRecordingServer returns an httptest server that records each request into
// the returned capturedRequest and answers 200 with an empty JSON object. The
// generated clients' decode of that body may fail; the pins only assert on the
// captured request, which is complete before decoding starts.
func newRecordingServer(t *testing.T) (*httptest.Server, *capturedRequest) {
	t.Helper()
	last := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last.method, last.path, last.query = r.Method, r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)
	return srv, last
}

// TestUpstreamEndpointDrift pins every request path and query parameter name
// arrapi hand-builds (client.go, sonarr.go, radarr.go, profiles.go, tags.go,
// history.go, command.go) against the generated clients' own request
// construction. If an upstream API move renames an endpoint or a parameter,
// the regenerated devopsarr client stops issuing the pinned shape and this
// test fails in the bump PR; if the generated METHOD is renamed or loses a
// parameter, this file stops compiling there, which is the same signal
// earlier.
func TestUpstreamEndpointDrift(t *testing.T) {
	ctx := t.Context()
	srv, last := newRecordingServer(t)

	scfg := sonarr.NewConfiguration()
	scfg.Servers = sonarr.ServerConfigurations{{URL: srv.URL}}
	sc := sonarr.NewAPIClient(scfg)

	rcfg := radarr.NewConfiguration()
	rcfg.Servers = radarr.ServerConfigurations{{URL: srv.URL}}
	rc := radarr.NewAPIClient(rcfg)

	since := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	pins := []struct {
		call       func() *http.Response
		wantQuery  []string
		name       string
		wantMethod string
		wantPath   string
	}{
		// Sonarr-only read surface (sonarr.go).
		{
			name: "sonarr ListSeries", wantMethod: "GET", wantPath: "/api/v3/series",
			call: func() *http.Response { _, r, _ := sc.SeriesAPI.ListSeries(ctx).Execute(); return r },
		},
		{
			name: "sonarr GetSeriesById", wantMethod: "GET", wantPath: "/api/v3/series/42",
			call: func() *http.Response { _, r, _ := sc.SeriesAPI.GetSeriesById(ctx, 42).Execute(); return r },
		},
		{
			name: "sonarr ListEpisode", wantMethod: "GET", wantPath: "/api/v3/episode",
			wantQuery: []string{"seriesId", "includeEpisodeFile"},
			call: func() *http.Response {
				_, r, _ := sc.EpisodeAPI.ListEpisode(ctx).SeriesId(42).IncludeEpisodeFile(true).Execute()
				return r
			},
		},
		{
			name: "sonarr GetEpisodeById", wantMethod: "GET", wantPath: "/api/v3/episode/42",
			call: func() *http.Response { _, r, _ := sc.EpisodeAPI.GetEpisodeById(ctx, 42).Execute(); return r },
		},
		{
			name: "sonarr ListEpisodeFile", wantMethod: "GET", wantPath: "/api/v3/episodefile",
			wantQuery: []string{"seriesId"},
			call: func() *http.Response {
				_, r, _ := sc.EpisodeFileAPI.ListEpisodeFile(ctx).SeriesId(42).Execute()
				return r
			},
		},

		// Radarr-only read surface (radarr.go).
		{
			name: "radarr ListMovie", wantMethod: "GET", wantPath: "/api/v3/movie",
			call: func() *http.Response { _, r, _ := rc.MovieAPI.ListMovie(ctx).Execute(); return r },
		},
		{
			name: "radarr GetMovieById", wantMethod: "GET", wantPath: "/api/v3/movie/42",
			call: func() *http.Response { _, r, _ := rc.MovieAPI.GetMovieById(ctx, 42).Execute(); return r },
		},

		// Shared surface (client.go, tags.go, profiles.go, history.go,
		// command.go) — pinned against BOTH services.
		{
			name: "sonarr ListTag", wantMethod: "GET", wantPath: "/api/v3/tag",
			call: func() *http.Response { _, r, _ := sc.TagAPI.ListTag(ctx).Execute(); return r },
		},
		{
			name: "radarr ListTag", wantMethod: "GET", wantPath: "/api/v3/tag",
			call: func() *http.Response { _, r, _ := rc.TagAPI.ListTag(ctx).Execute(); return r },
		},
		{
			name: "sonarr ListQualityProfile", wantMethod: "GET", wantPath: "/api/v3/qualityprofile",
			call: func() *http.Response { _, r, _ := sc.QualityProfileAPI.ListQualityProfile(ctx).Execute(); return r },
		},
		{
			name: "radarr ListQualityProfile", wantMethod: "GET", wantPath: "/api/v3/qualityprofile",
			call: func() *http.Response { _, r, _ := rc.QualityProfileAPI.ListQualityProfile(ctx).Execute(); return r },
		},
		{
			name: "sonarr ListRootFolder", wantMethod: "GET", wantPath: "/api/v3/rootfolder",
			call: func() *http.Response { _, r, _ := sc.RootFolderAPI.ListRootFolder(ctx).Execute(); return r },
		},
		{
			name: "radarr ListRootFolder", wantMethod: "GET", wantPath: "/api/v3/rootfolder",
			call: func() *http.Response { _, r, _ := rc.RootFolderAPI.ListRootFolder(ctx).Execute(); return r },
		},
		{
			name: "sonarr GetSystemStatus", wantMethod: "GET", wantPath: "/api/v3/system/status",
			call: func() *http.Response { _, r, _ := sc.SystemAPI.GetSystemStatus(ctx).Execute(); return r },
		},
		{
			name: "radarr GetSystemStatus", wantMethod: "GET", wantPath: "/api/v3/system/status",
			call: func() *http.Response { _, r, _ := rc.SystemAPI.GetSystemStatus(ctx).Execute(); return r },
		},
		{
			name: "sonarr ListHistorySince", wantMethod: "GET", wantPath: "/api/v3/history/since",
			wantQuery: []string{"date", "includeSeries", "includeEpisode"},
			call: func() *http.Response {
				_, r, _ := sc.HistoryAPI.ListHistorySince(ctx).Date(since).IncludeSeries(false).IncludeEpisode(false).Execute()
				return r
			},
		},
		{
			name: "radarr ListHistorySince", wantMethod: "GET", wantPath: "/api/v3/history/since",
			wantQuery: []string{"date", "includeMovie"},
			call: func() *http.Response {
				_, r, _ := rc.HistoryAPI.ListHistorySince(ctx).Date(since).IncludeMovie(false).Execute()
				return r
			},
		},
		{
			name: "sonarr GetHistory", wantMethod: "GET", wantPath: "/api/v3/history",
			wantQuery: []string{"page", "pageSize", "sortKey", "sortDirection"},
			call: func() *http.Response {
				_, r, _ := sc.HistoryAPI.GetHistory(ctx).Page(1).PageSize(50).SortKey("date").SortDirection(sonarr.SORTDIRECTION_DESCENDING).Execute()
				return r
			},
		},
		{
			name: "radarr GetHistory", wantMethod: "GET", wantPath: "/api/v3/history",
			wantQuery: []string{"page", "pageSize", "sortKey", "sortDirection"},
			call: func() *http.Response {
				_, r, _ := rc.HistoryAPI.GetHistory(ctx).Page(1).PageSize(50).SortKey("date").SortDirection(radarr.SORTDIRECTION_DESCENDING).Execute()
				return r
			},
		},
		{
			name: "sonarr CreateCommand", wantMethod: "POST", wantPath: "/api/v3/command",
			call: func() *http.Response {
				_, r, _ := sc.CommandAPI.CreateCommand(ctx).CommandResource(*sonarr.NewCommandResource()).Execute()
				return r
			},
		},
		{
			name: "radarr CreateCommand", wantMethod: "POST", wantPath: "/api/v3/command",
			call: func() *http.Response {
				_, r, _ := rc.CommandAPI.CreateCommand(ctx).CommandResource(*radarr.NewCommandResource()).Execute()
				return r
			},
		},
		{
			name: "sonarr GetCommandById", wantMethod: "GET", wantPath: "/api/v3/command/42",
			call: func() *http.Response { _, r, _ := sc.CommandAPI.GetCommandById(ctx, 42).Execute(); return r },
		},
		{
			name: "radarr GetCommandById", wantMethod: "GET", wantPath: "/api/v3/command/42",
			call: func() *http.Response { _, r, _ := rc.CommandAPI.GetCommandById(ctx, 42).Execute(); return r },
		},
	}

	for _, tc := range pins {
		t.Run(tc.name, func(t *testing.T) {
			*last = capturedRequest{}
			if resp := tc.call(); resp != nil {
				_ = resp.Body.Close()
			}
			if last.method != tc.wantMethod || last.path != tc.wantPath {
				t.Errorf("generated client issued %s %s, arrapi builds %s %s: the upstream endpoint moved",
					last.method, last.path, tc.wantMethod, tc.wantPath)
			}
			for _, key := range tc.wantQuery {
				if !last.query.Has(key) {
					t.Errorf("generated client sent no %q query parameter (got %v): the upstream parameter was renamed or removed", key, last.query)
				}
			}
		})
	}
}
