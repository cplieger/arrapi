package arrapi_test

// Schema-drift guard.
//
// arrapi hand-curates its DTO field subsets (types.go, history.go, command.go)
// against the Sonarr/Radarr v3 wire format, and Go's JSON decoding ignores
// unknown or missing fields — so an upstream field rename or removal would
// silently decode to a zero value instead of failing. This test pins every
// curated JSON tag against the devopsarr OpenAPI-generated models (test-only
// dependencies, regenerated upstream from the arr teams' published API specs).
// When Renovate bumps sonarr-go/radarr-go after an upstream schema change, a
// dropped or renamed field arrapi carries fails this test in the bump PR,
// turning silent wire drift into a loud CI signal.
//
// The reverse direction is deliberately unchecked: the generated models carry
// the full upstream resource, and arrapi's subsets are curation, not drift.
// Detection latency is bounded by devopsarr's release cadence (their tags can
// trail upstream by months); this guard trades that latency for zero runtime
// dependencies and no facade.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/arrapi"
	"github.com/devopsarr/radarr-go/radarr"
	"github.com/devopsarr/sonarr-go/sonarr"
)

// jsonTagSet returns the JSON field names declared by a struct type's tags,
// excluding untagged fields and `json:"-"`.
func jsonTagSet(typ reflect.Type) map[string]struct{} {
	tags := make(map[string]struct{}, typ.NumField())
	for f := range typ.Fields() {
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			tags[name] = struct{}{}
		}
	}
	return tags
}

func TestUpstreamSchemaDrift(t *testing.T) {
	tests := []struct {
		// eachOf: every arrapi JSON tag must exist in EVERY listed generated
		// model. Used for types decoded from both services (a rename on either
		// wire breaks the shared struct there).
		eachOf []any
		// anyOf: every arrapi JSON tag must exist in AT LEAST ONE listed model.
		// Used only for HistoryRecord, a deliberate union type (seriesId and
		// episodeId are Sonarr-only, movieId is Radarr-only).
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
			localTags := jsonTagSet(localType)
			if len(localTags) == 0 {
				t.Fatalf("arrapi.%s declares no JSON tags; table entry is pointless", localType.Name())
			}

			for _, model := range tc.eachOf {
				modelType := reflect.TypeOf(model)
				modelTags := jsonTagSet(modelType)
				for tag := range localTags {
					if _, ok := modelTags[tag]; !ok {
						t.Errorf("arrapi.%s tag %q is not carried by %s: the upstream schema renamed or removed it",
							localType.Name(), tag, modelType)
					}
				}
			}

			if len(tc.anyOf) == 0 {
				return
			}
			union := make(map[string]struct{})
			names := make([]string, 0, len(tc.anyOf))
			for _, model := range tc.anyOf {
				modelType := reflect.TypeOf(model)
				names = append(names, modelType.String())
				for tag := range jsonTagSet(modelType) {
					union[tag] = struct{}{}
				}
			}
			for tag := range localTags {
				if _, ok := union[tag]; !ok {
					t.Errorf("arrapi.%s tag %q is not carried by any of %s: the upstream schema renamed or removed it",
						localType.Name(), tag, strings.Join(names, ", "))
				}
			}
		})
	}
}
