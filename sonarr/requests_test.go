package sonarr

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v3"
)

// engineCase is one engine's server shape and the bodies its paths answer.
type engineCase struct {
	serve       func(*fake) *fake
	version     string
	prefix      string
	series      string
	episode     string
	episodeFile string
	filesFlag   string // the query EpisodesWithFiles adds
	wantFile    EpisodeFile
}

func engineCases() map[string]engineCase {
	return map[string]engineCase{
		"Sonarr v4 (API v3)": {
			serve: func(f *fake) *fake { return f.sonarr4(v4) }, version: v4, prefix: "/api/v3",
			series: sonarrV4SeriesBody, episode: sonarrV4EpisodeBody, episodeFile: sonarrV4EpisodeFileBody,
			filesFlag: "includeEpisodeFile=true", wantFile: sonarrV4EpisodeFileWant,
		},
		"Sonarr v5 (API v5)": {
			serve: func(f *fake) *fake { return f.sonarr5(v5) }, version: v5, prefix: "/api/v5",
			series: sonarrV5SeriesBody, episode: sonarrV5EpisodeBody, episodeFile: sonarrV5EpisodeFileBody,
			filesFlag: "includeSubresources=episodeFile", wantFile: sonarrV5EpisodeFileWant,
		},
	}
}

// engineClient returns a client cached on the case's server and its fake.
func engineClient(t *testing.T, ec engineCase) (*Client, *fake) {
	t.Helper()
	f := ec.serve(newFake())
	c := newClient(t, f)
	if _, err := c.Tags(t.Context()); err != nil {
		t.Fatalf("detect: %v", err)
	}
	f.clearLog()
	return c, f
}

func wantURIs(t *testing.T, f *fake, want ...string) {
	t.Helper()
	if got := f.requestURIs(); !slices.Equal(got, want) {
		t.Errorf("requests =\n  %q\nwant\n  %q", got, want)
	}
}

func TestEngineRequests(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			t.Run("Series", func(t *testing.T) {
				c, f := engineClient(t, ec)
				f.on("GET "+ec.prefix+"/series", ans(200, ec.version, "["+ec.series+"]"))
				got, err := c.Series(t.Context())
				if err != nil || len(got) != 1 || got[0].ID != 41 || got[0].Title != "Example Show" {
					t.Fatalf("Series = (%+v, %v), want the one series", got, err)
				}
				wantURIs(t, f, "GET "+ec.prefix+"/series")
			})
			t.Run("SeriesByID", func(t *testing.T) {
				c, f := engineClient(t, ec)
				f.on("GET "+ec.prefix+"/series/41", ans(200, ec.version, ec.series))
				got, found, err := c.SeriesByID(t.Context(), 41)
				if err != nil || !found || got.ID != 41 {
					t.Fatalf("SeriesByID = (%+v, %v, %v), want series 41", got, found, err)
				}
				wantURIs(t, f, "GET "+ec.prefix+"/series/41")
			})
			t.Run("Episodes", func(t *testing.T) {
				c, f := engineClient(t, ec)
				f.on("GET "+ec.prefix+"/episode", ans(200, ec.version, "["+ec.episode+"]"))
				got, err := c.Episodes(t.Context(), 41)
				if err != nil || len(got) != 1 || got[0].SeriesID != 41 || got[0].File != nil {
					t.Fatalf("Episodes = (%+v, %v), want one episode without its file", got, err)
				}
				wantURIs(t, f, "GET "+ec.prefix+"/episode?seriesId=41")
			})
			t.Run("EpisodesWithFiles", func(t *testing.T) {
				c, f := engineClient(t, ec)
				f.on("GET "+ec.prefix+"/episode", ans(200, ec.version, "["+ec.episode+"]"))
				got, err := c.EpisodesWithFiles(t.Context(), 41)
				if err != nil || len(got) != 1 || got[0].File == nil {
					t.Fatalf("EpisodesWithFiles = (%+v, %v), want one episode with its file", got, err)
				}
				assertFields(t, *got[0].File, ec.wantFile)
				wantURIs(t, f, "GET "+ec.prefix+"/episode?seriesId=41&"+ec.filesFlag)
			})
			t.Run("EpisodeFiles", func(t *testing.T) {
				c, f := engineClient(t, ec)
				f.on("GET "+ec.prefix+"/episodefile", ans(200, ec.version, "["+ec.episodeFile+"]"))
				got, err := c.EpisodeFiles(t.Context(), 41)
				if err != nil || len(got) != 1 {
					t.Fatalf("EpisodeFiles = (%+v, %v), want one file", got, err)
				}
				assertFields(t, got[0], ec.wantFile)
				wantURIs(t, f, "GET "+ec.prefix+"/episodefile?seriesId=41")
			})
			t.Run("HistorySince", func(t *testing.T) {
				c, f := engineClient(t, ec)
				f.on("GET "+ec.prefix+"/history/since", ans(200, ec.version,
					`[{"id":1,"seriesId":41,"date":"2001-02-03T04:05:06Z","eventType":"grabbed"}]`))
				since := time.Date(2001, 2, 3, 5, 5, 6, 999_000_000, time.FixedZone("plus1", 3600))
				got, err := c.HistorySince(t.Context(), since)
				if err != nil || len(got) != 1 || got[0].EventType != EventGrabbed || got[0].SeriesID != 41 {
					t.Fatalf("HistorySince = (%+v, %v), want the one record", got, err)
				}
				wantURIs(t, f, "GET "+ec.prefix+"/history/since?date=2001-02-03T04:05:06Z")
			})
			t.Run("Tags", func(t *testing.T) {
				c, f := engineClient(t, ec)
				f.on("GET "+ec.prefix+"/tag", ans(200, ec.version, `[{"id":2,"label":"anime"}]`))
				got, err := c.Tags(t.Context())
				if err != nil || !slices.Equal(got, []arrapi.Tag{{ID: 2, Label: "anime"}}) {
					t.Fatalf("Tags = (%v, %v), want the one tag", got, err)
				}
				wantURIs(t, f, "GET "+ec.prefix+"/tag")
			})
			t.Run("RootFolders", func(t *testing.T) {
				c, f := engineClient(t, ec)
				f.on("GET "+ec.prefix+"/rootfolder", ans(200, ec.version, `[{"id":1,"path":"/tv/","freeSpace":5}]`))
				got, err := c.RootFolders(t.Context())
				if err != nil || !slices.Equal(got, []string{"/tv/"}) {
					t.Fatalf("RootFolders = (%v, %v), want the one path", got, err)
				}
				wantURIs(t, f, "GET "+ec.prefix+"/rootfolder")
			})
		})
	}
}

func TestOnlyEpisodesWithFilesSendsItsFlag(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			calls := []func() error{
				func() error { _, err := c.Series(t.Context()); return err },
				func() error { _, _, err := c.SeriesByID(t.Context(), 1); return err },
				func() error { _, err := c.Episodes(t.Context(), 1); return err },
				func() error { _, err := c.EpisodesWithFiles(t.Context(), 1); return err },
				func() error { _, err := c.EpisodeFiles(t.Context(), 1); return err },
				func() error { _, err := c.HistorySince(t.Context(), time.Unix(0, 0)); return err },
				func() error { _, err := c.Tags(t.Context()); return err },
				func() error { _, err := c.RootFolders(t.Context()); return err },
			}
			f.on("GET "+ec.prefix+"/series/1", ans(200, ec.version, "{}"))
			for _, call := range calls {
				if err := call(); err != nil {
					t.Fatalf("call: %v", err)
				}
			}
			var flagged []string
			for _, uri := range f.requestURIs() {
				if strings.Contains(uri, "includeEpisodeFile") || strings.Contains(uri, "includeSubresources") {
					flagged = append(flagged, uri)
				}
			}
			want := []string{"GET " + ec.prefix + "/episode?seriesId=1&" + ec.filesFlag}
			if !slices.Equal(flagged, want) {
				t.Errorf("requests carrying a files flag = %q, want %q", flagged, want)
			}
		})
	}
}

// TestEpisodesWithFilesPersistent500IsStatus: Sonarr 5 before 5.0.0.2701 answers an
// embedded-file episode read with a 500 while an episode names a file whose row is
// gone (Sonarr#9162), written by its exception handler without X-Application-Version
// (Sonarr#9161). docs/request-behavior.md "Retries": a cached read's header-less 5xx
// moves once, then retries on that detection, for at most WithMaxAttempts sends
// (default 3), and returns the 500.
func TestEpisodesWithFilesPersistent500IsStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const pre2701 = "5.0.0.2668"
		episodes := "GET /api/v5/episode"
		f := newFake().sonarr5(pre2701)
		c := newClient(t, f)
		f.on(v5Series, ans(200, pre2701, v5SeriesList))
		cached(t, c, f)
		f.on(episodes, ans(500, "", `{"message":"Object reference not set to an instance of an object."}`))
		got, err := c.EpisodesWithFiles(t.Context(), 1)
		wantStatus(t, err, 500)
		if got != nil {
			t.Errorf("EpisodesWithFiles returned %+v with its error", got)
		}
		if n := count(f, episodes); n != 3 {
			t.Errorf("sent %d episode reads, want 3", n)
		}
		if n := count(f, "GET /api"); n != 1 {
			t.Errorf("made %d detections, want 1", n)
		}
		for _, uri := range f.requestURIs() {
			if strings.HasPrefix(uri, episodes) && uri != episodes+"?seriesId=1&includeSubresources=episodeFile" {
				t.Errorf("episode read %q, want the embedded-file query", uri)
			}
		}
	})
}

func TestHistorySinceFiltersClientSide(t *testing.T) {
	const records = `[
		{"id":1,"seriesId":1,"date":"2001-01-01T00:00:01Z","eventType":"grabbed"},
		{"id":2,"seriesId":1,"date":"2001-01-01T00:00:02Z","eventType":"somethingNew"},
		{"id":3,"seriesId":1,"date":"2001-01-01T00:00:03Z","eventType":"episodeFileDeleted"},
		{"id":4,"seriesId":1,"date":"2001-01-01T00:00:04Z","eventType":"grabbed"}
	]`
	ids := func(recs []HistoryRecord) []int {
		out := make([]int, len(recs))
		for i, r := range recs {
			out[i] = r.ID
		}
		return out
	}
	for name, tc := range map[string]struct {
		types []EventType
		want  []int
	}{
		"no types keeps every record, unknown kinds included": {nil, []int{1, 2, 3, 4}},
		"listed types in the server's order":                  {[]EventType{EventFileDeleted, EventGrabbed}, []int{1, 3, 4}},
		"an unlisted type keeps nothing":                      {[]EventType{EventDownloadFailed}, []int{}},
	} {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, engineCases()["Sonarr v4 (API v3)"])
			f.on("GET /api/v3/history/since", ans(200, v4, records))
			got, err := c.HistorySince(t.Context(), time.Unix(0, 0), tc.types...)
			if err != nil {
				t.Fatalf("HistorySince: %v", err)
			}
			if !slices.Equal(ids(got), tc.want) {
				t.Errorf("HistorySince ids = %v, want %v", ids(got), tc.want)
			}
			if n := len(f.requestURIs()); n != 1 {
				t.Errorf("HistorySince sent %d requests, want 1", n)
			}
		})
	}
}

func TestHistorySinceZeroTypeMatchesNothing(t *testing.T) {
	c, f := engineClient(t, engineCases()["Sonarr v4 (API v3)"])
	f.on("GET /api/v3/history/since", ans(200, v4,
		`[{"id":1,"seriesId":1,"date":"2001-01-01T00:00:01Z","eventType":"somethingNew"},
		  {"id":2,"seriesId":1,"date":"2001-01-01T00:00:02Z","eventType":"grabbed"}]`))
	got, err := c.HistorySince(t.Context(), time.Unix(0, 0), EventType(0))
	if err != nil || len(got) != 0 {
		t.Errorf("HistorySince(EventType(0)) = (%+v, %v), want no record", got, err)
	}
}

// TestHistorySinceDecodeErrorNamesTheField pins the decode error a caller sees for a
// record of a kind no server writes: the field and the type, none of the value.
func TestHistorySinceDecodeErrorNamesTheField(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			f.on("GET "+ec.prefix+"/history/since", ans(200, ec.version,
				`[{"id":1,"seriesId":1,"date":"2001-01-01T00:00:01Z","eventType":{"token":"grabbed-7f3a"}}]`))
			_, err := c.HistorySince(t.Context(), time.Unix(0, 0))
			want := "arrapi: decode " + ec.prefix + "/history/since: field eventType: a JSON object cannot decode into sonarr.EventType"
			if err == nil || err.Error() != want {
				t.Errorf("HistorySince = %v, want %q", err, want)
			}
		})
	}
}

// TestNullAnswersAreDecodeErrors pins, on both engines, that no list or by-id read
// turns a null answer, or a null list item, into an empty library or a zero-value
// resource.
func TestNullAnswersAreDecodeErrors(t *testing.T) {
	lists := map[string]struct {
		resource string
		read     func(*Client) (gotValue bool, err error)
	}{
		"Series": {"/series", func(c *Client) (bool, error) {
			got, err := c.Series(t.Context())
			return got != nil, err
		}},
		"Episodes": {"/episode", func(c *Client) (bool, error) {
			got, err := c.Episodes(t.Context(), 41)
			return got != nil, err
		}},
		"EpisodesWithFiles": {"/episode", func(c *Client) (bool, error) {
			got, err := c.EpisodesWithFiles(t.Context(), 41)
			return got != nil, err
		}},
		"EpisodeFiles": {"/episodefile", func(c *Client) (bool, error) {
			got, err := c.EpisodeFiles(t.Context(), 41)
			return got != nil, err
		}},
		"HistorySince": {"/history/since", func(c *Client) (bool, error) {
			got, err := c.HistorySince(t.Context(), time.Unix(0, 0))
			return got != nil, err
		}},
		"Tags": {"/tag", func(c *Client) (bool, error) {
			got, err := c.Tags(t.Context())
			return got != nil, err
		}},
		"RootFolders": {"/rootfolder", func(c *Client) (bool, error) {
			got, err := c.RootFolders(t.Context())
			return got != nil, err
		}},
	}
	for engineName, ec := range engineCases() {
		t.Run(engineName, func(t *testing.T) {
			for name, l := range lists {
				path := ec.prefix + l.resource
				for body, want := range map[string]string{
					`null`:            "arrapi: decode " + path + ": the answer is null",
					`[null]`:          "arrapi: decode " + path + ": list item 0 is null",
					`[{"id":1},null]`: "arrapi: decode " + path + ": list item 1 is null",
				} {
					t.Run(name+" "+body, func(t *testing.T) {
						c, f := engineClient(t, ec)
						f.on("GET "+path, ans(200, ec.version, body))
						gotValue, err := l.read(c)
						if err == nil || err.Error() != want || gotValue {
							t.Errorf("%s = (value %v, %v), want no value and %q", name, gotValue, err, want)
						}
					})
				}
			}
			t.Run("SeriesByID null", func(t *testing.T) {
				c, f := engineClient(t, ec)
				f.on("GET "+ec.prefix+"/series/9", ans(200, ec.version, `null`))
				s, found, err := c.SeriesByID(t.Context(), 9)
				want := "arrapi: decode " + ec.prefix + "/series/9: the answer is null"
				if err == nil || err.Error() != want || found || s.ID != 0 {
					t.Errorf("SeriesByID = (%+v, %v, %v), want not found and %q", s, found, err, want)
				}
			})
		})
	}
}

func TestReadErrorReturnsNoValue(t *testing.T) {
	c, f := engineClient(t, engineCases()["Sonarr v4 (API v3)"])
	f.on(v4Series, ans(200, v4, notAList))
	got, err := c.Series(t.Context())
	if err == nil || got != nil {
		t.Errorf("Series on an undecodable answer = (%+v, %v), want an error and no series", got, err)
	}
	f.on("GET /api/v3/series/3", ans(200, v4, `[]`))
	s, found, err := c.SeriesByID(t.Context(), 3)
	if err == nil || found || s.ID != 0 {
		t.Errorf("SeriesByID on an undecodable answer = (%+v, %v, %v), want an error, not found", s, found, err)
	}
}

// TestNestedNullItemIsRejected answers each nested array a Sonarr engine decodes with
// [null] and requires a decode error naming the wire field (the OpenAPI property names
// of SeriesResource, EpisodeResource and EpisodeFileResource), never a zero-value item.
func TestNestedNullItemIsRejected(t *testing.T) {
	type nested struct {
		route, body, field string
		read               func(*Client) error
	}
	series := func(c *Client) error { _, err := c.Series(t.Context()); return err }
	files := func(c *Client) error { _, err := c.EpisodeFiles(t.Context(), 41); return err }
	withFiles := func(c *Client) error { _, err := c.EpisodesWithFiles(t.Context(), 41); return err }
	byEngine := map[string][]nested{
		"Sonarr v4 (API v3)": {
			{"/series", `[{"id":1,"alternateTitles":[null]}]`, "alternateTitles", series},
			{"/series", `[{"id":1,"seasons":[null]}]`, "seasons", series},
			{"/series", `[{"id":1,"tags":[3,null]}]`, "tags", series},
		},
		"Sonarr v5 (API v5)": {
			{"/series", `[{"id":1,"translations":[null]}]`, "translations", series},
			{"/series", `[{"id":1,"alternateTitles":[null]}]`, "alternateTitles", series},
			{"/series", `[{"id":1,"seasons":[null]}]`, "seasons", series},
			{"/series", `[{"id":1,"seasonType":"dvd","seasonTypes":[null]}]`, "seasonTypes", series},
			{"/series", `[{"id":1,"tags":[3,null]}]`, "tags", series},
			{"/series", `[{"id":1,"aniListIds":[null]}]`, "aniListIds", series},
			{"/episodefile", `[{"id":1,"mediaInfo":{"audioStreams":[null]}}]`, "mediaInfo.audioStreams", files},
			{"/episode", `[{"id":1,"episodeFile":{"id":2,"mediaInfo":{"audioStreams":[{"language":"jpn"},null]}}}]`, "episodeFile.mediaInfo.audioStreams", withFiles},
		},
	}
	for engineName, ec := range engineCases() {
		for _, n := range byEngine[engineName] {
			t.Run(engineName+" "+n.field, func(t *testing.T) {
				c, f := engineClient(t, ec)
				path := ec.prefix + n.route
				f.on("GET "+path, ans(200, ec.version, n.body))
				err := n.read(c)
				idx := "0"
				if strings.Contains(n.body, ",null]") {
					idx = "1"
				}
				want := "arrapi: decode " + path + ": field " + n.field + ": item " + idx + " is null"
				if err == nil || err.Error() != want {
					t.Errorf("err = %v, want %q", err, want)
				}
			})
		}
	}
}
