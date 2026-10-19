package sonarr

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
)

// The bodies below are synthetic: every field of the Sonarr v4 (API v3) resource the
// engine maps, plus fields it does not, with invented values.

const sonarrV4SeriesBody = `{
	"title": "Example Show",
	"alternateTitles": [
		{"title": "Example Alt", "seasonNumber": -1, "sceneOrigin": "tvdb", "comment": ""},
		{"title": ""},
		{"title": "Second Alt", "sceneSeasonNumber": 2}
	],
	"sortTitle": "example show",
	"status": "continuing",
	"ended": false,
	"overview": "An invented overview.",
	"network": "Example Network",
	"images": [{"coverType": "poster", "url": "/MediaCover/41/poster.jpg"}],
	"originalLanguage": {"id": 8, "name": "Japanese"},
	"seasons": [
		{"seasonNumber": 0, "monitored": false, "statistics": {"episodeFileCount": 0}},
		{"seasonNumber": 1, "monitored": true},
		{"seasonNumber": 2, "monitored": true}
	],
	"year": 2011,
	"path": "/tv/example-show",
	"qualityProfileId": 4,
	"seasonFolder": true,
	"monitored": true,
	"useSceneNumbering": false,
	"runtime": 24,
	"tvdbId": 900001,
	"tvRageId": 0,
	"tvMazeId": 700002,
	"tmdbId": 800003,
	"seriesType": "anime",
	"cleanTitle": "exampleshow",
	"imdbId": "tt0000004",
	"titleSlug": "example-show",
	"genres": ["Animation"],
	"tags": [3, 1],
	"added": "2031-07-09T13:57:11Z",
	"ratings": {"votes": 5, "value": 8.1},
	"statistics": {"seasonCount": 2, "episodeFileCount": 12, "episodeCount": 25, "totalEpisodeCount": 26, "sizeOnDisk": 7777777777, "releaseGroups": ["GRP"]},
	"languageProfileId": 1,
	"seasonType": "dvd",
	"aniListIds": [5],
	"malIds": [6],
	"arrapiUnknownField": 1,
	"id": 41
}`

func TestSonarrV4APIv3SeriesMapping(t *testing.T) {
	w := decodeWire[sonarrV4Series](t, sonarrV4SeriesBody)
	assertFields(t, w.series(), Series{
		OriginalLanguage: &arrapi.Language{ID: 8, Name: "Japanese"},
		Title:            "Example Show",
		TitleSlug:        "example-show",
		IDs:              ExternalIDs{IMDb: "tt0000004", TVDB: 900001, TMDB: 800003},
		Order:            Order{Type: "official", Official: true},
		AlternateTitles:  []string{"Example Alt", "Second Alt"},
		Seasons:          []Season{{Number: 0}, {Number: 1}, {Number: 2}},
		Tags:             []int{3, 1},
		ID:               41,
		Year:             2011,
		FileCount:        12,
	})
}

func TestSonarrV4APIv3SeriesNulls(t *testing.T) {
	w := decodeWire[sonarrV4Series](t, `{"id": 7, "title": "Bare", "originalLanguage": null,
		"alternateTitles": null, "seasons": null, "tags": null, "statistics": null,
		"imdbId": null, "titleSlug": null, "tvdbId": 0, "tmdbId": 0, "year": 0}`)
	assertFields(t, w.series(), Series{
		Title: "Bare",
		Order: Order{Type: "official", Official: true},
		ID:    7,
	})
}

func TestSonarrV4APIv3SeriesEmptyLists(t *testing.T) {
	w := decodeWire[sonarrV4Series](t, `{"id": 7, "alternateTitles": [{"title": ""}],
		"seasons": [], "tags": [], "statistics": {"episodeFileCount": 0}}`)
	assertFields(t, w.series(), Series{
		Order:           Order{Type: "official", Official: true},
		AlternateTitles: []string{},
		Seasons:         []Season{},
		Tags:            []int{},
		ID:              7,
	})
}

const sonarrV4EpisodeFileBody = `{
	"seriesId": 41,
	"seasonNumber": 1,
	"relativePath": "Season 01/Example Show - S01E02.mkv",
	"path": "/tv/example-show/Season 01/Example Show - S01E02.mkv",
	"size": 7340032001,
	"dateAdded": "2031-07-09T13:57:11Z",
	"sceneName": "Example.Show.S01E02.1080p.BluRay-GRP",
	"releaseGroup": "GRP",
	"languages": [{"id": 8, "name": "Japanese"}],
	"quality": {"quality": {"id": 7, "name": "Bluray-1080p", "source": "bluray", "resolution": 1080},
		"revision": {"version": 2, "real": 1, "isRepack": true}},
	"customFormats": [],
	"customFormatScore": 0,
	"indexerFlags": 0,
	"releaseType": "singleEpisode",
	"mediaInfo": {"audioBitrate": 0, "audioChannels": 2, "audioCodec": "AAC",
		"audioLanguages": "jpn/ENG / und//jpn", "audioStreamCount": 4, "videoBitDepth": 10,
		"videoBitrate": 0, "videoCodec": "x265", "videoFps": 23.976, "videoDynamicRange": "",
		"videoDynamicRangeType": "", "resolution": "1920x1080", "runTime": "23:40",
		"scanType": "Progressive", "subtitles": "eng/jpn"},
	"qualityCutoffNotMet": false,
	"arrapiUnknownField": 1,
	"id": 55
}`

var sonarrV4EpisodeFileWant = EpisodeFile{
	MediaInfo:    &arrapi.MediaInfo{VideoCodec: "x265", AudioLanguages: []string{"jpn", "eng", "jpn"}},
	Revision:     &arrapi.Revision{Version: 2, IsRepack: true},
	RelativePath: "Season 01/Example Show - S01E02.mkv",
	Path:         "/tv/example-show/Season 01/Example Show - S01E02.mkv",
	SceneName:    "Example.Show.S01E02.1080p.BluRay-GRP",
	ReleaseGroup: "GRP",
	ID:           55,
	Season:       1,
	Size:         7340032001,
}

func TestSonarrV4APIv3EpisodeFileMapping(t *testing.T) {
	w := decodeWire[sonarrV4EpisodeFile](t, sonarrV4EpisodeFileBody)
	assertFields(t, w.episodeFile(), sonarrV4EpisodeFileWant)
}

func TestSonarrV4APIv3EpisodeFileNulls(t *testing.T) {
	tests := []struct {
		name string
		body string
		want EpisodeFile
	}{
		{
			"null quality, media info and strings", `{"id": 3, "seasonNumber": 0, "quality": null, "mediaInfo": null,
			"relativePath": null, "path": null, "sceneName": null, "releaseGroup": null, "size": 0}`,
			EpisodeFile{ID: 3},
		},
		{
			"null revision", `{"id": 3, "quality": {"quality": {"id": 1}, "revision": null}}`,
			EpisodeFile{ID: 3},
		},
		{
			"media info without audio languages", `{"id": 3, "mediaInfo": {"videoCodec": "h264", "audioLanguages": ""}}`,
			EpisodeFile{ID: 3, MediaInfo: &arrapi.MediaInfo{VideoCodec: "h264"}},
		},
		{
			"media info with only und", `{"id": 3, "mediaInfo": {"videoCodec": "h264", "audioLanguages": "und"}}`,
			EpisodeFile{ID: 3, MediaInfo: &arrapi.MediaInfo{VideoCodec: "h264"}},
		},
		{
			"media info with null fields", `{"id": 3, "mediaInfo": {"videoCodec": null, "audioLanguages": null}}`,
			EpisodeFile{ID: 3, MediaInfo: &arrapi.MediaInfo{}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := decodeWire[sonarrV4EpisodeFile](t, tc.body)
			assertFields(t, w.episodeFile(), tc.want)
		})
	}
}

const sonarrV4EpisodeBody = `{
	"seriesId": 41,
	"tvdbId": 700101,
	"episodeFileId": 55,
	"seasonNumber": 1,
	"episodeNumber": 2,
	"title": "The Second One",
	"airDate": "2011-04-10",
	"airDateUtc": "2011-04-09T15:00:00Z",
	"lastSearchTime": null,
	"runtime": 24,
	"finaleType": null,
	"overview": "An invented overview.",
	"episodeFile": ` + sonarrV4EpisodeFileBody + `,
	"hasFile": true,
	"monitored": true,
	"absoluteEpisodeNumber": 14,
	"sceneAbsoluteEpisodeNumber": 91,
	"sceneEpisodeNumber": 92,
	"sceneSeasonNumber": 93,
	"unverifiedSceneNumbering": false,
	"images": [],
	"arrapiUnknownField": 1,
	"id": 301
}`

func TestSonarrV4APIv3EpisodeMapping(t *testing.T) {
	want := Episode{
		Title:    "The Second One",
		AirDate:  "2011-04-10",
		ID:       301,
		SeriesID: 41,
		Season:   1,
		Number:   2,
		Absolute: 14,
		TVDBID:   700101,
		FileID:   55,
		HasFile:  true,
	}
	w := decodeWire[sonarrV4Episode](t, sonarrV4EpisodeBody)

	t.Run("without files", func(t *testing.T) {
		assertFields(t, w.episode(false), want)
	})
	t.Run("with files", func(t *testing.T) {
		got := w.episode(true)
		if got.File == nil {
			t.Fatal("episode(true).File = nil, want the embedded file")
		}
		assertFields(t, *got.File, sonarrV4EpisodeFileWant)
		got.File = nil
		assertFields(t, got, want)
	})
}

func TestSonarrV4APIv3EpisodeWithoutFile(t *testing.T) {
	w := decodeWire[sonarrV4Episode](t, `{"id": 302, "seriesId": 41, "tvdbId": 0, "episodeFileId": 0,
		"seasonNumber": 0, "episodeNumber": 1, "title": null, "airDate": null, "hasFile": false,
		"absoluteEpisodeNumber": null}`)
	want := Episode{ID: 302, SeriesID: 41, Number: 1}
	assertFields(t, w.episode(true), want)
	assertFields(t, w.episode(false), want)
}

func TestSonarrV4APIv3TagMapping(t *testing.T) {
	w := decodeWire[sonarrV4Tag](t, `{"label": "anime", "delayProfileIds": [], "arrapiUnknownField": 1, "id": 3}`)
	assertFields(t, w.tag(), arrapi.Tag{Label: "anime", ID: 3})
}

func TestSonarrV4APIv3RootFolderMapping(t *testing.T) {
	w := decodeWire[sonarrV4RootFolder](t, `{"path": "/tv", "accessible": true, "freeSpace": 1000,
		"unmappedFolders": [{"name": "x", "path": "/tv/x"}], "arrapiUnknownField": 1, "id": 1}`)
	if got := w.path(); got != "/tv" {
		t.Errorf("path() = %q, want %q", got, "/tv")
	}
}

func TestSonarrV4APIv3Features(t *testing.T) {
	assertFields(t, sonarrV4APIv3{}.features(), Features{})
}

// The tests below read the recorded Sonarr 4.0 answers of testdata/sonarr/v4-api-v3/
// through a client of an httptest server replaying them.

func codeGeassSonarrV4APIv3() Series {
	return Series{
		OriginalLanguage: &arrapi.Language{Name: "Japanese", ID: 8},
		Title:            "Code Geass: Lelouch of the Rebellion",
		TitleSlug:        "code-geass-lelouch-of-the-rebellion",
		AlternateTitles:  codeGeassAlternateTitles(),
		Seasons:          []Season{{Number: 0}, {Number: 1}, {Number: 2}},
		Tags:             []int{},
		Order:            Order{Type: "official", Official: true},
		IDs:              ExternalIDs{IMDb: "tt0994314", TVDB: 79525, TMDB: 31724},
		ID:               1,
		Year:             2006,
		FileCount:        51,
	}
}

func TestSonarrV4APIv3Fixtures(t *testing.T) {
	sh := sonarrV4APIv3Shape()
	c, p := replay(t, sh, "series-codegeass.json")
	t.Run("Info", func(t *testing.T) {
		got, err := c.Info(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		assertFields(t, got, Info{Version: "4.0.20.3014", API: arrapi.APIv3, Features: Features{}})
		if want := detectV4; !slices.Equal(p.Requests(), want) {
			t.Errorf("requests = %q, want %q", p.Requests(), want)
		}
	})
	t.Run("Series", func(t *testing.T) {
		got, err := c.Series(t.Context())
		if err != nil || len(got) != 1 {
			t.Fatalf("Series = %+v, %v; want Code Geass alone", got, err)
		}
		assertFields(t, got[0], codeGeassSonarrV4APIv3())
	})
	t.Run("SeriesByID", func(t *testing.T) {
		got, ok, err := c.SeriesByID(t.Context(), 1)
		if err != nil || !ok {
			t.Fatalf("SeriesByID(1) = %t, %v", ok, err)
		}
		assertFields(t, got, codeGeassSonarrV4APIv3())
	})
	t.Run("SeriesByIDAbsent", func(t *testing.T) {
		got, ok, err := c.SeriesByID(t.Context(), absentSeriesID)
		if err != nil || ok || !reflect.DeepEqual(got, Series{}) {
			t.Errorf("SeriesByID(absent) = %+v, %t, %v; want the zero Series, false, nil", got, ok, err)
		}
	})
	recordedReads(t, c, sh, map[int]int{1: 328515, 26: 359905, 54: 6915211, 63: 7607145, 102: 7737005},
		HistoryRecord{Date: fixtureSince.Add(4 * time.Hour), EventType: EventGrabbed, ID: 8, SeriesID: 3})
}

// TestSonarrV4APIv3FixtureDetectionBehindHiddenAPI hides /api, so detection reads the
// recorded answers of the fallback probes: Sonarr 4.0's 404 to /api/v5/system/status,
// then its /api/v3/system/status.
func TestSonarrV4APIv3FixtureDetectionBehindHiddenAPI(t *testing.T) {
	c, p := replayDir(t, fixtureDir(dirSonarrV4APIv3), sonarrRoutes(t, sonarrV4APIv3Shape(), "series-codegeass.json"), hideAPI)
	got, err := c.Info(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertFields(t, got, Info{Version: "4.0.20.3014", API: arrapi.APIv3, Features: Features{}})
	if want := []string{"GET /api/v5/system/status", "GET /api/v3/system/status"}; !slices.Equal(p.Requests(), want) {
		t.Errorf("replayed requests = %q, want %q", p.Requests(), want)
	}
}
