package radarr

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/detect"
	"github.com/cplieger/arrapi/v3/internal/fixturegen"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

const (
	dirRadarrAPIv3 = "radarr/api-v3"
	historySample  = 8
)

// recordTarget is the server the recorder reads; opts carry a test's transport.
type recordTarget struct {
	url  string
	key  arrapi.APIKey
	opts []Option
}

var radarrFixtureRules = func() fixturegen.Rules {
	id := func(role string) fixturegen.Rule { return fixturegen.Rule{Kind: fixturegen.ID, Role: role} }
	return fixturegen.Rules{
		reflect.TypeFor[radarrMovie](): {"id": id("movie"), "tags": id("tag")},
		reflect.TypeFor[radarrMovieFile](): {
			"id":           id("moviefile"),
			"size":         {Kind: fixturegen.Size},
			"relativePath": {Kind: fixturegen.RelPath, Role: "movie-file"},
			"path":         {Kind: fixturegen.Path, Role: "movie-file", Root: "/movies"},
			"sceneName":    {Kind: fixturegen.Pseudo, Role: "scene"},
			"releaseGroup": {Kind: fixturegen.Pseudo, Role: "group"},
		},
		reflect.TypeFor[radarrHistory]():    {"id": id("history"), "movieId": id("movie"), "date": {Kind: fixturegen.Date}},
		reflect.TypeFor[radarrTag]():        {"id": id("tag"), "label": {Kind: fixturegen.Pseudo, Role: "tag"}},
		reflect.TypeFor[radarrRootFolder](): {"path": {Kind: fixturegen.RootPath, Root: "/movies"}},
	}
}()

// recordRadarr records testdata/radarr/api-v3 under root from tgt, writing nothing
// unless every read and every check passed.
func recordRadarr(ctx context.Context, root string, tgt recordTarget, since time.Time) (recorded, error) {
	c, err := New(tgt.url, tgt.key, tgt.opts...)
	if err != nil {
		return recorded{}, err
	}
	defer c.Close()
	rec := fixturegen.NewRecording(radarrFixtureRules)
	rec.Secret(c.t.ContainsKey)
	if u, err := url.Parse(tgt.url); err == nil {
		rec.Private(u.Hostname())
	}
	d, err := c.s.Info(ctx)
	if err != nil {
		return recorded{}, fmt.Errorf("ARRAPI_RADARR_URL: Info: %w", err)
	}

	r := fixturegen.NewReader(rec, c.t, d, dirRadarrAPIv3)
	r.Read(ctx, "api.json", "/api", transport.MaxObjectBytes, reflect.TypeFor[detect.APIDoc]())
	status := r.Read(ctx, "system-status.json", radarrPrefix+"/system/status", transport.MaxObjectBytes, reflect.TypeFor[detect.StatusDoc]())
	rec.Private(statusPrivates(status)...)
	list := r.List(ctx, radarrPrefix+"/movie")
	picked := pickMovies(list)
	if r.Err == nil && len(picked) != 2 {
		r.Err = errors.New("the library has no movie with a file or none without one")
	}
	r.Add("movies.json", picked, reflect.TypeFor[[]radarrMovie]())
	for i, name := range []string{"movie-withfile.json", "movie-nofile.json"} {
		if r.Err != nil {
			break
		}
		id, _ := fixturegen.Int(picked[i].(map[string]any)["id"])
		r.Read(ctx, name, fmt.Sprintf("%s/movie/%d", radarrPrefix, id), transport.MaxObjectBytes, reflect.TypeFor[radarrMovie]())
	}
	r.Add("history-since.json", fixturegen.Sample(r.List(ctx, historySincePath(since)), "eventType", historySample), reflect.TypeFor[[]radarrHistory]())
	r.Read(ctx, "tags.json", radarrPrefix+"/tag", transport.MaxListBytes, reflect.TypeFor[[]radarrTag]())
	r.Read(ctx, "rootfolders.json", radarrPrefix+"/rootfolder", transport.MaxListBytes, reflect.TypeFor[[]radarrRootFolder]())
	if r.Err != nil {
		return recorded{}, fmt.Errorf("Radarr (API v3): %w", r.Err)
	}
	n, err := rec.Write(root)
	return recorded{files: n, counts: rec.Counts()}, err
}

// recorded is what a recording wrote: the file count and how many real values each
// synthesis map replaced, never the values.
type recorded struct {
	counts map[string]int
	files  int
}

// pickMovies returns the first movie with a file and the first without one, by TMDB
// id ascending, so no internal id of the deployment chooses the fixture.
func pickMovies(list []any) []any {
	sorted := slices.Clone(list)
	tmdb := func(v any) int64 {
		obj, _ := v.(map[string]any)
		n, _ := fixturegen.Int(obj["tmdbId"])
		return n
	}
	slices.SortStableFunc(sorted, func(a, b any) int { return cmp.Compare(tmdb(a), tmdb(b)) })
	var with, without any
	for _, m := range sorted {
		obj, ok := m.(map[string]any)
		if !ok {
			continue
		}
		switch has, _ := obj["hasFile"].(bool); {
		case has && with == nil:
			with = m
		case !has && without == nil:
			without = m
		default:
		}
	}
	if with == nil || without == nil {
		return nil
	}
	return []any{with, without}
}

// statusPrivates are the strings of a status answer that name the deployment: every
// one but the kept appName and version.
func statusPrivates(status any) []string {
	obj, _ := status.(map[string]any)
	keep := map[any]bool{obj["appName"]: true, obj["version"]: true}
	var out []string
	for _, v := range obj {
		if s, ok := v.(string); ok && !keep[v] {
			out = append(out, s)
		}
	}
	return out
}

// uriFake answers each request by method and request URI, query included; an
// unrouted request gets a header-less 404.
type uriFake struct {
	routes map[string]step
	log    []string
	mu     sync.Mutex
}

func (f *uriFake) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.RequestURI()
	f.mu.Lock()
	f.log = append(f.log, key)
	s, ok := f.routes[key]
	f.mu.Unlock()
	if !ok {
		s = ans(http.StatusNotFound, "", "")
	}
	rec := httptest.NewRecorder()
	if err := s(rec, req); err != nil {
		return nil, err
	}
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

func (f *uriFake) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

var recordSince = time.Date(2031, 7, 1, 0, 0, 0, 0, time.UTC)

// Values no real answer carries, planted so a test can prove none is written.
const (
	plantedDate = "2031-07-09T13:57:11Z"
	plantedSize = "7777777777"
	plantedID   = "987654"
)

func radarrLibrary() *uriFake {
	file := `{"id":987654,"movieId":987654,"size":7777777777,"relativePath":"Serenity (2005) Bluray-1080p.mkv",
		"path":"/data/movies/Serenity (2005)/Serenity (2005) Bluray-1080p.mkv","sceneName":"Serenity.2005.1080p-SHADOWGRP",
		"releaseGroup":"SHADOWGRP","quality":{"revision":{"version":1,"isRepack":false}},
		"mediaInfo":{"videoCodec":"x264","audioLanguages":"English / und","audioCodec":"DTS"}}`
	serenity := `{"id":987654,"title":"Serenity","tmdbId":16320,"imdbId":"tt0379786","year":2005,"hasFile":true,"tags":[3],
		"path":"/data/movies/Serenity (2005)","movieFile":` + file + `}`
	list := `[{"id":9,"title":"Later","tmdbId":90000,"hasFile":true,"movieFile":` + file + `},` + serenity + `,
		{"id":8,"title":"Wanted","tmdbId":20000,"hasFile":false},{"id":10,"title":"Earliest Missing","tmdbId":10,"hasFile":false}]`
	history := `[{"id":2,"movieId":9,"date":"2031-07-09T10:00:00Z","eventType":"grabbed","data":{"downloadUrl":"https://indexer.invalid/api?apikey=CREDENTIAL-abc"}},
		{"id":987654,"movieId":987654,"date":"` + plantedDate + `","eventType":"downloadFolderImported","data":{"droppedPath":"/downloads/x.mkv"}}]`
	status := `{"appName":"Radarr","version":"` + vRadarr + `","appData":"/config","startupPath":"/app/radarr/bin","instanceName":"Example Radarr"}`
	s := func(body string) step { return ans(http.StatusOK, vRadarr, body) }
	return &uriFake{routes: map[string]step{
		"GET /api":                             s(apiBody("v3")),
		"GET /api/v3/system/status":            s(status),
		"GET /api/v3/movie":                    s(list),
		"GET /api/v3/movie/987654":             s(serenity),
		"GET /api/v3/movie/10":                 s(`{"id":10,"title":"Earliest Missing","tmdbId":10,"hasFile":false}`),
		"GET " + historySincePath(recordSince): s(history),
		"GET /api/v3/tag":                      s(`[{"id":3,"label":"private-label"}]`),
		"GET /api/v3/rootfolder":               s(`[{"id":1,"path":"/data/movies/","freeSpace":1}]`),
	}}
}

func target(f http.RoundTripper, key arrapi.APIKey) recordTarget {
	return recordTarget{url: fakeBase, key: key, opts: []Option{WithHTTPClient(&http.Client{Transport: f}), WithLogger(slog.New(slog.DiscardHandler))}}
}

// radarrFixtureFiles is every file design section 11 names for testdata/radarr.
var radarrFixtureFiles = []string{
	"radarr/api-v3/api.json", "radarr/api-v3/history-since.json", "radarr/api-v3/movie-nofile.json",
	"radarr/api-v3/movie-withfile.json", "radarr/api-v3/movies.json", "radarr/api-v3/rootfolders.json",
	"radarr/api-v3/system-status.json", "radarr/api-v3/tags.json",
}

func readFixtures(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func recordFake(t *testing.T, f http.RoundTripper, key arrapi.APIKey) (map[string]string, error) {
	t.Helper()
	root := t.TempDir()
	got, err := recordRadarr(t.Context(), root, target(f, key), recordSince)
	files := readFixtures(t, root)
	if n := got.files; n != len(files) {
		t.Errorf("recordRadarr reported %d files, wrote %d", n, len(files))
	}
	return files, err
}

func decodeInto(t *testing.T, body string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), out); err != nil {
		t.Fatalf("decode %T: %v", out, err)
	}
}

func TestRecordRadarrWritesEveryFixture(t *testing.T) {
	files, err := recordFake(t, radarrLibrary(), testKey)
	if err != nil {
		t.Fatalf("recordRadarr: %v", err)
	}
	if got := slices.Sorted(maps.Keys(files)); !slices.Equal(got, radarrFixtureFiles) {
		t.Fatalf("files =\n  %q\nwant\n  %q", got, radarrFixtureFiles)
	}
	var picked []radarrMovie
	decodeInto(t, files[dirRadarrAPIv3+"/movies.json"], &picked)
	if len(picked) != 2 || picked[0].Title != "Serenity" || !picked[0].HasFile || picked[1].Title != "Earliest Missing" || picked[1].HasFile {
		t.Fatalf("movies.json = %+v, want the first movie with and the first without a file by TMDB id", picked)
	}
	var withFile radarrMovie
	decodeInto(t, files[dirRadarrAPIv3+"/movie-withfile.json"], &withFile)
	mf := withFile.MovieFile
	if mf == nil || mf.Path != "/movies/folder-1/movie-file-1.mkv" || mf.RelativePath != "movie-file-1.mkv" || mf.Size != 1000000000 ||
		mf.SceneName != "scene-1" || mf.ReleaseGroup != "group-1" || mf.MediaInfo == nil || mf.MediaInfo.AudioLanguages != "English / und" {
		t.Fatalf("movie file = %+v", mf)
	}
	if withFile.ID != picked[0].ID || withFile.Tags[0] != 1 || withFile.IMDbID != "tt0379786" {
		t.Fatalf("movie = %+v", withFile)
	}
	var hist []radarrHistory
	decodeInto(t, files[dirRadarrAPIv3+"/history-since.json"], &hist)
	if len(hist) != 2 || hist[1].MovieID != withFile.ID || hist[0].Date.Year() != 2001 || !hist[0].Date.Before(hist[1].Date) {
		t.Fatalf("history = %+v", hist)
	}
	for name, body := range files {
		for _, p := range []string{
			plantedDate, plantedSize, plantedID, "CREDENTIAL", "apikey", `"data"`, "/data", "/config", "/downloads",
			"Example Radarr", "SHADOWGRP", "Bluray-1080p.mkv", "private-label", "radarr.test", "DTS",
		} {
			if strings.Contains(body, p) {
				t.Errorf("%s holds %q", name, p)
			}
		}
	}
}

func TestRecordRadarrStopsOnASonarr(t *testing.T) {
	f := &uriFake{routes: map[string]step{
		"GET /api":                  ans(http.StatusOK, v5Sonarr, apiBody("v5", "v3")),
		"GET /api/v5/system/status": ans(http.StatusOK, v5Sonarr, statusBody("Sonarr", v5Sonarr)),
	}}
	files, err := recordFake(t, f, testKey)
	if e, ok := errors.AsType[*arrapi.UnsupportedAPIError](err); !ok || e.Reason != arrapi.ReasonWrongApp {
		t.Fatalf("err = %v, want ReasonWrongApp", err)
	}
	if got := f.requests(); !slices.Equal(got, []string{"GET /api", "GET /api/v5/system/status"}) || len(files) != 0 {
		t.Fatalf("requests = %q, files = %d", got, len(files))
	}
}

func TestRecordRadarrRefusesAnswersFromAnotherVersion(t *testing.T) {
	f := radarrLibrary()
	f.routes["GET /api/v3/tag"] = ans(http.StatusOK, "7.0.0.1", `[]`)
	files, err := recordFake(t, f, testKey)
	if _, ok := errors.AsType[*arrapi.ServerChangedError](err); !ok || len(files) != 0 {
		t.Fatalf("err = %v, files = %d; want *arrapi.ServerChangedError and nothing written", err, len(files))
	}
}

// The Radarr recording reads no error answer today; one added later is written with
// its message and title replaced, as Sonarr's are.
func TestRadarrRecordingReplacesErrorText(t *testing.T) {
	rec := fixturegen.NewRecording(radarrFixtureRules)
	doc, err := fixturegen.Decode([]byte(`{"message":"Alice Example keeps films on volume wintermute","title":"Could not read \\\\nas\\films"}`))
	if err != nil {
		t.Fatal(err)
	}
	rec.AddJSON(dirRadarrAPIv3+"/movie-404.json", doc, reflect.TypeFor[transport.ErrorDoc]())
	root := t.TempDir()
	if _, err := rec.Write(root); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := map[string]any{}
	decodeInto(t, readFixtures(t, root)[dirRadarrAPIv3+"/movie-404.json"], &got)
	if got["message"] != "error-1" || got["title"] != "error-2" {
		t.Fatalf("error doc = %v, want error-1 and error-2", got)
	}
}

func TestRecordRadarrRefusesKeyAndDeploymentData(t *testing.T) {
	const key arrapi.APIKey = "test.key_7a2e"
	for name, title := range map[string]string{
		"the key":           `"` + string(key) + `"`,
		"a URL":             `"http://radarr.invalid/x"`,
		"an absolute path":  `"/config/radarr.db"`,
		"the instance name": `"Example Radarr"`,
	} {
		t.Run(name, func(t *testing.T) {
			f := radarrLibrary()
			f.routes["GET /api/v3/movie/10"] = ans(http.StatusOK, vRadarr, `{"id":10,"title":`+title+`,"tmdbId":10,"hasFile":false}`)
			files, err := recordFake(t, f, key)
			if err == nil || len(files) != 0 {
				t.Fatalf("err = %v, files = %d; want a refusal and nothing written", err, len(files))
			}
			if strings.Contains(err.Error(), "7a2e") {
				t.Fatalf("the refusal renders the value: %v", err)
			}
		})
	}
}
