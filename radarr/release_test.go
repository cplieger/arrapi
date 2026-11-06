package radarr

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

// linkSecret is the value every downloadUrl and magnetUrl of the built release fixture
// holds, standing in for an indexer key or a tracker passkey.
const linkSecret = "LINKSECRET0451"

const (
	releaseSearch = "GET /api/v3/release"
	releaseGrabs  = "POST /api/v3/release"
)

// builtReleases returns the hand-built release search answer.
func builtReleases(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "built", "radarr", "api-v3", "releases.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// releasesWant is what the built fixture holds, read from Radarr's ReleaseResource
// mapper: an approved release with Radarr's Scene and Freeleech25 names, a rejected
// Usenet release with no info link or hash, and a temporarily rejected release mapped
// onto no movie.
var releasesWant = []Release{
	{
		GUID: "g-1", Indexer: "indexer-1", Title: "Example.Movie.2011.1080p.BluRay.x264-GRP", Quality: "Bluray-1080p",
		InfoHash: "hash-1", InfoURL: "info-1", CommentURL: "comment-1",
		Size: 1_001_000_000, IndexerID: 3, CustomFormatScore: 150, MappedMovieID: 41, Weight: 0,
		Flags: FlagScene | FlagFreeleech25, Approved: true,
	},
	{
		GUID: "g-2", Indexer: "indexer-2", Title: "Example.Movie.2011.720p.HDTV.x264-OTHER", Quality: "HDTV-720p",
		CommentURL: "comment-2",
		Rejections: []string{"Quality not wanted in profile", "Not an upgrade for existing movie file"},
		Size:       1_002_000_000, IndexerID: 4, CustomFormatScore: -10, MappedMovieID: 41, Weight: 1,
		Rejected: true,
	},
	{
		GUID: "g-3", Indexer: "indexer-1", Title: "Unknown.Movie.2011.1080p.WEB-DL", Quality: "WEBDL-1080p",
		InfoHash:   "hash-3",
		Rejections: []string{"Waiting for better quality release"},
		IndexerID:  3, Weight: 2, Flags: FlagFreeleech, TemporarilyRejected: true,
	},
}

func TestMovieReleasesSendsOneRequestAfterAFreshDetection(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	cached(t, c, f)
	f.on(releaseSearch, ans(200, vRadarr, builtReleases(t)))
	got, err := c.MovieReleases(t.Context(), 41)
	if err != nil {
		t.Fatalf("MovieReleases: %v", err)
	}
	if !reflect.DeepEqual(got, releasesWant) {
		t.Errorf("releases =\n%#v\nwant\n%#v", got, releasesWant)
	}
	wantURIs(t, f, join(detectRadarr, []string{releaseSearch + "?movieId=41"})...)
}

func TestMovieReleasesIsNeverResent(t *testing.T) {
	for name, a := range map[string]struct {
		step  step
		check func(*testing.T, error)
	}{
		"an attributed 500": {ans(500, vRadarr, `{"message":"Movie search failed"}`), func(t *testing.T, err error) {
			se := wantStatus(t, err, 500)
			if !se.Attributed || se.Message != "Movie search failed" {
				t.Errorf("StatusError = %+v, want the attributed 500 with Radarr's message", se)
			}
		}},
		"a header-less 400": {ans(400, "", `{"message":"Movie search failed"}`), func(t *testing.T, err error) {
			if se := wantStatus(t, err, 400); se.Attributed {
				t.Error("a header-less 400 reads as attributed")
			}
		}},
		"an attributed 429":     {ans(429, vRadarr, ""), func(t *testing.T, err error) { wantStatus(t, err, 429) }},
		"a header-less 503":     {ans(503, "", ""), func(t *testing.T, err error) { wantStatus(t, err, 503) }},
		"another version's 200": {ans(200, "6.4.5.10700", builtReleases(t)), func(t *testing.T, err error) { wantChanged(t, err) }},
		"a header-less 200": {ans(200, "", builtReleases(t)), func(t *testing.T, err error) {
			wantUnsupported(t, err, arrapi.ReasonNoVersionHeader)
		}},
		"a dial failure": {dialFails, func(t *testing.T, err error) {
			if err == nil {
				t.Fatal("a failed dial returned no error")
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			cached(t, c, f)
			f.on(releaseSearch, a.step)
			got, err := c.MovieReleases(t.Context(), 41)
			a.check(t, err)
			if got != nil {
				t.Errorf("releases = %v on a failed search, want nil", got)
			}
			if n := count(f, releaseSearch); n != 1 {
				t.Errorf("sent %d searches, want 1", n)
			}
		})
	}
}

func TestReleaseHoldsNoDownloadLink(t *testing.T) {
	answer := builtReleases(t)
	if n := strings.Count(answer, linkSecret); n < 4 {
		t.Fatalf("the fixture holds %d link sentinels, want one per downloadUrl and magnetUrl", n)
	}
	f := newFake().radarr(vRadarr)
	f.on(releaseSearch, ans(200, vRadarr, answer))
	got, err := newClient(t, f).MovieReleases(t.Context(), 41)
	if err != nil || len(got) == 0 {
		t.Fatalf("MovieReleases = (%v, %v), want the fixture's releases", got, err)
	}
	if s := fmt.Sprintf("%#v", got); strings.Contains(s, linkSecret) {
		t.Error("a release's Go-syntax form holds a download or magnet link")
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), linkSecret) {
		t.Error("a release's JSON form holds a download or magnet link")
	}
}

func TestReleaseTypeDeclaresNoLinkField(t *testing.T) {
	typ := reflect.TypeFor[Release]()
	for f := range typ.Fields() {
		if name := strings.ToLower(f.Name); strings.Contains(name, "download") || strings.Contains(name, "magnet") {
			t.Errorf("%s.%s can hold a download or magnet link", typ.Name(), f.Name)
		}
	}
}

func TestMovieReleasesRefusesABadItem(t *testing.T) {
	for name, answer := range map[string]string{
		"a null release":    `[null]`,
		"a null rejection":  `[{"rejections":["a",null]}]`,
		"a flag string":     `[{"indexerFlags":"G_Scene"}]`,
		"a flag object":     `[{"indexerFlags":{}}]`,
		"a negative flag":   `[{"indexerFlags":-1}]`,
		"a fractional flag": `[{"indexerFlags":1.5}]`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			f.on(releaseSearch, ans(200, vRadarr, answer))
			got, err := newClient(t, f).MovieReleases(t.Context(), 41)
			if _, ok := errors.AsType[*arrapi.DecodeError](err); !ok || got != nil {
				t.Errorf("MovieReleases = (%v, %v), want a *arrapi.DecodeError", got, err)
			}
			if n := count(f, releaseSearch); n != 1 {
				t.Errorf("sent %d searches, want 1", n)
			}
		})
	}
}

func TestMovieReleasesRefusesBadInputBeforeAnyRequest(t *testing.T) {
	for _, id := range []int{0, -1} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			got, err := newClient(t, f).MovieReleases(t.Context(), id)
			if got != nil || !errors.Is(err, errReleaseMovieID) {
				t.Errorf("MovieReleases(%d) = (%v, %v), want (nil, %v)", id, got, err, errReleaseMovieID)
			}
			wantRequests(t, f)
		})
	}
}

func TestGrabSendsTheGUIDAndIndexer(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	cached(t, c, f)
	var got string
	f.on(releaseGrabs, body(&got, ans(200, vRadarr, `{"guid":"g-1","indexerId":3}`)))
	if err := c.Grab(t.Context(), "g-1", 3); err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if want := `{"guid":"g-1","indexerId":3}`; got != want {
		t.Errorf("grab body = %s, want %s", got, want)
	}
	wantRequests(t, f, join(detectRadarr, []string{releaseGrabs})...)
}

// The refusals Radarr's ReleaseController (Radarr.Api.V3 Indexers) writes for a grab,
// and whether each is classified.
var grabRefusals = map[string]struct {
	message string
	code    int
	want    arrapi.GrabReason // 0: a plain *arrapi.StatusError
}{
	"the cache miss":            {"Couldn't find requested release in cache, try searching again", 404, arrapi.GrabNotCached},
	"no matching movie":         {"Unable to find matching movie, will need to be manually provided", 404, arrapi.GrabUnmapped},
	"the indexer failed":        {"Getting release from indexer failed", 409, arrapi.GrabUnavailable},
	"a proxy's 404":             {"Not Found", 404, 0},
	"a database constraint":     {"UNIQUE constraint failed: History.Id", 409, 0},
	"the cache sentence, a 500": {"Couldn't find requested release in cache, try searching again", 500, 0},
}

func TestGrabRefusalIsClassifiedByTheArrsSentence(t *testing.T) {
	for name, refusal := range grabRefusals {
		for headerName, header := range map[string]string{"attributed": vRadarr, "header-less": ""} {
			t.Run(name+" "+headerName, func(t *testing.T) {
				f := newFake().radarr(vRadarr)
				c := newClient(t, f)
				msg, _ := json.Marshal(map[string]string{"message": refusal.message})
				f.on(releaseGrabs, ans(refusal.code, header, string(msg)))
				err := c.Grab(t.Context(), "g-1", 3)
				se := wantStatus(t, err, refusal.code)
				if se.Attributed != (header != "") {
					t.Errorf("Attributed = %v with header %q", se.Attributed, header)
				}
				ge, ok := errors.AsType[*arrapi.GrabError](err)
				switch {
				case refusal.want == 0 && ok:
					t.Errorf("err = %v, want a plain *arrapi.StatusError", err)
				case refusal.want != 0 && (!ok || ge.Reason != refusal.want || ge.Err != se):
					t.Errorf("err = %v, want a *arrapi.GrabError with reason %d over the refusal", err, refusal.want)
				}
				if n := count(f, releaseGrabs); n != 1 {
					t.Errorf("sent %d grabs, want 1", n)
				}
			})
		}
	}
}

// A refusal from another server version came from a server the grab was not sent to
// as detected, so its sentence is not read.
func TestGrabRefusalFromAnotherVersionIsNotClassified(t *testing.T) {
	f := newFake().radarr(vRadarr)
	f.on(releaseGrabs, ans(404, "6.4.5.10700", `{"message":"Couldn't find requested release in cache, try searching again"}`))
	err := newClient(t, f).Grab(t.Context(), "g-1", 3)
	wantChanged(t, err)
	if _, ok := errors.AsType[*arrapi.GrabError](err); ok {
		t.Errorf("err = %v, want no *arrapi.GrabError", err)
	}
}

func TestGrabRefusesBadInputBeforeAnyRequest(t *testing.T) {
	for name, tc := range map[string]struct {
		guid    string
		indexer int
		want    error
	}{
		"an empty guid": {"", 3, errGrabGUID},
		"indexer id 0":  {"g-1", 0, errGrabIndexer},
		"indexer id -1": {"g-1", -1, errGrabIndexer},
		"both missing":  {"", 0, errGrabGUID},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			if err := newClient(t, f).Grab(t.Context(), tc.guid, tc.indexer); !errors.Is(err, tc.want) {
				t.Errorf("Grab = %v, want %v", err, tc.want)
			}
			wantRequests(t, f)
		})
	}
}
