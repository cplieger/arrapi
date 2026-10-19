package sonarr

import (
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3/internal/detect"
	"github.com/cplieger/arrapi/v3/internal/specdrift"
)

var (
	sonarrV4APIv3Contract = specdrift.Contract{
		Name: "Sonarr v4 (API v3)",
		Repo: "Sonarr/Sonarr",
		// v4-develop, not develop: develop's Sonarr.Api.V3 is Sonarr 5's deprecated view.
		Branch: "v4-develop",
		Path:   "src/Sonarr.Api.V3/openapi.json",
		Mirror: "sonarr-v4-api-v3-openapi.json",
	}
	sonarrV5APIv5Contract = specdrift.Contract{
		Name:   "Sonarr v5 (API v5)",
		Repo:   "Sonarr/Sonarr",
		Branch: "develop",
		Path:   "src/Sonarr.Api.V5/openapi.json",
		Mirror: "sonarr-v5-api-v5-openapi.json",
	}
)

// wireKinds names the JSON kind each custom decoder in the DTOs reads.
var wireKinds = specdrift.Kinds{reflect.TypeFor[eventToken](): "string"}

type dtoRoot struct {
	dto    reflect.Type
	schema string
}

// driftCase is one engine's contract, the DTOs it decodes, and the fake that serves it.
type driftCase struct {
	contract specdrift.Contract
	serve    func(*fake) *fake
	version  string
	prefix   string
	roots    []dtoRoot
}

func driftCases() []driftCase {
	return []driftCase{
		{
			contract: sonarrV4APIv3Contract, serve: func(f *fake) *fake { return f.sonarr4(v4) }, version: v4, prefix: sonarrV4Prefix,
			// Sonarr 4's document declares no schema for GET /api, so detect.APIDoc is
			// pinned on the Sonarr v5 (API v5) contract only.
			roots: []dtoRoot{
				{reflect.TypeFor[sonarrV4Series](), "SeriesResource"},
				{reflect.TypeFor[sonarrV4Episode](), "EpisodeResource"},
				{reflect.TypeFor[sonarrV4EpisodeFile](), "EpisodeFileResource"},
				{reflect.TypeFor[sonarrV4History](), "HistoryResource"},
				{reflect.TypeFor[sonarrV4Tag](), "TagResource"},
				{reflect.TypeFor[sonarrV4RootFolder](), "RootFolderResource"},
				{reflect.TypeFor[detect.StatusDoc](), "SystemResource"},
			},
		},
		{
			contract: sonarrV5APIv5Contract, serve: func(f *fake) *fake { return f.sonarr5(v5) }, version: v5, prefix: sonarrV5Prefix,
			roots: []dtoRoot{
				{reflect.TypeFor[sonarrV5Series](), "SeriesResource"},
				{reflect.TypeFor[sonarrV5Episode](), "EpisodeResource"},
				{reflect.TypeFor[sonarrV5EpisodeFile](), "EpisodeFileResource"},
				{reflect.TypeFor[sonarrV5History](), "HistoryResource"},
				{reflect.TypeFor[sonarrV5Tag](), "TagResource"},
				{reflect.TypeFor[sonarrV5RootFolder](), "RootFolderResource"},
				{reflect.TypeFor[detect.StatusDoc](), "SystemResource"},
				{reflect.TypeFor[detect.APIDoc](), "ApiInfoResource"},
			},
		},
	}
}

func TestUpstreamSchemaDrift(t *testing.T) {
	for _, dc := range driftCases() {
		t.Run(dc.contract.Name, func(t *testing.T) {
			doc := specdrift.Load(t, &dc.contract)
			for _, r := range dc.roots {
				t.Run(r.dto.Name(), func(t *testing.T) {
					for _, p := range doc.CheckDTO(r.schema, r.dto, wireKinds) {
						t.Error(p)
					}
				})
			}
		})
	}
}

// TestUpstreamRequestDrift runs every public method that sends or builds a request
// against a fake of the engine's server and checks each request, detection probes
// included, against the engine's document, so the pinned requests are the ones the
// code builds.
func TestUpstreamRequestDrift(t *testing.T) {
	for _, dc := range driftCases() {
		t.Run(dc.contract.Name, func(t *testing.T) {
			doc := specdrift.Load(t, &dc.contract)
			f := dc.serve(newFake())
			f.on("GET "+dc.prefix+"/series/41", ans(200, dc.version, `{"id":41}`))
			c := newClient(t, f)
			for name, call := range map[string]func() ([]string, error){
				"Info":              func() ([]string, error) { _, err := c.Info(t.Context()); return nil, err },
				"Series":            func() ([]string, error) { _, err := c.Series(t.Context()); return nil, err },
				"SeriesByID":        func() ([]string, error) { _, _, err := c.SeriesByID(t.Context(), 41); return nil, err },
				"Episodes":          func() ([]string, error) { _, err := c.Episodes(t.Context(), 41); return nil, err },
				"EpisodesWithFiles": func() ([]string, error) { _, err := c.EpisodesWithFiles(t.Context(), 41); return nil, err },
				"EpisodeFiles":      func() ([]string, error) { _, err := c.EpisodeFiles(t.Context(), 41); return nil, err },
				"HistorySince": func() ([]string, error) {
					_, err := c.HistorySince(t.Context(), time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), EventGrabbed)
					return nil, err
				},
				"Tags":         func() ([]string, error) { _, err := c.Tags(t.Context()); return nil, err },
				"RootFolders":  func() ([]string, error) { _, err := c.RootFolders(t.Context()); return nil, err },
				"RescanSeries": func() ([]string, error) { return nil, c.RescanSeries(t.Context(), 41) },
				"MediaCoverURL": func() ([]string, error) {
					u, err := c.MediaCoverURL(t.Context(), 41, "poster-250.jpg")
					return []string{http.MethodGet + " " + u}, err
				},
			} {
				t.Run(name, func(t *testing.T) {
					f.clearLog()
					built, err := call()
					if err != nil {
						t.Fatalf("%s: %v", name, err)
					}
					sent := append(f.requestURIs(), built...)
					if len(sent) == 0 {
						t.Fatalf("%s sent and built no request, so nothing was checked", name)
					}
					for _, r := range sent {
						method, uri, _ := strings.Cut(r, " ")
						for _, p := range doc.CheckRequest(method, uri) {
							t.Error(p)
						}
					}
				})
			}
		})
	}
}

func TestUpstreamEventTokens(t *testing.T) {
	for _, dc := range driftCases() {
		t.Run(dc.contract.Name, func(t *testing.T) {
			doc := specdrift.Load(t, &dc.contract)
			enum, err := doc.PropertyEnum("HistoryResource", "eventType")
			if err != nil {
				t.Fatal(err)
			}
			for v, tok := range eventTokens {
				if v > 0 && !slices.Contains(enum, tok) {
					t.Errorf("%s: HistoryResource.eventType declares no %q (EventType %d): %v", dc.contract.Name, tok, v, enum)
				}
			}
		})
	}
}

func TestSonarrV4APIv3ContractIsSonarr4Document(t *testing.T) {
	doc := specdrift.Load(t, &sonarrV4APIv3Contract)
	declared, err := doc.Declares("SeriesResource", "seasonType")
	if err != nil {
		t.Fatal(err)
	}
	if declared {
		t.Fatal("fetched Sonarr v4 (API v3) contract declares SeriesResource.seasonType: it is Sonarr 5's deprecated API v3 view, not v4-develop")
	}
}

func TestSonarrV5APIv5ContractIsV5Document(t *testing.T) {
	doc := specdrift.Load(t, &sonarrV5APIv5Contract)
	if d := doc.Description(); !strings.Contains(d, "Sonarr v5") {
		t.Fatalf("fetched Sonarr v5 (API v5) contract's info.description %q does not name Sonarr v5", d)
	}
}

// TestSonarrV5APIv5ForbidsIncludeEpisodeFile pins that /api/v5/episode declares no
// includeEpisodeFile, so TestUpstreamRequestDrift refuses an engine sending Sonarr v4
// (API v3)'s file flag there, which the server ignores and embeds no file.
func TestSonarrV5APIv5ForbidsIncludeEpisodeFile(t *testing.T) {
	doc := specdrift.Load(t, &sonarrV5APIv5Contract)
	params, err := doc.QueryParams(http.MethodGet, "/api/v5/episode")
	if err != nil {
		t.Fatal(err)
	}
	if params["includeEpisodeFile"] {
		t.Error("Sonarr v5 (API v5) declares includeEpisodeFile on GET /api/v5/episode: re-check which flag embeds files there")
	}
	if !params["includeSubresources"] {
		t.Error("Sonarr v5 (API v5) declares no includeSubresources on GET /api/v5/episode: the upstream parameter was renamed or removed")
	}
}

func TestContractNamesItsBranch(t *testing.T) {
	for _, tc := range []struct {
		c                  specdrift.Contract
		branch, path, file string
	}{
		{sonarrV4APIv3Contract, "v4-develop", "src/Sonarr.Api.V3/openapi.json", "sonarr-v4-api-v3-openapi.json"},
		{sonarrV5APIv5Contract, "develop", "src/Sonarr.Api.V5/openapi.json", "sonarr-v5-api-v5-openapi.json"},
	} {
		t.Run(tc.c.Name, func(t *testing.T) {
			if tc.c.Repo != "Sonarr/Sonarr" || tc.c.Branch != tc.branch || tc.c.Path != tc.path || tc.c.Mirror != tc.file {
				t.Errorf("contract = %+v, want Sonarr/Sonarr %s %s mirrored as %s", tc.c, tc.branch, tc.path, tc.file)
			}
			if strings.Contains(tc.c.URL(), "HEAD") || strings.Contains(tc.c.MirrorURL(), "HEAD") {
				t.Errorf("%s names HEAD, which follows Sonarr's default branch", tc.c.URL())
			}
		})
	}
}

// TestMirrorWorkflowMirrorsContract keeps the mirror workflow fetching the documents
// these contracts read, so a moved document cannot leave the fallback on a stale copy.
func TestMirrorWorkflowMirrorsContract(t *testing.T) {
	wf, err := os.ReadFile("../.github/workflows/schema-mirror.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range specdrift.CheckMirrored(string(wf), &sonarrV4APIv3Contract, &sonarrV5APIv5Contract) {
		t.Error(p)
	}
}
