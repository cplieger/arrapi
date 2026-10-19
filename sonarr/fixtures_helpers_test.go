package sonarr

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/fixturegen"
)

// fixtureSince is the time the replay tests pass to HistorySince; the replay answers
// only the request it builds.
var fixtureSince = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// fixtureDir is the committed fixture directory dir, as the recorder names it.
func fixtureDir(dir string) string { return filepath.Join("..", "testdata", dir) }

// fixtureSeriesID returns the row id of the series with tvdb in the recorded series
// list file of dir, found by TVDB id as the recorder found it.
func fixtureSeriesID(t *testing.T, dir, file string, tvdb int64) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir(dir), file))
	if err != nil {
		t.Fatalf("%v; %s", err, reRecord)
	}
	list, err := fixturegen.Decode(b)
	items, ok := list.([]any)
	if err != nil || !ok {
		t.Fatalf("%s/%s is not a JSON list; %s", dir, file, reRecord)
	}
	s, ok := fixturegen.Find(items, "tvdbId", tvdb)
	id, okID := fixturegen.Int(s["id"])
	if !ok || !okID {
		t.Fatalf("%s/%s holds no series with tvdbId %d; %s", dir, file, tvdb, reRecord)
	}
	return int(id)
}

// sonarrRoutes are the requests sh's engine sends that dir's recorded files answer,
// with seriesFile answering the series list.
func sonarrRoutes(t *testing.T, sh sonarrShape, seriesFile string) map[string]fixturegen.Route {
	t.Helper()
	cg := fixtureSeriesID(t, sh.dir, "series-codegeass.json", tvdbCodeGeass)
	p := sh.prefix
	r := map[string]fixturegen.Route{
		"GET /api":                                             {File: "api.json"},
		"GET " + p + "/system/status":                          {File: "system-status.json"},
		"GET " + p + "/series":                                 {File: seriesFile},
		"GET " + p + "/series/" + strconv.Itoa(cg):             {File: "series-by-id-codegeass.json"},
		"GET " + p + "/series/" + strconv.Itoa(absentSeriesID): {Role: "series-404", Code: http.StatusNotFound},
		"GET " + seriesQuery(p, "episode", cg):                 {File: "episodes-codegeass.json"},
		"GET " + seriesQuery(p, "episode", cg) + sh.filesQuery: {File: "episodes-codegeass-files.json"},
		"GET " + seriesQuery(p, "episodefile", cg):             {File: "episodefiles-codegeass.json"},
		"GET " + sinceQuery(p, fixtureSince):                   {File: "history-since.json"},
		"GET " + p + "/tag":                                    {File: "tags.json"},
		"GET " + p + "/rootfolder":                             {File: "rootfolders.json"},
	}
	switch sh.api {
	case arrapi.APIv3:
		r["GET /api/v5/system/status"] = fixturegen.Route{Role: "v5-system-status-404", Code: http.StatusNotFound}
	case arrapi.APIv5:
		ff := fixtureSeriesID(t, sh.dir, "series-firefly.json", tvdbFirefly)
		r["GET "+seriesQuery(p, "episode", ff)] = fixturegen.Route{File: "episodes-firefly.json"}
		r["GET "+seriesQuery(p, "episodefile", ff)] = fixturegen.Route{File: "episodefiles-firefly.json"}
		r["GET "+seriesQuery(p, "episodefile", absentSeriesID)] = fixturegen.Route{Role: "episodefile-missing-parent", Code: http.StatusNotFound}
	}
	return r
}

// replayDir serves the recorded files under path by routes over httptest and returns a
// client of it and the replay; wrap, when set, sees each request first. A request no
// route answers fails the test at cleanup, naming the record command.
func replayDir(t *testing.T, path string, routes map[string]fixturegen.Route, wrap func(http.Handler) http.Handler) (*Client, *fixturegen.Replay) {
	t.Helper()
	p, err := fixturegen.NewReplay(path, routes)
	if err != nil {
		t.Fatalf("%v; %s", err, reRecord)
	}
	var h http.Handler = p
	if wrap != nil {
		h = wrap(p)
	}
	srv := httptest.NewServer(h)
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

// replay serves sh's fixture directory with its series list in seriesFile.
func replay(t *testing.T, sh sonarrShape, seriesFile string) (*Client, *fixturegen.Replay) {
	t.Helper()
	return replayDir(t, fixtureDir(sh.dir), sonarrRoutes(t, sh, seriesFile), nil)
}

// The values both Sonarr recordings carry: Code Geass's files and episodes, the tag
// list and the history sample. Each engine's tests assert them field by field, beside
// the values only that engine maps.

func codeGeassAlternateTitles() []string {
	return []string{
		"Code Geass: Lelouch of the Rebellion R2", "Code Geass: Lelouch of the Rebellion",
		"コードギアス 反逆のルルーシュ", "コードギアス 反逆のルルーシュ R2",
		"Code Geass", "Code Geass R2", "Code Geass S1", "Code Geass S2",
	}
}

// codeGeassFiles are three of the series' 51 recorded files: the first, the first of
// season 2 and the one special with a file.
func codeGeassFiles() map[int]EpisodeFile {
	file := func(id, season int, rel string, size int64, group, codec string, audio ...string) EpisodeFile {
		return EpisodeFile{
			MediaInfo:    &arrapi.MediaInfo{VideoCodec: codec, AudioLanguages: audio},
			Revision:     &arrapi.Revision{Version: 1},
			RelativePath: rel,
			Path:         "/tv/folder-1/" + rel,
			ReleaseGroup: group,
			ID:           id,
			Season:       season,
			Size:         size,
		}
	}
	return map[int]EpisodeFile{
		1:  file(1, 1, "Season 01/episode-file-1.mkv", 1023000000, "group-3", "x265", "jpn", "jpn", "eng"),
		26: file(26, 2, "Season 02/episode-file-26.mkv", 1042000000, "group-4", "x264", "eng", "jpn", "jpn", "jpn"),
		51: file(51, 0, "Season 00/episode-file-51.mkv", 1000000000, "group-1", "x264", "jpn"),
	}
}

// codeGeassEpisodes are five of the series' 102 recorded episodes with the given
// episode TVDB ids (0 for every one in the recorded Sonarr v5 (API v5) answer, a series
// not refreshed since its metadata carried the ids): S01E01, S02E01, a special
// with an absolute number, the one special with a file and the last special.
func codeGeassEpisodes(tvdb map[int]int) map[int]Episode {
	ep := func(id, season, number, absolute, fileID int, title, airDate string) Episode {
		return Episode{
			Title: title, AirDate: airDate, ID: id, SeriesID: 1, Season: season, Number: number,
			Absolute: absolute, TVDBID: tvdb[id], FileID: fileID, HasFile: fileID != 0,
		}
	}
	return map[int]Episode{
		1:   ep(1, 1, 1, 1, 1, "The Day a New Demon Was Born", "2006-10-05"),
		26:  ep(26, 2, 1, 27, 26, "The Day a Demon Awakens", "2008-04-06"),
		54:  ep(54, 0, 4, 52, 0, "Code Geass: Lelouch of the Re;surrection", "2019-02-09"),
		63:  ep(63, 0, 13, 0, 51, "Code Geass: Oz the Reflection Picture Drama", "2013-01-10"),
		102: ep(102, 0, 53, 0, 0, "Picture Drama: Re;f 103.00 - Fragment of the Deep Sea", "2019-12-05"),
	}
}

func recordedTags() []arrapi.Tag {
	return []arrapi.Tag{{Label: "tag-1", ID: 1}, {Label: "tag-2", ID: 4}, {Label: "tag-3", ID: 2}, {Label: "tag-4", ID: 3}}
}

// recordedHistory is the recorded history sample, oldest first; its last record
// differs between the two recordings.
func recordedHistory(last HistoryRecord) []HistoryRecord {
	at := func(h int) time.Time { return fixtureSince.Add(time.Duration(h) * time.Hour) }
	return []HistoryRecord{
		{Date: at(0), EventType: EventGrabbed, ID: 1, SeriesID: 2},
		{Date: at(1), EventType: EventDownloadImported, ID: 2, SeriesID: 2},
		{Date: at(2), EventType: EventGrabbed, ID: 3, SeriesID: 2},
		{Date: at(3), EventType: EventFileDeleted, ID: 4, SeriesID: 2},
		{Date: at(3), EventType: EventDownloadImported, ID: 5, SeriesID: 2},
		{Date: at(4), EventType: EventGrabbed, ID: 6, SeriesID: 3},
		{Date: at(4), EventType: EventGrabbed, ID: 7, SeriesID: 3},
		last,
	}
}

// checkCodeGeassEpisodes asserts the recorded episode list: 102 episodes of series 1,
// want's episodes field by field, a file id exactly where HasFile, and, when files is
// set, every episode's File equal to the EpisodeFiles answer's file of its FileID
// (nil without one); files nil means Episode.File must be nil throughout.
func checkCodeGeassEpisodes(t *testing.T, got []Episode, want map[int]Episode, files map[int]EpisodeFile) {
	t.Helper()
	if len(got) != 102 {
		t.Fatalf("len(episodes) = %d, want 102", len(got))
	}
	byID := map[int]Episode{}
	withFile := 0
	for _, e := range got {
		byID[e.ID] = e
		if e.SeriesID != 1 || e.HasFile != (e.FileID != 0) {
			t.Errorf("episode %d: SeriesID %d, HasFile %t, FileID %d", e.ID, e.SeriesID, e.HasFile, e.FileID)
		}
		if e.HasFile {
			withFile++
		}
		switch {
		case files == nil && e.File != nil:
			t.Errorf("episode %d: File = %+v, want nil", e.ID, *e.File)
		case files != nil && e.HasFile && (e.File == nil || !reflect.DeepEqual(*e.File, files[e.FileID])):
			t.Errorf("episode %d: File = %s, want the EpisodeFiles answer's file %d", e.ID, show(e.File), e.FileID)
		case files != nil && !e.HasFile && e.File != nil:
			t.Errorf("episode %d has no file but File = %+v", e.ID, *e.File)
		}
	}
	if withFile != 51 {
		t.Errorf("%d episodes have a file, want 51", withFile)
	}
	for id, w := range want {
		g, ok := byID[id]
		if !ok {
			t.Errorf("no episode %d", id)
			continue
		}
		g.File = nil
		assertFields(t, g, w)
	}
}

// codeGeassFileList asserts the recorded file list (51 files, want's files field by
// field, every one with a revision and media info, no "" or "und" language) and
// returns it by id.
func codeGeassFileList(t *testing.T, got []EpisodeFile) map[int]EpisodeFile {
	t.Helper()
	if len(got) != 51 {
		t.Fatalf("len(files) = %d, want 51", len(got))
	}
	byID := map[int]EpisodeFile{}
	for _, f := range got {
		byID[f.ID] = f
		if f.Revision == nil || f.MediaInfo == nil || slices.Contains(f.MediaInfo.AudioLanguages, "") || slices.Contains(f.MediaInfo.AudioLanguages, "und") {
			t.Errorf("file %d: Revision %s, MediaInfo %s", f.ID, show(f.Revision), show(f.MediaInfo))
		}
	}
	if len(byID) != 51 {
		t.Errorf("%d distinct file ids, want 51", len(byID))
	}
	for id, w := range codeGeassFiles() {
		assertFields(t, byID[id], w)
	}
	return byID
}

// recordedReads runs, as subtests on c, every read whose recorded answer both Sonarr
// recordings share in shape: Code Geass's episodes with and without files, its files,
// the history sample, tags, root folders and a media cover URL under sh's prefix.
func recordedReads(t *testing.T, c *Client, sh sonarrShape, tvdb map[int]int, lastHistory HistoryRecord) {
	t.Run("EpisodeFiles", func(t *testing.T) {
		got, err := c.EpisodeFiles(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		codeGeassFileList(t, got)
	})
	t.Run("Episodes", func(t *testing.T) {
		got, err := c.Episodes(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		checkCodeGeassEpisodes(t, got, codeGeassEpisodes(tvdb), nil)
	})
	t.Run("EpisodesWithFiles", func(t *testing.T) {
		files, err := c.EpisodeFiles(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.EpisodesWithFiles(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		checkCodeGeassEpisodes(t, got, codeGeassEpisodes(tvdb), codeGeassFileList(t, files))
	})
	t.Run("HistorySince", func(t *testing.T) {
		got, err := c.HistorySince(t.Context(), fixtureSince)
		if err != nil {
			t.Fatal(err)
		}
		want := recordedHistory(lastHistory)
		if len(got) != len(want) {
			t.Fatalf("history = %+v, want %+v", got, want)
		}
		for i, w := range want {
			if g := got[i]; !g.Date.Equal(w.Date) || g.EventType != w.EventType || g.ID != w.ID || g.SeriesID != w.SeriesID {
				t.Errorf("history[%d] = %+v, want %+v", i, g, w)
			}
		}
		grabbed, err := c.HistorySince(t.Context(), fixtureSince, EventGrabbed)
		if err != nil {
			t.Fatal(err)
		}
		var ids, wantIDs []int
		for _, r := range grabbed {
			ids = append(ids, r.ID)
		}
		for _, r := range want {
			if r.EventType == EventGrabbed {
				wantIDs = append(wantIDs, r.ID)
			}
		}
		if !slices.Equal(ids, wantIDs) {
			t.Errorf("grabbed records = %v, want %v", ids, wantIDs)
		}
	})
	t.Run("Tags", func(t *testing.T) {
		got, err := c.Tags(t.Context())
		if err != nil || !slices.Equal(got, recordedTags()) {
			t.Errorf("Tags = %+v, %v; want %+v", got, err, recordedTags())
		}
	})
	t.Run("RootFolders", func(t *testing.T) {
		got, err := c.RootFolders(t.Context())
		if want := []string{"/tv/root-2", "/tv/root-1"}; err != nil || !slices.Equal(got, want) {
			t.Errorf("RootFolders = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("MediaCoverURL", func(t *testing.T) {
		got, err := c.MediaCoverURL(t.Context(), 1, "poster.jpg")
		if want := c.t.BaseURL() + sh.prefix + "/mediacover/1/poster.jpg"; err != nil || got != want {
			t.Errorf("MediaCoverURL = %q, %v; want %q", got, err, want)
		}
	})
}

// hideAPI answers GET /api with a header-less 404, as a proxy that does not forward it.
func hideAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}
