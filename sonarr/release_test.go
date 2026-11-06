package sonarr

import (
	"context"
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

// linkSecret is the value every downloadUrl and magnetUrl of the built release fixtures
// holds, standing in for an indexer key or a tracker passkey.
const linkSecret = "LINKSECRET0451"

// builtReleases returns the hand-built release search answer of the engine at prefix.
func builtReleases(t *testing.T, prefix string) string {
	t.Helper()
	dir := map[string]string{sonarrV4Prefix: dirSonarrV4APIv3, sonarrV5Prefix: dirSonarrV5APIv5}[prefix]
	b, err := os.ReadFile(filepath.Join("..", "testdata", "built", dir, "releases.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// releasesWant is what both built fixtures hold, read from Sonarr's ReleaseResource
// mappers: an approved season pack mapped onto two episodes, a rejected Usenet release
// with no info link or hash, and a temporarily rejected release mapped onto no series.
var releasesWant = []Release{
	{
		GUID: "g-1", Indexer: "indexer-1", Title: "Code.Geass.S01.1080p.BluRay.x264-GRP", Quality: "Bluray-1080p",
		InfoHash: "hash-1", InfoURL: "info-1", CommentURL: "comment-1",
		MappedEpisodes: []ReleaseEpisode{
			{ID: 411, SeasonNumber: 1, EpisodeNumber: 1, Absolute: 1},
			{ID: 412, SeasonNumber: 1, EpisodeNumber: 2, Absolute: 2},
		},
		Size: 1_001_000_000, IndexerID: 3, CustomFormatScore: 150, MappedSeriesID: 41, Weight: 0,
		Flags: FlagScene | FlagFreeleech25, Approved: true,
	},
	{
		GUID: "g-2", Indexer: "indexer-2", Title: "Code.Geass.S01E01.720p.HDTV.x264-OTHER", Quality: "HDTV-720p",
		CommentURL:     "comment-2",
		Rejections:     []string{"Quality not wanted in profile", "Not an upgrade for existing episode file(s)"},
		MappedEpisodes: []ReleaseEpisode{{ID: 411, SeasonNumber: 1, EpisodeNumber: 1}},
		Size:           1_002_000_000, IndexerID: 4, CustomFormatScore: -10, MappedSeriesID: 41, Weight: 1,
		Rejected: true,
	},
	{
		GUID: "g-3", Indexer: "indexer-1", Title: "Unknown.Show.S01E01.1080p.WEB-DL", Quality: "WEBDL-1080p",
		InfoHash:   "hash-3",
		Rejections: []string{"Waiting for better quality release"},
		IndexerID:  3, Weight: 2, Flags: FlagFreeleech, TemporarilyRejected: true,
	},
}

func wantReleases(t *testing.T, got, want []Release) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("releases =\n%#v\nwant\n%#v", got, want)
	}
}

// releaseSearches are the two public searches and the request each sends.
var releaseSearches = map[string]struct {
	search func(context.Context, *Client) ([]Release, error)
	query  string
}{
	"SeasonReleases": {
		func(ctx context.Context, c *Client) ([]Release, error) {
			return c.SeasonReleases(ctx, SeasonRef{SeriesID: 41})
		},
		"?seasonNumber=0&seriesId=41",
	},
	"EpisodeReleases": {
		func(ctx context.Context, c *Client) ([]Release, error) { return c.EpisodeReleases(ctx, 7) },
		"?episodeId=7",
	},
}

func TestReleaseSearchSendsOneRequestAfterAFreshDetection(t *testing.T) {
	for engineName, tc := range sonarrEngines {
		prefix := tc.prefix
		for name, s := range releaseSearches {
			t.Run(engineName+" "+name, func(t *testing.T) {
				f := tc.serve(newFake())
				c := newClient(t, f)
				cached(t, c, f)
				f.on("GET "+prefix+"/release", ans(200, tc.version, builtReleases(t, prefix)))
				got, err := s.search(t.Context(), c)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				wantReleases(t, got, releasesWant)
				wantURIs(t, f, join(tc.detect, []string{"GET " + prefix + "/release" + s.query})...)
			})
		}
	}
}

func TestReleaseSearchIsNeverResent(t *testing.T) {
	for engineName, tc := range sonarrEngines {
		prefix := tc.prefix
		search := "GET " + prefix + "/release"
		for name, s := range releaseSearches {
			t.Run(engineName+" "+name, func(t *testing.T) {
				for answerName, a := range map[string]struct {
					step  step
					check func(*testing.T, error)
				}{
					"an attributed 500": {ans(500, tc.version, `{"message":"Episode search failed"}`), func(t *testing.T, err error) {
						se := wantStatus(t, err, 500)
						if !se.Attributed || se.Message != "Episode search failed" {
							t.Errorf("StatusError = %+v, want the attributed 500 with Sonarr's message", se)
						}
					}},
					"a header-less 500": {ans(500, "", `{"message":"Episode search failed"}`), func(t *testing.T, err error) {
						if se := wantStatus(t, err, 500); se.Attributed {
							t.Error("a header-less 500 reads as attributed")
						}
					}},
					"an attributed 429":     {ans(429, tc.version, ""), func(t *testing.T, err error) { wantStatus(t, err, 429) }},
					"a header-less 503":     {ans(503, "", ""), func(t *testing.T, err error) { wantStatus(t, err, 503) }},
					"another version's 200": {ans(200, "9.0.0.1", builtReleases(t, prefix)), func(t *testing.T, err error) { wantChanged(t, err) }},
					"a header-less 200": {ans(200, "", builtReleases(t, prefix)), func(t *testing.T, err error) {
						wantUnsupported(t, err, arrapi.ReasonNoVersionHeader)
					}},
					"a dial failure": {dialFails, func(t *testing.T, err error) {
						if err == nil {
							t.Fatal("a failed dial returned no error")
						}
					}},
				} {
					t.Run(answerName, func(t *testing.T) {
						f := tc.serve(newFake())
						c := newClient(t, f)
						cached(t, c, f)
						f.on(search, a.step)
						got, err := s.search(t.Context(), c)
						a.check(t, err)
						if got != nil {
							t.Errorf("releases = %v on a failed search, want nil", got)
						}
						if n := count(f, search); n != 1 {
							t.Errorf("sent %d searches, want 1", n)
						}
					})
				}
			})
		}
	}
}

func TestReleaseHoldsNoDownloadLink(t *testing.T) {
	for engineName, tc := range sonarrEngines {
		prefix := tc.prefix
		t.Run(engineName, func(t *testing.T) {
			answer := builtReleases(t, prefix)
			if n := strings.Count(answer, linkSecret); n < 4 {
				t.Fatalf("the fixture holds %d link sentinels, want one per downloadUrl and magnetUrl", n)
			}
			c, f := engineClient(t, engineCases()[engineName])
			f.on("GET "+prefix+"/release", ans(200, tc.version, answer))
			got, err := c.EpisodeReleases(t.Context(), 7)
			if err != nil || len(got) == 0 {
				t.Fatalf("EpisodeReleases = (%v, %v), want the fixture's releases", got, err)
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
		})
	}
}

func TestReleaseTypesDeclareNoLinkField(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[Release](), reflect.TypeFor[ReleaseEpisode]()} {
		for f := range typ.Fields() {
			if name := strings.ToLower(f.Name); strings.Contains(name, "download") || strings.Contains(name, "magnet") {
				t.Errorf("%s.%s can hold a download or magnet link", typ.Name(), f.Name)
			}
		}
	}
}

// Sonarr 5 declares parsedInfo, release and decision nullable; a null one reads as
// that part's zero values.
func TestReleaseSearchReadsSonarr5NullParts(t *testing.T) {
	c, f := engineClient(t, engineCases()["Sonarr v5 (API v5)"])
	f.on("GET /api/v5/release", ans(200, v5, `[{"parsedInfo":null,"release":null,"decision":null,"mappedSeriesId":41,"releaseWeight":0},
		{"parsedInfo":{"quality":null},"release":{"guid":"g-9","indexerId":2},"decision":{"approved":true,"rejections":null},"releaseWeight":1}]`))
	got, err := c.EpisodeReleases(t.Context(), 7)
	if err != nil {
		t.Fatalf("EpisodeReleases: %v", err)
	}
	wantReleases(t, got, []Release{{MappedSeriesID: 41}, {GUID: "g-9", IndexerID: 2, Weight: 1, Approved: true}})
}

func TestReleaseSearchRefusesANullItem(t *testing.T) {
	for engineName, tc := range sonarrEngines {
		prefix := tc.prefix
		bodies := map[string]string{
			"a null release":           `[null]`,
			"a null mapped episode":    `[{"mappedEpisodeInfo":[null]}]`,
			"a flag word":              `[{"indexerFlags":"G_Scene"}]`,
			"a negative flag set":      `[{"indexerFlags":-1}]`,
			"a null rejection, Sonarr": `[{"rejections":["a",null]}]`,
		}
		if prefix == sonarrV5Prefix {
			bodies["a flag word"] = `[{"release":{"indexerFlags":"G_Scene"}}]`
			bodies["a negative flag set"] = `[{"release":{"indexerFlags":-1}}]`
			bodies["a null rejection, Sonarr"] = `[{"decision":{"rejections":[{"message":"a"},null]}}]`
		}
		for name, answer := range bodies {
			t.Run(engineName+" "+name, func(t *testing.T) {
				c, f := engineClient(t, engineCases()[engineName])
				f.on("GET "+prefix+"/release", ans(200, tc.version, answer))
				got, err := c.EpisodeReleases(t.Context(), 7)
				if _, ok := errors.AsType[*arrapi.DecodeError](err); !ok || got != nil {
					t.Errorf("EpisodeReleases = (%v, %v), want a *arrapi.DecodeError", got, err)
				}
				if n := count(f, "GET "+prefix+"/release"); n != 1 {
					t.Errorf("sent %d searches, want 1", n)
				}
			})
		}
	}
}

func TestReleaseSearchRefusesBadInputBeforeAnyRequest(t *testing.T) {
	for name, tc := range map[string]struct {
		search func(context.Context, *Client) ([]Release, error)
		want   error
	}{
		"SeasonReleases series id 0": {
			func(ctx context.Context, c *Client) ([]Release, error) {
				return c.SeasonReleases(ctx, SeasonRef{Number: 1})
			},
			errReleaseSeriesID,
		},
		"SeasonReleases season -1": {
			func(ctx context.Context, c *Client) ([]Release, error) {
				return c.SeasonReleases(ctx, SeasonRef{SeriesID: 41, Number: -1})
			},
			errReleaseSeason,
		},
		"EpisodeReleases id 0": {
			func(ctx context.Context, c *Client) ([]Release, error) { return c.EpisodeReleases(ctx, 0) },
			errReleaseEpisodeID,
		},
		"EpisodeReleases id -2": {
			func(ctx context.Context, c *Client) ([]Release, error) { return c.EpisodeReleases(ctx, -2) },
			errReleaseEpisodeID,
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			got, err := tc.search(t.Context(), newClient(t, f))
			if got != nil || !errors.Is(err, tc.want) {
				t.Errorf("= (%v, %v), want (nil, %v)", got, err, tc.want)
			}
			wantRequests(t, f)
		})
	}
}

func TestGrabSendsTheGUIDAndIndexer(t *testing.T) {
	for engineName, tc := range sonarrEngines {
		prefix := tc.prefix
		t.Run(engineName, func(t *testing.T) {
			f := tc.serve(newFake())
			c := newClient(t, f)
			cached(t, c, f)
			var got string
			f.on("POST "+prefix+"/release", body(&got, ans(200, tc.version, `{"guid":"g-1","indexerId":3}`)))
			if err := c.Grab(t.Context(), "g-1", 3); err != nil {
				t.Fatalf("Grab: %v", err)
			}
			if want := `{"guid":"g-1","indexerId":3}`; got != want {
				t.Errorf("grab body = %s, want %s", got, want)
			}
			wantRequests(t, f, join(tc.detect, []string{"POST " + prefix + "/release"})...)
		})
	}
}

// The refusals Sonarr's ReleaseController writes for a grab (Sonarr.Api.V3 Indexers and
// Sonarr.Api.V5 Release), and whether each is classified.
var grabRefusals = map[string]struct {
	message string
	code    int
	want    arrapi.GrabReason // 0: a plain *arrapi.StatusError
}{
	"the cache miss":            {"Couldn't find requested release in cache, try searching again", 404, arrapi.GrabNotCached},
	"unparsed episodes":         {"Unable to parse episodes in the release, will need to be manually provided", 404, arrapi.GrabUnmapped},
	"no matching series":        {"Unable to find matching series and episodes, will need to be manually provided", 404, arrapi.GrabUnmapped},
	"the indexer failed":        {"Getting release from indexer failed", 409, arrapi.GrabUnavailable},
	"the indexer failed, why":   {"Getting release from indexer failed: Connection refused", 409, arrapi.GrabUnavailable},
	"a proxy's 404":             {"Not Found", 404, 0},
	"a database constraint":     {"UNIQUE constraint failed: History.Id", 409, 0},
	"the cache sentence, a 500": {"Couldn't find requested release in cache, try searching again", 500, 0},
}

func TestGrabRefusalIsClassifiedByTheArrsSentence(t *testing.T) {
	for engineName, tc := range sonarrEngines {
		prefix := tc.prefix
		for name, refusal := range grabRefusals {
			for headerName, header := range map[string]string{"attributed": tc.version, "header-less": ""} {
				t.Run(engineName+" "+name+" "+headerName, func(t *testing.T) {
					f := tc.serve(newFake())
					c := newClient(t, f)
					msg, _ := json.Marshal(map[string]string{"message": refusal.message})
					f.on("POST "+prefix+"/release", ans(refusal.code, header, string(msg)))
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
					if n := count(f, "POST "+prefix+"/release"); n != 1 {
						t.Errorf("sent %d grabs, want 1", n)
					}
				})
			}
		}
	}
}

// Sonarr 5's 409 quotes its download error (Sonarr.Api.V5 ReleaseController), and one
// is TorrentClientBase's magnet parse failure quoting the release's magnet link, which
// can hold a private tracker's passkey.
func TestGrabRefusalMasksAMagnetLink(t *testing.T) {
	const passkey = "PASSKEY0451"
	f := newFake().sonarr5(v5)
	c := newClient(t, f)
	msg := "Getting release from indexer failed: Failed to parse magnetlink for episode 'Show S01E01': " +
		"'magnet:?xt=urn:btih:zz&dn=Frieren Journey's End \"x\"&tr=https%3A%2F%2Ftracker.example%2Fannounce%2F" + passkey + "'"
	answer, _ := json.Marshal(map[string]string{"message": msg, "description": "NzbDrone.Core.Exceptions.NzbDroneClientException: " + msg + "\n   at Sonarr.Api.V5.Release.ReleaseController.Create()"})
	f.on("POST "+sonarrV5Prefix+"/release", ans(409, v5, string(answer)))
	err := c.Grab(t.Context(), "g-1", 3)
	se := wantStatus(t, err, 409)
	for name, text := range map[string]string{"Error()": err.Error(), "Message": se.Message, "Body": se.Body} {
		if strings.Contains(text, passkey) {
			t.Errorf("%s = %q, holds the passkey", name, text)
		}
	}
	if ge, ok := errors.AsType[*arrapi.GrabError](err); !ok || ge.Reason != arrapi.GrabUnavailable {
		t.Errorf("err = %v, want a *arrapi.GrabError with reason GrabUnavailable", err)
	}
}

// A refusal from another server version came from a server the grab was not sent to
// as detected, so its sentence is not read.
func TestGrabRefusalFromAnotherVersionIsNotClassified(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	f.on("POST /api/v3/release", ans(404, v5, `{"message":"Couldn't find requested release in cache, try searching again"}`))
	err := c.Grab(t.Context(), "g-1", 3)
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
			f := newFake().sonarr4(v4)
			if err := newClient(t, f).Grab(t.Context(), tc.guid, tc.indexer); !errors.Is(err, tc.want) {
				t.Errorf("Grab = %v, want %v", err, tc.want)
			}
			wantRequests(t, f)
		})
	}
}
