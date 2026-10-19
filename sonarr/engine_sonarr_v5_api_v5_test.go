package sonarr

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
)

// The bodies below are synthetic: every field of the Sonarr v5 (API v5) resource the
// engine maps, plus fields it does not, with invented values.

const sonarrV5SeriesBody = `{
	"title": "Example Show",
	"alternateTitles": [
		{"title": "Example Alt", "seasonNumber": -1, "sceneOrigin": "tvdb", "comment": ""},
		{"title": ""},
		{"title": null},
		{"title": "Second Alt", "sceneSeasonNumber": 2}
	],
	"sortTitle": "example show",
	"status": "continuing",
	"ended": false,
	"profileName": "HD",
	"overview": "An invented overview.",
	"network": "Example Network",
	"airTime": "01:00",
	"images": [{"coverType": "poster", "url": "/MediaCover/41/poster.jpg"}],
	"language": {"id": 1, "name": "English"},
	"originalLanguage": {"id": 8, "name": "Japanese"},
	"seasonType": "dvd",
	"seasons": [
		{"seasonNumber": 0, "monitored": false, "title": null, "statistics": {"episodeFileCount": 0}},
		{"seasonNumber": 1, "monitored": true},
		{"seasonNumber": 2, "monitored": true, "title": "Second Arc"}
	],
	"seasonTypes": [
		{"name": "Aired Order", "type": "official", "seasonNumbers": [0, 1, 2], "episodeCount": 25},
		{"name": "DVD Order", "type": "dvd", "seasonNumbers": [0, 1, 2], "episodeCount": 26}
	],
	"translations": [
		{"language": {"id": 8, "name": "Japanese"}, "title": "Rei no Shou", "overview": "x"},
		{"language": {"id": 4, "name": "German"}, "title": "Beispielserie", "overview": null}
	],
	"year": 2011,
	"path": "/tv/example-show",
	"qualityProfileId": 4,
	"seasonFolder": true,
	"monitored": true,
	"monitorNewItems": "all",
	"useSceneNumbering": false,
	"runtime": 24,
	"tvdbId": 900001,
	"tvRageId": 0,
	"tvMazeId": 700002,
	"tmdbId": 800003,
	"malIds": [6],
	"aniListIds": [97001, 15, 3004],
	"firstAired": "2011-04-03T00:00:00Z",
	"seriesType": "anime",
	"originalTitle": "Rei no Shou",
	"cleanTitle": "exampleshow",
	"cleanOriginalTitle": "reinoshou",
	"imdbId": "tt0000004",
	"titleSlug": "example-show",
	"rootFolderPath": "/tv/",
	"folder": "example-show",
	"folders": [],
	"genres": ["Animation"],
	"originalCountry": "jp",
	"tags": [3, 1],
	"added": "2031-07-09T13:57:11Z",
	"ratings": {"votes": 5, "value": 8.1},
	"statistics": {"seasonCount": 2, "episodeFileCount": 12, "episodeCount": 25, "totalEpisodeCount": 26,
		"monitoredEpisodeCount": 25, "sizeOnDisk": 7777777777, "releaseGroups": ["GRP"], "percentOfEpisodes": 48},
	"arrapiUnknownField": 1,
	"id": 41
}`

func TestSonarrV5APIv5SeriesMapping(t *testing.T) {
	w := decodeWire[sonarrV5Series](t, sonarrV5SeriesBody)
	assertFields(t, w.series(), Series{
		OriginalLanguage: &arrapi.Language{ID: 8, Name: "Japanese"},
		Language:         &arrapi.Language{ID: 1, Name: "English"},
		Title:            "Example Show",
		OriginalTitle:    "Rei no Shou",
		TitleSlug:        "example-show",
		Translations: []Translation{
			{Title: "Rei no Shou", Language: arrapi.Language{ID: 8, Name: "Japanese"}},
			{Title: "Beispielserie", Language: arrapi.Language{ID: 4, Name: "German"}},
		},
		AlternateTitles: []string{"Example Alt", "Second Alt"},
		Seasons:         []Season{{Number: 0}, {Number: 1}, {Number: 2, Title: "Second Arc"}},
		Tags:            []int{3, 1},
		Order:           Order{Type: "dvd", Name: "DVD Order"},
		IDs:             ExternalIDs{IMDb: "tt0000004", AniList: []int{15, 3004, 97001}, TVDB: 900001, TMDB: 800003},
		ID:              41,
		Year:            2011,
		FileCount:       12,
	})
}

func TestSonarrV5APIv5SeriesNulls(t *testing.T) {
	w := decodeWire[sonarrV5Series](t, `{"id": 7, "title": "Bare", "originalLanguage": null,
		"language": null, "originalTitle": null, "seasonType": null, "seasonTypes": null,
		"translations": null, "alternateTitles": null, "seasons": null, "tags": null,
		"aniListIds": null, "statistics": null, "imdbId": null, "titleSlug": null,
		"tvdbId": 0, "tmdbId": 0, "year": 0}`)
	assertFields(t, w.series(), Series{Title: "Bare", ID: 7})
}

func TestSonarrV5APIv5SeriesEmptyLists(t *testing.T) {
	w := decodeWire[sonarrV5Series](t, `{"id": 7, "seasonType": "official", "seasonTypes": [],
		"translations": [], "alternateTitles": [{"title": ""}], "seasons": [], "tags": [],
		"aniListIds": [], "statistics": {"episodeFileCount": 0}}`)
	assertFields(t, w.series(), Series{
		Translations:    []Translation{},
		AlternateTitles: []string{},
		Seasons:         []Season{},
		Tags:            []int{},
		Order:           Order{Type: "official", Official: true},
		IDs:             ExternalIDs{AniList: []int{}},
		ID:              7,
	})
}

func TestSonarrV5APIv5TranslationNullLanguage(t *testing.T) {
	w := decodeWire[sonarrV5Series](t, `{"id": 7, "translations": [{"language": null, "title": "T"}]}`)
	got := w.series().Translations
	if len(got) != 1 {
		t.Fatalf("Translations = %v, want one entry", got)
	}
	assertFields(t, got[0], Translation{Title: "T"})
}

func TestSonarrV5APIv5SeriesOrder(t *testing.T) {
	const officialAndDVD = `[{"name": "Aired Order", "type": "official"}, {"name": "DVD Order", "type": "dvd"}]`
	tests := []struct {
		name        string
		seasonType  string
		seasonTypes string
		want        Order
	}{
		{"official", `"official"`, officialAndDVD, Order{Type: "official", Name: "Aired Order", Official: true}},
		{"dvd", `"dvd"`, officialAndDVD, Order{Type: "dvd", Name: "DVD Order"}},
		{
			"alttwo named by the series", `"alttwo"`,
			`[{"name": "Aired Order", "type": "official"}, {"name": "Superfan Order", "type": "alttwo"}]`,
			Order{Type: "alttwo", Name: "Superfan Order"},
		},
		{
			"alternate", `"alternate"`,
			`[{"name": "Aired Order", "type": "official"}, {"name": "Book Order", "type": "alternate"}]`,
			Order{Type: "alternate", Name: "Book Order"},
		},
		{"empty seasonTypes keeps the type", `"alternate"`, `[]`, Order{Type: "alternate"}},
		{"null seasonTypes keeps the type", `"dvd"`, `null`, Order{Type: "dvd"}},
		{"official with empty seasonTypes", `"official"`, `[]`, Order{Type: "official", Official: true}},
		{"no entry of the type", `"alttwo"`, officialAndDVD, Order{Type: "alttwo"}},
		{
			"first entry of the type names it", `"dvd"`,
			`[{"name": "DVD Order", "type": "dvd"}, {"name": "Second DVD", "type": "dvd"}]`,
			Order{Type: "dvd", Name: "DVD Order"},
		},
		{"type matched exactly", `"DVD"`, officialAndDVD, Order{Type: "DVD"}},
		{"entry with a null name", `"dvd"`, `[{"name": null, "type": "dvd"}]`, Order{Type: "dvd"}},
		{"empty seasonType", `""`, officialAndDVD, Order{}},
		{"empty seasonType beside an untyped entry", `""`, `[{"name": "Untyped", "type": null}]`, Order{}},
		{"null seasonType", `null`, officialAndDVD, Order{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := decodeWire[sonarrV5Series](t, `{"id": 1, "seasonType": `+tc.seasonType+`, "seasonTypes": `+tc.seasonTypes+`}`)
			assertFields(t, w.series().Order, tc.want)
		})
	}
}

func TestSonarrV5APIv5SeasonTitles(t *testing.T) {
	w := decodeWire[sonarrV5Series](t, `{"id": 1, "seasonType": "alternate",
		"seasons": [{"seasonNumber": 1, "title": "First Book"}, {"seasonNumber": 2, "title": ""}, {"seasonNumber": 3}]}`)
	got := w.series().Seasons
	want := []Season{{Number: 1, Title: "First Book"}, {Number: 2}, {Number: 3}}
	if len(got) != len(want) {
		t.Fatalf("Seasons = %v, want %v", got, want)
	}
	for i := range want {
		assertFields(t, got[i], want[i])
	}
}

const sonarrV5EpisodeFileBody = `{
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
	"mediaInfo": {"id": 0, "videoBitDepth": 10, "videoBitrate": 0, "videoCodec": "x265", "videoFps": 23.976,
		"videoDynamicRange": "", "videoDynamicRangeType": "", "resolution": "1920x1080", "runTime": "23:40",
		"scanType": "Progressive",
		"audioStreams": [
			{"language": "jpn", "codec": "FLAC", "channels": 2, "title": "Main"},
			{"language": " ENG ", "codec": "AAC", "channels": 2},
			{"language": "und", "codec": "AAC"},
			{"language": "UND"},
			{"language": ""},
			{"language": null},
			{"codec": "AC3"},
			{"language": "jpn", "title": "Commentary"}
		],
		"subtitleStreams": [{"language": "fre", "format": "ass", "forced": false}]},
	"qualityCutoffNotMet": false,
	"arrapiUnknownField": 1,
	"id": 55
}`

var sonarrV5EpisodeFileWant = EpisodeFile{
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

func TestSonarrV5APIv5EpisodeFileMapping(t *testing.T) {
	w := decodeWire[sonarrV5EpisodeFile](t, sonarrV5EpisodeFileBody)
	assertFields(t, w.episodeFile(), sonarrV5EpisodeFileWant)
}

func TestSonarrV5APIv5EpisodeFileNulls(t *testing.T) {
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
			"null audio streams", `{"id": 3, "mediaInfo": {"videoCodec": "h264", "audioStreams": null}}`,
			EpisodeFile{ID: 3, MediaInfo: &arrapi.MediaInfo{VideoCodec: "h264"}},
		},
		{
			"empty audio streams", `{"id": 3, "mediaInfo": {"videoCodec": "h264", "audioStreams": []}}`,
			EpisodeFile{ID: 3, MediaInfo: &arrapi.MediaInfo{VideoCodec: "h264"}},
		},
		{
			"only untagged streams", `{"id": 3, "mediaInfo": {"videoCodec": "h264",
			"audioStreams": [{"language": "und"}, {"language": " "}, {}]}}`,
			EpisodeFile{ID: 3, MediaInfo: &arrapi.MediaInfo{VideoCodec: "h264"}},
		},
		{
			"null video codec", `{"id": 3, "mediaInfo": {"videoCodec": null}}`,
			EpisodeFile{ID: 3, MediaInfo: &arrapi.MediaInfo{}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := decodeWire[sonarrV5EpisodeFile](t, tc.body)
			assertFields(t, w.episodeFile(), tc.want)
		})
	}
}

// audioLanguages returns f's AudioLanguages, failing the test when f has no MediaInfo.
func audioLanguages(t *testing.T, f EpisodeFile) []string {
	t.Helper()
	if f.MediaInfo == nil {
		t.Fatal("EpisodeFile.MediaInfo = nil, want the decoded media info")
	}
	return f.MediaInfo.AudioLanguages
}

func TestSonarrV5APIv5AudioLanguagesKeepEveryStream(t *testing.T) {
	streams := strings.Repeat(`{"language": "jpn"},{"language": "eng"},`, 50)
	w := decodeWire[sonarrV5EpisodeFile](t, `{"id": 3, "mediaInfo": {"audioStreams": [`+streams+`{"language": "fre"}]}}`)
	got := audioLanguages(t, w.episodeFile())
	if len(got) != 101 {
		t.Fatalf("AudioLanguages has %d entries, want 101", len(got))
	}
	if got[0] != "jpn" || got[1] != "eng" || got[100] != "fre" {
		t.Errorf("AudioLanguages = %q ... %q, want jpn, eng ... fre in stream order", got[:2], got[100])
	}
}

func TestSonarrV5APIv5AudioLanguageIsOneToken(t *testing.T) {
	w := decodeWire[sonarrV5EpisodeFile](t, `{"id": 3, "mediaInfo": {"audioStreams": [{"language": "jpn/eng"}]}}`)
	got := audioLanguages(t, w.episodeFile())
	if len(got) != 1 || got[0] != "jpn/eng" {
		t.Errorf("AudioLanguages = %q, want the stream's one language [\"jpn/eng\"]", got)
	}
}

const sonarrV5EpisodeBody = `{
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
	"episodeFile": ` + sonarrV5EpisodeFileBody + `,
	"hasFile": true,
	"monitored": true,
	"absoluteEpisodeNumber": 14,
	"sceneAbsoluteEpisodeNumber": 91,
	"sceneEpisodeNumber": 92,
	"sceneSeasonNumber": 93,
	"unverifiedSceneNumbering": false,
	"endTime": null,
	"grabDate": null,
	"series": null,
	"images": [],
	"arrapiUnknownField": 1,
	"id": 301
}`

func TestSonarrV5APIv5EpisodeMapping(t *testing.T) {
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
	w := decodeWire[sonarrV5Episode](t, sonarrV5EpisodeBody)

	t.Run("without files", func(t *testing.T) {
		assertFields(t, w.episode(false), want)
	})
	t.Run("with files", func(t *testing.T) {
		got := w.episode(true)
		if got.File == nil {
			t.Fatal("episode(true).File = nil, want the embedded file")
		}
		assertFields(t, *got.File, sonarrV5EpisodeFileWant)
		got.File = nil
		assertFields(t, got, want)
	})
}

func TestSonarrV5APIv5EpisodeWithoutFile(t *testing.T) {
	w := decodeWire[sonarrV5Episode](t, `{"id": 302, "seriesId": 41, "tvdbId": 0, "episodeFileId": 0,
		"seasonNumber": 0, "episodeNumber": 1, "title": null, "airDate": null, "hasFile": false,
		"absoluteEpisodeNumber": null, "episodeFile": null}`)
	want := Episode{ID: 302, SeriesID: 41, Number: 1}
	assertFields(t, w.episode(true), want)
	assertFields(t, w.episode(false), want)
}

func TestSonarrV5APIv5EpisodeTVDBIDPassesThrough(t *testing.T) {
	for _, id := range []int{0, 328515} {
		w := sonarrV5Episode{ID: 1, TVDBID: id}
		if got := w.episode(false).TVDBID; got != id {
			t.Errorf("tvdbId %d mapped to TVDBID %d", id, got)
		}
	}
}

func TestSonarrV5APIv5HistoryMapping(t *testing.T) {
	w := decodeWire[sonarrV5History](t, `{"id": 9, "episodeId": 301, "seriesId": 41, "sourceTitle": "x",
		"languages": [], "quality": null, "customFormats": [], "customFormatScore": 0,
		"qualityCutoffNotMet": false, "date": "2031-07-09T13:57:11Z", "downloadId": "abc",
		"eventType": "downloadFolderImported", "data": {"k": "v"}, "episode": null, "series": null,
		"arrapiUnknownField": 1}`)
	got := w.record()
	if want := time.Date(2031, 7, 9, 13, 57, 11, 0, time.UTC); !got.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", got.Date, want)
	}
	got.Date = time.Time{}
	assertFields(t, got, HistoryRecord{EventType: EventDownloadImported, ID: 9, SeriesID: 41})
}

func TestSonarrV5APIv5TagMapping(t *testing.T) {
	w := decodeWire[sonarrV5Tag](t, `{"label": "anime", "arrapiUnknownField": 1, "id": 3}`)
	assertFields(t, w.tag(), arrapi.Tag{Label: "anime", ID: 3})
}

func TestSonarrV5APIv5RootFolderMapping(t *testing.T) {
	w := decodeWire[sonarrV5RootFolder](t, `{"path": "/tv", "accessible": true, "isEmpty": false,
		"freeSpace": 1000, "totalSpace": 2000, "unmappedFolders": [], "arrapiUnknownField": 1, "id": 1}`)
	if got := w.path(); got != "/tv" {
		t.Errorf("path() = %q, want %q", got, "/tv")
	}
}

func TestSonarrV5APIv5Features(t *testing.T) {
	assertFields(t, sonarrV5APIv5{}.features(), Features{
		SeriesOrders: true,
		SeasonTitles: true,
		Translations: true,
		AnimeIDs:     true,
	})
}

// The tests below read the recorded Sonarr 5.0 answers of testdata/sonarr/v5-api-v5/
// through a client of an httptest server replaying them.

func codeGeassSonarrV5APIv5() Series {
	tr := func(id int, name, title string) Translation {
		return Translation{Title: title, Language: arrapi.Language{Name: name, ID: id}}
	}
	return Series{
		OriginalLanguage: &arrapi.Language{Name: "Japanese", ID: 8},
		Language:         &arrapi.Language{Name: "English", ID: 1},
		Title:            "Code Geass: Lelouch of the Rebellion",
		OriginalTitle:    "コードギアス 反逆のルルーシュ",
		TitleSlug:        "code-geass-lelouch-of-the-rebellion",
		Translations: []Translation{
			tr(4, "German", "Code Geass: Lelouch of the Rebellion"),
			tr(1, "English", "Code Geass: Lelouch of the Rebellion"),
			tr(2, "French", "Code Geass: Lelouch of the Rebellion"),
			tr(23, "Hebrew", "קוֹד גיאַס: לולוש של המהפכה"),
			tr(5, "Italian", "Code Geass: Lelouch of the Rebellion"),
			tr(8, "Japanese", "コードギアス 反逆のルルーシュ"),
			tr(21, "Korean", "코드 기아스: 반역의 를르슈"),
			tr(7, "Dutch", "Code Geass: Lelouch of the Rebellion"),
			tr(12, "Polish", "Code Geass: Lelouch of the Rebellion"),
			tr(18, "Portuguese", "Code Geass: Lelouch of the Rebellion"),
			tr(11, "Russian", "Код Гиасс: Восстание Лелуша"),
			tr(3, "Spanish", "Code Geass: Hangyaku no Lelouch"),
			tr(17, "Turkish", "Code Geass: Lelouch of the Rebellion R2"),
			tr(10, "Chinese", "反叛的鲁路修"),
			tr(10, "Chinese", "Code Geass 反叛的魯路修"),
			tr(18, "Portuguese", "Code Geass"),
			tr(26, "Arabic", "Code Geass: Hangyaku no Lelouch"),
			tr(14, "Swedish", "Code Geass - Lelouch of the Rebellion"),
		},
		AlternateTitles: codeGeassAlternateTitles(),
		Seasons:         []Season{{Number: 0}, {Number: 1}, {Title: "R2", Number: 2}},
		Tags:            []int{},
		Order:           Order{Type: "official", Name: "Aired Order", Official: true},
		IDs:             ExternalIDs{IMDb: "tt0994314", AniList: []int{1575, 2904, 12685, 97880}, TVDB: 79525, TMDB: 31724},
		ID:              1,
		Year:            2006,
		FileCount:       51,
	}
}

var sonarrV5APIv5Features = Features{SeriesOrders: true, SeasonTitles: true, Translations: true, AnimeIDs: true}

func TestSonarrV5APIv5Fixtures(t *testing.T) {
	sh := sonarrV5APIv5Shape()
	c, p := replay(t, sh, "series-codegeass.json")
	t.Run("Info", func(t *testing.T) {
		got, err := c.Info(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		assertFields(t, got, Info{Version: "5.0.0.2701", API: arrapi.APIv5, Features: sonarrV5APIv5Features})
		if want := detectV5; !slices.Equal(p.Requests(), want) {
			t.Errorf("requests = %q, want %q", p.Requests(), want)
		}
	})
	t.Run("Series", func(t *testing.T) {
		got, err := c.Series(t.Context())
		if err != nil || len(got) != 1 {
			t.Fatalf("Series = %+v, %v; want Code Geass alone", got, err)
		}
		assertFields(t, got[0], codeGeassSonarrV5APIv5())
	})
	t.Run("SeriesByID", func(t *testing.T) {
		got, ok, err := c.SeriesByID(t.Context(), 1)
		if err != nil || !ok {
			t.Fatalf("SeriesByID(1) = %t, %v", ok, err)
		}
		assertFields(t, got, codeGeassSonarrV5APIv5())
	})
	t.Run("SeriesByIDAbsent", func(t *testing.T) {
		got, ok, err := c.SeriesByID(t.Context(), absentSeriesID)
		if err != nil || ok || !reflect.DeepEqual(got, Series{}) {
			t.Errorf("SeriesByID(absent) = %+v, %t, %v; want the zero Series, false, nil", got, ok, err)
		}
	})
	recordedReads(t, c, sh, nil, HistoryRecord{Date: fixtureSince.Add(5 * time.Hour), EventType: EventFileRenamed, ID: 9, SeriesID: 6})
}

// TestSonarrV5APIv5FixtureDetectionBehindHiddenAPI hides /api, so detection reads
// Sonarr 5.0's recorded /api/v5/system/status and sends nothing to /api/v3.
func TestSonarrV5APIv5FixtureDetectionBehindHiddenAPI(t *testing.T) {
	c, p := replayDir(t, fixtureDir(dirSonarrV5APIv5), sonarrRoutes(t, sonarrV5APIv5Shape(), "series-codegeass.json"), hideAPI)
	got, err := c.Info(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertFields(t, got, Info{Version: "5.0.0.2701", API: arrapi.APIv5, Features: sonarrV5APIv5Features})
	if want := []string{"GET /api/v5/system/status"}; !slices.Equal(p.Requests(), want) {
		t.Errorf("replayed requests = %q, want %q", p.Requests(), want)
	}
}

// TestSonarrV5APIv5FixtureMissingParentIsStatus replays Sonarr 5.0's recorded answer
// for the files of a series it does not have. From 5.0.0.2701 its exception pipeline
// sends X-Application-Version (Sonarr#9161), so the 404 is the call's error and the
// detection stays current.
func TestSonarrV5APIv5FixtureMissingParentIsStatus(t *testing.T) {
	c, p := replay(t, sonarrV5APIv5Shape(), "series-codegeass.json")
	if _, err := c.Tags(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err := c.EpisodeFiles(t.Context(), absentSeriesID)
	if se := wantStatus(t, err, 404); se.Message != "error-2" {
		t.Errorf("Message = %q, want the recorded %q", se.Message, "error-2")
	}
	if _, err := c.Tags(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := join(detectV5, []string{"GET /api/v5/tag", "GET " + seriesQuery(sonarrV5Prefix, "episodefile", absentSeriesID), "GET /api/v5/tag"})
	if !slices.Equal(p.Requests(), want) {
		t.Errorf("requests =\n  %q\nwant\n  %q", p.Requests(), want)
	}
}

// TestSonarrV5APIv5HeaderlessMissingParentDetectsNext: a Sonarr 5 before 5.0.0.2701
// wrote the same 404 without X-Application-Version (Sonarr#9161), which names no
// server, so the call gets the 404 and the next call detects first.
func TestSonarrV5APIv5HeaderlessMissingParentDetectsNext(t *testing.T) {
	f := newFake().sonarr5(v5)
	c := newClient(t, f)
	cached(t, c, f)
	f.on("GET /api/v5/episodefile", missingParent404)
	f.on("GET /api/v5/tag", ans(200, v5, `[]`))
	_, err := c.EpisodeFiles(t.Context(), absentSeriesID)
	wantStatus(t, err, 404)
	if _, err := c.Tags(t.Context()); err != nil {
		t.Fatal(err)
	}
	wantRequests(t, f, join([]string{"GET /api/v5/episodefile"}, detectV5, []string{"GET /api/v5/tag"})...)
}
