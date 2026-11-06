package radarr

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/fixturegen"
)

// The bodies below are synthetic: every field of the Radarr (API v3) resource the
// engine maps, plus fields it does not, with invented values.

const radarrMovieFileBody = `{
	"movieId": 41,
	"relativePath": "Example Movie (2011).mkv",
	"path": "/movies/Example Movie (2011)/Example Movie (2011).mkv",
	"size": 7340032001,
	"dateAdded": "2031-07-09T13:57:11Z",
	"sceneName": "Example.Movie.2011.1080p.BluRay-GRP",
	"releaseGroup": "GRP",
	"edition": "Director's Cut",
	"languages": [{"id": 8, "name": "Japanese"}],
	"quality": {"quality": {"id": 7, "name": "Bluray-1080p", "source": "bluray", "resolution": 1080, "modifier": "none"},
		"revision": {"version": 2, "real": 1, "isRepack": true}},
	"customFormats": [],
	"customFormatScore": 0,
	"indexerFlags": 0,
	"mediaInfo": {"id": 0, "audioBitrate": 0, "audioChannels": 2, "audioCodec": "AAC",
		"audioLanguages": "jpn/ ENG /und//jpn", "audioStreamCount": 4, "videoBitDepth": 10,
		"videoBitrate": 0, "videoCodec": "x265", "videoFps": 23.976, "videoDynamicRange": "",
		"videoDynamicRangeType": "", "resolution": "1920x1080", "runTime": "1:52:40",
		"scanType": "Progressive", "subtitles": "eng/jpn"},
	"originalFilePath": "Example.Movie.2011.1080p.BluRay-GRP/x.mkv",
	"qualityCutoffNotMet": false,
	"arrapiUnknownField": 1,
	"id": 55
}`

var radarrMovieFileWant = MovieFile{
	MediaInfo:    &arrapi.MediaInfo{VideoCodec: "x265", AudioLanguages: []string{"jpn", "eng", "jpn"}},
	Revision:     &arrapi.Revision{Version: 2, IsRepack: true},
	RelativePath: "Example Movie (2011).mkv",
	Path:         "/movies/Example Movie (2011)/Example Movie (2011).mkv",
	SceneName:    "Example.Movie.2011.1080p.BluRay-GRP",
	ReleaseGroup: "GRP",
	ID:           55,
	Size:         7340032001,
}

const radarrMovieBody = `{
	"title": "Example Movie",
	"originalTitle": "Rei no Eiga",
	"originalLanguage": {"id": 8, "name": "Japanese"},
	"alternateTitles": [
		{"sourceType": "tmdb", "movieMetadataId": 3, "title": "Example Alt", "cleanTitle": "examplealt", "id": 1},
		{"title": ""},
		{"sourceType": "mappings", "title": "Second Alt"}
	],
	"secondaryYear": null,
	"secondaryYearSourceId": 0,
	"sortTitle": "example movie",
	"sizeOnDisk": 7340032001,
	"status": "released",
	"overview": "An invented overview.",
	"inCinemas": "2011-04-09T00:00:00Z",
	"physicalRelease": "2011-09-01T00:00:00Z",
	"digitalRelease": null,
	"releaseDate": "2011-09-01T00:00:00Z",
	"images": [{"coverType": "poster", "url": "/MediaCover/41/poster.jpg"}],
	"website": "",
	"year": 2011,
	"youTubeTrailerId": "",
	"studio": "Example Studio",
	"path": "/movies/Example Movie (2011)",
	"qualityProfileId": 4,
	"hasFile": true,
	"movieFileId": 55,
	"monitored": true,
	"minimumAvailability": "released",
	"isAvailable": true,
	"folderName": "/movies/Example Movie (2011)",
	"runtime": 112,
	"cleanTitle": "examplemovie",
	"imdbId": "tt0000004",
	"tmdbId": 800003,
	"titleSlug": "800003",
	"rootFolderPath": "/movies/",
	"certification": "PG",
	"genres": ["Animation"],
	"keywords": [],
	"tags": [3, 1],
	"added": "2031-07-09T13:57:11Z",
	"ratings": {"tmdb": {"votes": 5, "value": 8.1, "type": "user"}},
	"movieFile": ` + radarrMovieFileBody + `,
	"collection": {"title": "Example Collection", "tmdbId": 900001},
	"popularity": 3.5,
	"lastSearchTime": null,
	"statistics": {"movieFileCount": 1, "sizeOnDisk": 7340032001, "releaseGroups": ["GRP"]},
	"arrapiUnknownField": 1,
	"id": 41
}`

func TestRadarrAPIv3MovieMapping(t *testing.T) {
	w := decodeWire[radarrMovie](t, radarrMovieBody)
	got := w.movie()
	if got.File == nil {
		t.Fatal("movie().File = nil, want the embedded file")
	}
	assertFields(t, *got.File, radarrMovieFileWant)
	got.File = nil
	assertFields(t, got, Movie{
		OriginalLanguage: &arrapi.Language{ID: 8, Name: "Japanese"},
		Title:            "Example Movie",
		IDs:              ExternalIDs{IMDb: "tt0000004", TMDB: 800003},
		AlternateTitles:  []string{"Example Alt", "Second Alt"},
		Tags:             []int{3, 1},
		ID:               41,
		Year:             2011,
		HasFile:          true,
		Monitored:        true,
	})
}

func TestRadarrAPIv3MovieNulls(t *testing.T) {
	w := decodeWire[radarrMovie](t, `{"id": 7, "title": "Bare", "originalLanguage": null,
		"alternateTitles": null, "tags": null, "movieFile": null, "hasFile": null,
		"imdbId": null, "tmdbId": 0, "year": 0}`)
	assertFields(t, w.movie(), Movie{Title: "Bare", ID: 7})
}

func TestRadarrAPIv3MovieEmptyLists(t *testing.T) {
	w := decodeWire[radarrMovie](t, `{"id": 7, "alternateTitles": [{"title": ""}], "tags": [], "hasFile": false}`)
	assertFields(t, w.movie(), Movie{AlternateTitles: []string{}, Tags: []int{}, ID: 7})
}

func TestRadarrAPIv3MovieFileNulls(t *testing.T) {
	tests := []struct {
		name string
		body string
		want MovieFile
	}{
		{
			"null quality, media info and strings", `{"id": 3, "quality": null, "mediaInfo": null,
			"relativePath": null, "path": null, "sceneName": null, "releaseGroup": null, "size": 0}`,
			MovieFile{ID: 3},
		},
		{
			"null revision", `{"id": 3, "quality": {"quality": {"id": 1}, "revision": null}}`,
			MovieFile{ID: 3},
		},
		{
			"media info without audio languages", `{"id": 3, "mediaInfo": {"videoCodec": "h264", "audioLanguages": ""}}`,
			MovieFile{ID: 3, MediaInfo: &arrapi.MediaInfo{VideoCodec: "h264"}},
		},
		{
			"media info with only und and empty tokens", `{"id": 3, "mediaInfo": {"videoCodec": "h264", "audioLanguages": "und// UND /"}}`,
			MovieFile{ID: 3, MediaInfo: &arrapi.MediaInfo{VideoCodec: "h264"}},
		},
		{
			"media info with null fields", `{"id": 3, "mediaInfo": {"videoCodec": null, "audioLanguages": null}}`,
			MovieFile{ID: 3, MediaInfo: &arrapi.MediaInfo{}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := decodeWire[radarrMovieFile](t, tc.body)
			assertFields(t, w.movieFile(), tc.want)
		})
	}
}

func TestRadarrAPIv3TagMapping(t *testing.T) {
	w := decodeWire[radarrTag](t, `{"label": "anime", "delayProfileIds": [], "arrapiUnknownField": 1, "id": 3}`)
	assertFields(t, w.tag(), arrapi.Tag{Label: "anime", ID: 3})
}

func TestRadarrAPIv3RootFolderMapping(t *testing.T) {
	w := decodeWire[radarrRootFolder](t, `{"path": "/movies", "accessible": true, "freeSpace": 1000,
		"unmappedFolders": [{"name": "x", "path": "/movies/x"}], "arrapiUnknownField": 1, "id": 1}`)
	if got := w.path(); got != "/movies" {
		t.Errorf("path() = %q, want %q", got, "/movies")
	}
}

// The tests below read the recorded Radarr answers of testdata/radarr/api-v3/ through
// a client of an httptest server replaying them.

var fixtureSince = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// recordedMovieFiles are the by-id fixture files in the order of recordedMovies.
var recordedMovieFiles = []string{"movie-withfile.json", "movie-nofile.json"}

// fixtureMovieID returns the row id file records, so a re-record that moves the
// synthesized ids still routes the request.
func fixtureMovieID(t *testing.T, file string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", dirRadarrAPIv3, file))
	if err != nil {
		t.Fatalf("%v; %s", err, reRecord)
	}
	v, err := fixturegen.Decode(b)
	obj, ok := v.(map[string]any)
	if err != nil || !ok {
		t.Fatalf("%s is not a JSON object; %s", file, reRecord)
	}
	id, ok := fixturegen.Int(obj["id"])
	if !ok {
		t.Fatalf("%s records no integer id; %s", file, reRecord)
	}
	return int(id)
}

// recordedCommand is one item of the recorded commands.json, read without the engine.
type recordedCommand struct {
	name string
	id   int
}

func recordedCommands(t *testing.T) []recordedCommand {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", dirRadarrAPIv3, "commands.json"))
	if err != nil {
		t.Fatalf("%v; %s", err, reRecord)
	}
	v, err := fixturegen.Decode(b)
	items, ok := v.([]any)
	if err != nil || !ok || len(items) == 0 {
		t.Fatalf("commands.json is not a non-empty JSON list; %s", reRecord)
	}
	out := make([]recordedCommand, 0, len(items))
	for _, it := range items {
		obj, _ := it.(map[string]any)
		id, okID := fixturegen.Int(obj["id"])
		name, okName := obj["name"].(string)
		if !okID || !okName {
			t.Fatalf("commands.json holds an item without an integer id and a name; %s", reRecord)
		}
		out = append(out, recordedCommand{name: name, id: int(id)})
	}
	return out
}

// recordedDownloadIDs is the sorted set of non-empty downloadIds in the recorded
// queue.json, read without the engine.
func recordedDownloadIDs(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", dirRadarrAPIv3, "queue.json"))
	if err != nil {
		t.Fatalf("%v; %s", err, reRecord)
	}
	v, err := fixturegen.Decode(b)
	items, ok := v.([]any)
	if err != nil || !ok {
		t.Fatalf("queue.json is not a JSON list; %s", reRecord)
	}
	var out []string
	for _, it := range items {
		obj, _ := it.(map[string]any)
		if id, _ := obj["downloadId"].(string); id != "" {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func replayRadarr(t *testing.T) (*Client, *fixturegen.Replay) {
	t.Helper()
	routes := map[string]fixturegen.Route{
		"GET /api":                              {File: "api.json"},
		"GET /api/v3/system/status":             {File: "system-status.json"},
		"GET /api/v3/movie":                     {File: "movies.json"},
		"GET " + historySincePath(fixtureSince): {File: "history-since.json"},
		"GET /api/v3/tag":                       {File: "tags.json"},
		"GET /api/v3/rootfolder":                {File: "rootfolders.json"},
		"GET /api/v3/command":                   {File: "commands.json"},
		"GET /api/v3/queue/details":             {File: "queue.json"},
	}
	for _, f := range recordedMovieFiles {
		routes["GET /api/v3/movie/"+strconv.Itoa(fixtureMovieID(t, f))] = fixturegen.Route{File: f}
	}
	p, err := fixturegen.NewReplay(filepath.Join("..", "testdata", dirRadarrAPIv3), routes)
	if err != nil {
		t.Fatalf("%v; %s", err, reRecord)
	}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, testKey, WithBaseDelay(time.Millisecond), WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	t.Cleanup(func() {
		if m := p.Misses(); len(m) > 0 {
			t.Errorf("no recorded answer for %q; if the request is right, %s", m, reRecord)
		}
	})
	return c, p
}

// The two recorded movies: the first with a file and the first without one, by TMDB id.
func recordedMovies() []Movie {
	return []Movie{
		{
			File: &MovieFile{
				MediaInfo:    &arrapi.MediaInfo{VideoCodec: "x264", AudioLanguages: []string{"fin"}},
				Revision:     &arrapi.Revision{Version: 1},
				RelativePath: "movie-file-1.mkv",
				Path:         "/movies/folder-1/movie-file-1.mkv",
				SceneName:    "scene-1",
				ReleaseGroup: "group-1",
				ID:           1,
				Size:         1000000000,
			},
			OriginalLanguage: &arrapi.Language{Name: "Finnish", ID: 16},
			Title:            "Ariel",
			IDs:              ExternalIDs{IMDb: "tt0094675", TMDB: 2},
			AlternateTitles:  []string{"아리엘", "Mayonaka no Niji", "Ariel - Abgebrannt in Helsinki"},
			Tags:             []int{1},
			ID:               3,
			Year:             1988,
			HasFile:          true,
			Monitored:        true,
		},
		{
			OriginalLanguage: &arrapi.Language{Name: "English", ID: 1},
			Title:            "Enemies of Happiness",
			IDs:              ExternalIDs{IMDb: "tt0924122", TMDB: 121255},
			AlternateTitles:  []string{},
			Tags:             []int{1},
			ID:               4,
			Year:             2006,
			Monitored:        true,
		},
	}
}

func TestRadarrAPIv3Fixtures(t *testing.T) {
	c, p := replayRadarr(t)
	t.Run("Info", func(t *testing.T) {
		got, err := c.Info(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		assertFields(t, got, Info{Version: "6.4.4.10685", API: arrapi.APIv3})
		if want := []string{"GET /api", "GET /api/v3/system/status"}; !slices.Equal(p.Requests(), want) {
			t.Errorf("requests = %q, want %q", p.Requests(), want)
		}
	})
	t.Run("Movies", func(t *testing.T) {
		got, err := c.Movies(t.Context())
		if err != nil || len(got) != 2 {
			t.Fatalf("Movies = %+v, %v; want the two recorded movies", got, err)
		}
		for i, want := range recordedMovies() {
			assertFields(t, got[i], want)
		}
	})
	t.Run("MovieByID", func(t *testing.T) {
		for i, want := range recordedMovies() {
			id := fixtureMovieID(t, recordedMovieFiles[i])
			got, ok, err := c.MovieByID(t.Context(), id)
			if err != nil || !ok {
				t.Fatalf("MovieByID(%d) = %t, %v", id, ok, err)
			}
			assertFields(t, got, want)
		}
	})
	t.Run("HistorySince", func(t *testing.T) {
		got, err := c.HistorySince(t.Context(), fixtureSince)
		if err != nil {
			t.Fatal(err)
		}
		at := func(h int) time.Time { return fixtureSince.Add(time.Duration(h) * time.Hour) }
		want := []HistoryRecord{
			{
				Date: at(0), EventType: EventGrabbed, ID: 1, MovieID: 2, DownloadID: "download-2", SourceTitle: "release-7",
				GUID: "guid-1", Indexer: "indexer-2", InfoURL: "infourl-2", Source: arrapi.SourceRSS,
			},
			{Date: at(1), EventType: EventFileDeleted, ID: 2, MovieID: 2, SourceTitle: "release-2"},
			{Date: at(1), EventType: EventDownloadImported, ID: 3, MovieID: 2, DownloadID: "download-2", SourceTitle: "release-6"},
			{
				Date: at(2), EventType: EventGrabbed, ID: 4, MovieID: 5, DownloadID: "download-3", SourceTitle: "release-4",
				GUID: "guid-3", Indexer: "indexer-3", InfoURL: "infourl-3", Source: arrapi.SourceRSS,
			},
			{
				Date: at(3), EventType: EventGrabbed, ID: 5, MovieID: 6, DownloadID: "download-1", SourceTitle: "release-8",
				GUID: "guid-2", Indexer: "indexer-1", InfoURL: "infourl-1", Source: arrapi.SourceSearch,
			},
			{Date: at(4), EventType: EventFileDeleted, ID: 6, MovieID: 5, SourceTitle: "release-1"},
			{Date: at(4), EventType: EventDownloadImported, ID: 7, MovieID: 5, DownloadID: "download-3", SourceTitle: "release-3"},
			{
				Date: at(5), EventType: EventDownloadFailed, ID: 8, MovieID: 1, DownloadID: "download-4", SourceTitle: "release-5",
				Indexer: "indexer-2", Message: "message-1",
			},
		}
		if len(got) != len(want) {
			t.Fatalf("history = %+v, want %+v", got, want)
		}
		for i, w := range want {
			g := got[i]
			gd, wd := g, w
			gd.Date, wd.Date = time.Time{}, time.Time{}
			if !g.Date.Equal(w.Date) || gd != wd {
				t.Errorf("history[%d] = %+v, want %+v", i, g, w)
			}
		}
		for _, r := range got {
			switch {
			case r.EventType == EventGrabbed && (r.GUID == "" || r.Indexer == "" || r.DownloadID == "" || r.Source == ""):
				t.Errorf("grabbed record %d lacks a grab field: %+v", r.ID, r)
			case r.EventType == EventDownloadFailed && r.Message == "":
				t.Errorf("downloadFailed record %d has no Message", r.ID)
			}
		}
	})
	t.Run("Tags", func(t *testing.T) {
		got, err := c.Tags(t.Context())
		if want := []arrapi.Tag{{Label: "tag-1", ID: 1}, {Label: "tag-2", ID: 3}, {Label: "tag-3", ID: 2}}; err != nil || !slices.Equal(got, want) {
			t.Errorf("Tags = %+v, %v; want %+v", got, err, want)
		}
	})
	t.Run("RootFolders", func(t *testing.T) {
		got, err := c.RootFolders(t.Context())
		if want := []string{"/movies/root-2", "/movies/root-1"}; err != nil || !slices.Equal(got, want) {
			t.Errorf("RootFolders = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("Commands", func(t *testing.T) {
		got, err := c.Commands(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		want := recordedCommands(t)
		if len(got) != len(want) {
			t.Fatalf("Commands returned %d commands, want the %d recorded", len(got), len(want))
		}
		known := []arrapi.CommandStatus{
			arrapi.CommandQueued, arrapi.CommandStarted, arrapi.CommandCompleted, arrapi.CommandFailed,
			arrapi.CommandAborted, arrapi.CommandCancelled, arrapi.CommandOrphaned,
		}
		for i, w := range want {
			if g := got[i]; g.ID != w.id || g.Name != w.name || !slices.Contains(known, g.Status) {
				t.Errorf("command %d = %+v, want id %d, name %q and a known status", i, g, w.id, w.name)
			}
		}
	})
	t.Run("Queue", func(t *testing.T) {
		got, err := c.Queue(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		known := []arrapi.DownloadState{
			arrapi.DownloadDownloading, arrapi.DownloadImportBlocked, arrapi.DownloadImportPending, arrapi.DownloadImporting,
			arrapi.DownloadImported, arrapi.DownloadFailedPending, arrapi.DownloadFailed, arrapi.DownloadIgnored,
		}
		var ids []string
		for i, q := range got {
			if q.State != "" && !slices.Contains(known, q.State) || q.Size < 0 || q.SizeLeft < 0 {
				t.Errorf("queue item %d = %+v, want a known state and sizes of 0 or more", i, q)
			}
			if q.DownloadID != "" {
				ids = append(ids, q.DownloadID)
			}
		}
		slices.Sort(ids)
		if ids, want := slices.Compact(ids), recordedDownloadIDs(t); !slices.Equal(ids, want) {
			t.Errorf("queue download ids = %q, want the recorded %q", ids, want)
		}
	})
	t.Run("MediaCoverURL", func(t *testing.T) {
		got, err := c.MediaCoverURL(t.Context(), 4, "poster.jpg")
		if want := c.t.BaseURL() + "/api/v3/mediacover/4/poster.jpg"; err != nil || got != want {
			t.Errorf("MediaCoverURL = %q, %v; want %q", got, err, want)
		}
	})
}

// radarrFlagBits is Radarr's IndexerFlags enum (NzbDrone.Core/Parser/Model/IndexerFlags.cs),
// name to bit, as Radarr writes a release's indexerFlags.
var radarrFlagBits = map[string]IndexerFlags{
	"G_Freeleech": 1, "G_Halfleech": 2, "G_DoubleUpload": 4, "PTP_Golden": 8, "PTP_Approved": 16,
	"G_Internal": 32, "AHD_Internal": 64, "G_Scene": 128, "G_Freeleech75": 256, "G_Freeleech25": 512,
	"AHD_UserRelease": 1024, "Nuked": 2048,
}

func TestRadarrIndexerFlags(t *testing.T) {
	rows := map[string]struct {
		wire string
		want IndexerFlags
	}{
		"Scene and Freeleech25":   {`["G_Scene","G_Freeleech25"]`, 640},
		"no names":                {`[]`, 0},
		"null":                    {`null`, 0},
		"unknown names":           {`["Unknown","0"]`, 0},
		"an integer":              {`640`, 640},
		"an integer's other bits": {`4097`, 1},
		"other entries skipped":   {`[1,null,{},"Nuked","nuked"]`, FlagNuked},
		"every name":              {`["G_Freeleech","G_Halfleech","G_DoubleUpload","PTP_Golden","PTP_Approved","G_Internal","AHD_Internal","G_Scene","G_Freeleech75","G_Freeleech25","AHD_UserRelease","Nuked"]`, 4095},
	}
	for name, bit := range radarrFlagBits {
		rows[name] = struct {
			wire string
			want IndexerFlags
		}{`["` + name + `"]`, bit}
	}
	for name, r := range rows {
		t.Run(name, func(t *testing.T) {
			var got radarrIndexerFlags
			if err := json.Unmarshal([]byte(r.wire), &got); err != nil || IndexerFlags(got) != r.want {
				t.Errorf("decode %s = (%d, %v), want %d", r.wire, got, err, r.want)
			}
		})
	}
	for _, wire := range []string{`"G_Scene"`, `-1`, `1.5`, `4294967296`, `true`, `{}`} {
		t.Run("refuses "+wire, func(t *testing.T) {
			var got radarrIndexerFlags
			if err := json.Unmarshal([]byte(wire), &got); err == nil {
				t.Errorf("decode %s = %d, want an error", wire, got)
			}
		})
	}
}
