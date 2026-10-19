package radarr

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

var radarrAPIv3Contract = specdrift.Contract{
	Name:   "Radarr (API v3)",
	Repo:   "Radarr/Radarr",
	Branch: "develop",
	Path:   "src/Radarr.Api.V3/openapi.json",
	Mirror: "radarr-api-v3-openapi.json",
}

// wireKinds names the JSON kind each custom decoder in the DTOs reads.
var wireKinds = specdrift.Kinds{reflect.TypeFor[eventToken](): "string"}

func TestUpstreamSchemaDrift(t *testing.T) {
	doc := specdrift.Load(t, &radarrAPIv3Contract)
	for _, r := range []struct {
		dto    reflect.Type
		schema string
	}{
		{reflect.TypeFor[radarrMovie](), "MovieResource"},
		{reflect.TypeFor[radarrHistory](), "HistoryResource"},
		{reflect.TypeFor[radarrTag](), "TagResource"},
		{reflect.TypeFor[radarrRootFolder](), "RootFolderResource"},
		{reflect.TypeFor[detect.StatusDoc](), "SystemResource"},
		{reflect.TypeFor[detect.APIDoc](), "ApiInfoResource"},
	} {
		t.Run(r.dto.Name(), func(t *testing.T) {
			for _, p := range doc.CheckDTO(r.schema, r.dto, wireKinds) {
				t.Error(p)
			}
		})
	}
}

// TestUpstreamRequestDrift runs every public method that sends or builds a request
// against a fake Radarr and checks each request, detection probes included, against
// the document, so the pinned requests are the ones the code builds.
func TestUpstreamRequestDrift(t *testing.T) {
	doc := specdrift.Load(t, &radarrAPIv3Contract)
	f := newFake().radarr(vRadarr)
	f.on("GET "+radarrPrefix+"/movie/41", ans(200, vRadarr, `{"id":41}`))
	c := newClient(t, f)
	for name, call := range map[string]func() ([]string, error){
		"Info":      func() ([]string, error) { _, err := c.Info(t.Context()); return nil, err },
		"Movies":    func() ([]string, error) { _, err := c.Movies(t.Context()); return nil, err },
		"MovieByID": func() ([]string, error) { _, _, err := c.MovieByID(t.Context(), 41); return nil, err },
		"HistorySince": func() ([]string, error) {
			_, err := c.HistorySince(t.Context(), time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), EventGrabbed)
			return nil, err
		},
		"Tags":        func() ([]string, error) { _, err := c.Tags(t.Context()); return nil, err },
		"RootFolders": func() ([]string, error) { _, err := c.RootFolders(t.Context()); return nil, err },
		"RescanMovie": func() ([]string, error) { return nil, c.RescanMovie(t.Context(), 41) },
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
}

func TestUpstreamEventTokens(t *testing.T) {
	doc := specdrift.Load(t, &radarrAPIv3Contract)
	enum, err := doc.PropertyEnum("HistoryResource", "eventType")
	if err != nil {
		t.Fatal(err)
	}
	for v, tok := range eventTokens {
		if tok != "" && !slices.Contains(enum, tok) {
			t.Errorf("Radarr (API v3): HistoryResource.eventType declares no %q (EventType %d): %v", tok, v, enum)
		}
	}
}

func TestContractNamesItsBranch(t *testing.T) {
	c := radarrAPIv3Contract
	if c.Repo != "Radarr/Radarr" || c.Branch != "develop" || c.Path != "src/Radarr.Api.V3/openapi.json" || c.Mirror != "radarr-api-v3-openapi.json" {
		t.Errorf("contract = %+v, want Radarr/Radarr develop src/Radarr.Api.V3/openapi.json mirrored as radarr-api-v3-openapi.json", c)
	}
	if strings.Contains(c.URL(), "HEAD") || strings.Contains(c.MirrorURL(), "HEAD") {
		t.Errorf("%s names HEAD, which follows Radarr's default branch", c.URL())
	}
}

// TestMirrorWorkflowMirrorsContract keeps the mirror workflow fetching the document
// this contract reads, so a moved document cannot leave the fallback on a stale copy.
func TestMirrorWorkflowMirrorsContract(t *testing.T) {
	wf, err := os.ReadFile("../.github/workflows/schema-mirror.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range specdrift.CheckMirrored(string(wf), &radarrAPIv3Contract) {
		t.Error(p)
	}
}
