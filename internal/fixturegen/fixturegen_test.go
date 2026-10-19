package fixturegen_test

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/detect"
	"github.com/cplieger/arrapi/v3/internal/fixturegen"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

type language struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

type stats struct {
	EpisodeFileCount int `json:"episodeFileCount"`
}

type series struct {
	Language   *language `json:"language"`
	Title      string    `json:"title"`
	Overview   string    `json:"overview"`
	Seasons    []season  `json:"seasons"`
	Tags       []int     `json:"tags"`
	Statistics stats     `json:"statistics"`
	ID         int       `json:"id"`
	TVDBID     int       `json:"tvdbId"`
}

type season struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

type episode struct {
	File          *file  `json:"episodeFile"`
	ID            int    `json:"id"`
	SeriesID      int    `json:"seriesId"`
	EpisodeFileID int    `json:"episodeFileId"`
	AirDate       string `json:"airDate"`
}

type file struct {
	RelativePath string `json:"relativePath"`
	Path         string `json:"path"`
	SceneName    string `json:"sceneName"`
	ReleaseGroup string `json:"releaseGroup"`
	ID           int    `json:"id"`
	SeasonNumber int    `json:"seasonNumber"`
	Size         int64  `json:"size"`
}

type history struct {
	Date      time.Time       `json:"date"`
	EventType json.RawMessage `json:"eventType"`
	ID        int             `json:"id"`
	SeriesID  int             `json:"seriesId"`
}

type tag struct {
	Label string `json:"label"`
	ID    int    `json:"id"`
}

type root struct {
	Path string `json:"path"`
}

type apiDoc struct {
	Current    json.RawMessage   `json:"current"`
	Deprecated []json.RawMessage `json:"deprecated"`
}

var rules = fixturegen.Rules{
	reflect.TypeFor[series]():  {"id": {Kind: fixturegen.ID, Role: "series"}, "tags": {Kind: fixturegen.ID, Role: "tag"}},
	reflect.TypeFor[stats]():   {"episodeFileCount": {Kind: fixturegen.FileCount, Role: "series"}},
	reflect.TypeFor[episode](): {"id": {Kind: fixturegen.ID, Role: "episode"}, "seriesId": {Kind: fixturegen.ID, Role: "series"}, "episodeFileId": {Kind: fixturegen.ID, Role: "episodefile"}},
	reflect.TypeFor[file](): {
		"id":           {Kind: fixturegen.ID, Role: "episodefile"},
		"size":         {Kind: fixturegen.Size},
		"relativePath": {Kind: fixturegen.RelPath, Role: "episode-file"},
		"path":         {Kind: fixturegen.Path, Role: "episode-file", Root: "/tv"},
		"sceneName":    {Kind: fixturegen.Pseudo, Role: "scene"},
		"releaseGroup": {Kind: fixturegen.Pseudo, Role: "group"},
	},
	reflect.TypeFor[history](): {"id": {Kind: fixturegen.ID, Role: "history"}, "seriesId": {Kind: fixturegen.ID, Role: "series"}, "date": {Kind: fixturegen.Date}},
	reflect.TypeFor[tag]():     {"id": {Kind: fixturegen.ID, Role: "tag"}, "label": {Kind: fixturegen.Pseudo, Role: "tag"}},
	reflect.TypeFor[root]():    {"path": {Kind: fixturegen.RootPath, Root: "/tv"}},
}

func decode(t *testing.T, s string) any {
	t.Helper()
	v, err := fixturegen.Decode([]byte(s))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return v
}

func render(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestProjectKeepsOnlyDeclaredMembers(t *testing.T) {
	in := decode(t, `{
		"title": "T", "overview": "o", "appData": "/config", "tvdbId": 5,
		"language": {"name": "English", "id": 1, "extra": true},
		"seasons": [{"seasonNumber": 1, "statistics": {"x": 1}}, null],
		"tags": [1, 2],
		"statistics": {"episodeFileCount": 2, "sizeOnDisk": 9},
		"images": [{"remoteUrl": "https://example.invalid/x.jpg"}]
	}`)
	got := render(t, project(t, in, reflect.TypeFor[series]()))
	want := `{"arrapiUnknownField":1,"language":{"arrapiUnknownField":1,"id":1,"name":"English"},` +
		`"overview":"o","seasons":[{"arrapiUnknownField":1,"seasonNumber":1},null],` +
		`"statistics":{"arrapiUnknownField":1,"episodeFileCount":2},"tags":[1,2],"title":"T","tvdbId":5}`
	if got != want {
		t.Fatalf("projected\n got %s\nwant %s", got, want)
	}
}

func project(t *testing.T, v any, typ reflect.Type) any {
	t.Helper()
	p, err := fixturegen.Project(v, typ)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	return p
}

func TestProjectNamesTheMemberItRefuses(t *testing.T) {
	for in, want := range map[string]string{
		`{"seasons": [{"seasonNumber": 1}, {"seasonNumber": "/config/x"}]}`: "fixturegen: member seasons[1].seasonNumber: a JSON string that int does not decode",
		`"/config/x"`: "fixturegen: member (the answer): a JSON string that fixturegen_test.series does not decode",
	} {
		_, err := fixturegen.Project(decode(t, in), reflect.TypeFor[series]())
		if err == nil || err.Error() != want {
			t.Errorf("Project(%s) error = %v, want %q", in, err, want)
		}
	}
}

func TestProjectKeepsTheScalarsOfMembersThatDecodeThemselves(t *testing.T) {
	h := decode(t, `[{"date": "2026-01-02T03:04:05Z", "eventType": "grabbed", "data": {"downloadUrl": "x"}}]`)
	if got, want := render(t, project(t, h, reflect.TypeFor[[]history]())), `[{"arrapiUnknownField":1,"date":"2026-01-02T03:04:05Z","eventType":"grabbed"}]`; got != want {
		t.Fatalf("history\n got %s\nwant %s", got, want)
	}
	a := decode(t, `{"current": "v5", "deprecated": ["v3"], "build": "x"}`)
	if got, want := render(t, project(t, a, reflect.TypeFor[apiDoc]())), `{"arrapiUnknownField":1,"current":"v5","deprecated":["v3"]}`; got != want {
		t.Fatalf("api\n got %s\nwant %s", got, want)
	}
	nested := decode(t, `{"current": "v5", "deprecated": ["v3", {"x": 1}]}`)
	if _, err := fixturegen.Project(nested, reflect.TypeFor[apiDoc]()); err == nil || !strings.Contains(err.Error(), "member deprecated[1]: a JSON object") {
		t.Fatalf("Project kept an object under a member that decodes itself: %v", err)
	}
}

// planted values no real answer carries: a timestamp, a size and an internal id.
const (
	plantedDate = "2031-07-09T13:57:11Z"
	plantedSize = "7777777777"
	plantedID   = "987654"
)

func plantedRecording(t *testing.T) *fixturegen.Recording {
	t.Helper()
	r := fixturegen.NewRecording(rules)
	for _, dir := range []string{"a", "b"} {
		r.AddJSON(dir+"/series.json", decode(t, `[{"id": 987654, "title": "Code Geass", "tvdbId": 79525, "tags": [987654, 3],
			"statistics": {"episodeFileCount": 51}},
			{"id": 40, "title": "Firefly", "tags": [], "statistics": {"episodeFileCount": 14}},
			{"id": 41, "title": "Empty", "statistics": {"episodeFileCount": 0}}]`), reflect.TypeFor[[]series]())
		r.AddJSON(dir+"/episodes.json", decode(t, `[
			{"id": 987654, "seriesId": 987654, "episodeFileId": 987654, "airDate": "2006-10-06"},
			{"id": 12, "seriesId": 987654, "episodeFileId": 987654, "airDate": "2006-10-13"},
			{"id": 13, "seriesId": 987654, "episodeFileId": 0},
			{"id": 14, "seriesId": 987654, "episodeFileId": 500, "episodeFile": {"id": 500, "size": 1234567, "seasonNumber": 1,
				"relativePath": "Season 01/Code Geass - S01E03.mkv", "path": "/media/tv/Code Geass/Season 01/Code Geass - S01E03.mkv"}}]`),
			reflect.TypeFor[[]episode]())
		r.AddJSON(dir+"/files.json", decode(t, `[
			{"id": 987654, "size": 7777777777, "seasonNumber": 1, "relativePath": "Season 01/Code Geass - S01E01-E02.mkv",
			 "path": "/media/tv/Code Geass/Season 01/Code Geass - S01E01-E02.mkv", "sceneName": "Code.Geass.S01E01.1080p-SHADOWGRP", "releaseGroup": "SHADOWGRP"},
			{"id": 500, "size": 1234567, "seasonNumber": 1, "relativePath": "Season 01/Code Geass - S01E03.mkv",
			 "path": "/media/tv/Code Geass/Season 01/Code Geass - S01E03.mkv", "sceneName": "", "releaseGroup": "OTHERGRP"}]`),
			reflect.TypeFor[[]file]())
		r.AddJSON(dir+"/history.json", decode(t, `[
			{"id": 3, "seriesId": 40, "date": "2026-10-01T10:00:00Z", "eventType": "grabbed"},
			{"id": 987654, "seriesId": 987654, "date": "2031-07-09T13:57:11Z", "eventType": "downloadFolderImported"}]`),
			reflect.TypeFor[[]history]())
		r.AddJSON(dir+"/tags.json", decode(t, `[{"id": 3, "label": "private-label"}, {"id": 987654, "label": "kids"}]`), reflect.TypeFor[[]tag]())
		r.AddJSON(dir+"/rootfolders.json", decode(t, `[{"path": "/media/tv/"}]`), reflect.TypeFor[[]root]())
		r.Carries(dir, 987654, 2)
	}
	return r
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
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

func written(t *testing.T, r *fixturegen.Recording) map[string]string {
	t.Helper()
	root := t.TempDir()
	n, err := r.Write(root)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	files := readTree(t, root)
	if n != len(files) {
		t.Fatalf("Write reported %d files, wrote %d", n, len(files))
	}
	return files
}

func list(t *testing.T, s string) []map[string]any {
	t.Helper()
	var v []map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestWriteSynthesizesDeploymentNumbersAndTimes(t *testing.T) {
	r := plantedRecording(t)
	files := written(t, r)
	wantCounts := map[string]int{
		"id:series": 3, "id:tag": 2, "id:episode": 4, "id:episodefile": 2, "id:history": 2,
		"size": 2, "date": 2, "pseudo:scene": 1, "pseudo:group": 2, "pseudo:tag": 2, "pseudo:folder": 1, "pseudo:root": 1,
	}
	if got := r.Counts(); !maps.Equal(got, wantCounts) {
		t.Errorf("Counts = %v, want %v", got, wantCounts)
	}
	for name, body := range files {
		for _, p := range []string{plantedDate, plantedSize, plantedID, "1234567", "2026-10-01"} {
			if strings.Contains(body, p) {
				t.Errorf("%s still holds %s", name, p)
			}
		}
	}
	if files["a/series.json"] != files["b/series.json"] || files["a/files.json"] != files["b/files.json"] {
		t.Fatal("equal answers in two directories are not equal after synthesis")
	}
	ser := list(t, files["a/series.json"])
	eps := list(t, files["a/episodes.json"])
	fs := list(t, files["a/files.json"])
	hist := list(t, files["a/history.json"])
	tags := list(t, files["a/tags.json"])

	// Ascending real ids map to 1, 2, 3, ...: series 40 < 41 < 987654.
	if got := []any{ser[0]["id"], ser[1]["id"], ser[2]["id"]}; render(t, got) != `[3,1,2]` {
		t.Fatalf("series ids = %s, want [3,1,2]", render(t, got))
	}
	if render(t, ser[0]["tags"]) != `[2,1]` || render(t, tags) != `[{"arrapiUnknownField":1,"id":1,"label":"tag-2"},{"arrapiUnknownField":1,"id":2,"label":"tag-1"}]` {
		t.Fatalf("tag foreign keys or labels: series tags %s, tags %s", render(t, ser[0]["tags"]), render(t, tags))
	}
	// The multi-episode file's shared episodeFileId stays shared and joins its file.
	if eps[0]["episodeFileId"] != eps[1]["episodeFileId"] || render(t, eps[0]["episodeFileId"]) != render(t, fs[0]["id"]) {
		t.Fatalf("shared episodeFileId lost: %v %v, file %v", eps[0]["episodeFileId"], eps[1]["episodeFileId"], fs[0]["id"])
	}
	if render(t, eps[2]["episodeFileId"]) != "0" || render(t, eps[0]["seriesId"]) != render(t, ser[0]["id"]) {
		t.Fatal("a zero foreign key or a series foreign key changed meaning")
	}
	if render(t, eps[3]["episodeFile"].(map[string]any)["size"]) != render(t, fs[1]["size"]) {
		t.Fatal("an embedded file's size differs from its file list entry")
	}
	if render(t, fs[0]["size"]) != "1001000000" || render(t, fs[1]["size"]) != "1000000000" {
		t.Fatalf("sizes = %v %v, want ascending synthetic sizes", fs[0]["size"], fs[1]["size"])
	}
	if hist[0]["date"] != "2001-01-01T00:00:00Z" || hist[1]["date"] != "2001-01-01T01:00:00Z" {
		t.Fatalf("history dates = %v %v", hist[0]["date"], hist[1]["date"])
	}
	if render(t, hist[1]["seriesId"]) != render(t, ser[0]["id"]) || render(t, hist[1]["id"]) != "2" {
		t.Fatal("history foreign keys or ids not mapped")
	}
	counts := []string{
		render(t, ser[0]["statistics"].(map[string]any)["episodeFileCount"]),
		render(t, ser[1]["statistics"].(map[string]any)["episodeFileCount"]),
		render(t, ser[2]["statistics"].(map[string]any)["episodeFileCount"]),
	}
	if !slices.Equal(counts, []string{"2", "1", "0"}) {
		t.Fatalf("episodeFileCount = %v, want the carried 2, 1 without a file list, 0 kept", counts)
	}
	if eps[0]["airDate"] != "2006-10-06" {
		t.Fatal("airDate is public metadata and must be kept")
	}
}

func TestWritePseudonymizesDeploymentStrings(t *testing.T) {
	files := written(t, plantedRecording(t))
	for name, body := range files {
		for _, p := range []string{"/media", "SHADOWGRP", "OTHERGRP", "private-label", "kids", "Code Geass - S01E"} {
			if strings.Contains(body, p) {
				t.Errorf("%s still holds %q", name, p)
			}
		}
	}
	fs := list(t, files["a/files.json"])
	if fs[0]["relativePath"] != "Season 01/episode-file-2.mkv" || fs[0]["path"] != "/tv/folder-1/Season 01/episode-file-2.mkv" {
		t.Fatalf("paths = %v, %v", fs[0]["relativePath"], fs[0]["path"])
	}
	if fs[0]["sceneName"] != "scene-1" || fs[1]["sceneName"] != "" || fs[0]["releaseGroup"] != "group-2" {
		t.Fatalf("scene and group = %v %v %v", fs[0]["sceneName"], fs[1]["sceneName"], fs[0]["releaseGroup"])
	}
	roots := list(t, files["a/rootfolders.json"])
	if roots[0]["path"] != "/tv/root-1" {
		t.Fatalf("root folder = %v", roots[0]["path"])
	}
	ser := list(t, files["a/series.json"])
	if ser[0]["title"] != "Code Geass" {
		t.Fatal("an allowlisted title was replaced")
	}
}

func TestWritePseudonymizesStringsOffTheAllowlist(t *testing.T) {
	r := fixturegen.NewRecording(rules)
	r.AddJSON("a/series.json", decode(t, `{"title": "Kept", "overview": "my server notes"}`), reflect.TypeFor[series]())
	files := written(t, r)
	if got := files["a/series.json"]; !strings.Contains(got, `"overview": "text-1"`) || !strings.Contains(got, `"Kept"`) {
		t.Fatalf("series = %s", got)
	}
}

// An error answer's message and title are server-provided free text, so whatever they
// hold is replaced, whatever rules the recorder passes.
func TestWriteReplacesErrorText(t *testing.T) {
	for name, text := range map[string]string{
		"arbitrary text":        "Alice Example stores shows on volume wintermute",
		"a UNC path":            `Could not read \\nas\share\private`,
		"a symbol-led path":     "Could not read /🔥Media/private.mkv",
		"a public-looking word": "Not Found",
	} {
		t.Run(name, func(t *testing.T) {
			r := fixturegen.NewRecording(rules)
			doc := map[string]any{"message": text, "title": text + " (title)"}
			r.AddJSON("a/error.json", doc, reflect.TypeFor[transport.ErrorDoc]())
			r.AddJSON("a/series.json", decode(t, `{"title":"Code Geass"}`), reflect.TypeFor[series]())
			files := written(t, r)
			for f, body := range files {
				if strings.Contains(body, text) {
					t.Errorf("%s holds the error text", f)
				}
			}
			var got transport.ErrorDoc
			if err := json.Unmarshal([]byte(files["a/error.json"]), &got); err != nil {
				t.Fatal(err)
			}
			if string(got.Message) != `"error-1"` || string(got.Title) != `"error-2"` {
				t.Errorf("error doc = %s, %s; want error-1, error-2", got.Message, got.Title)
			}
			if !strings.Contains(files["a/series.json"], `"Code Geass"`) {
				t.Error("a series title was replaced with the error text")
			}
		})
	}
}

func TestWriteErrorRulesOverrideTheRecorders(t *testing.T) {
	own := maps.Clone(rules)
	own[reflect.TypeFor[transport.ErrorDoc]()] = map[string]fixturegen.Rule{"message": {Kind: fixturegen.Pseudo, Role: "kept"}}
	r := fixturegen.NewRecording(own)
	r.AddJSON("a/error.json", decode(t, `{"message":"Not Found"}`), reflect.TypeFor[transport.ErrorDoc]())
	if got := written(t, r)["a/error.json"]; !strings.Contains(got, `"message": "error-1"`) {
		t.Fatalf("error doc = %s", got)
	}
}

// A member named message on a DTO other than an error answer is not public metadata.
func TestWritePseudonymizesAMessageMember(t *testing.T) {
	type note struct {
		Message string `json:"message"`
	}
	r := fixturegen.NewRecording(rules)
	r.AddJSON("a/note.json", decode(t, `{"message":"my server notes"}`), reflect.TypeFor[note]())
	if got := written(t, r)["a/note.json"]; !strings.Contains(got, `"message": "text-1"`) {
		t.Fatalf("note = %s", got)
	}
}

func TestWriteHeadersKeepsOnlyTheVersion(t *testing.T) {
	r := fixturegen.NewRecording(rules)
	r.AddHeaders("a/series-404.headers", "4.0.20.3014")
	r.AddHeaders("a/missing.headers", "")
	files := written(t, r)
	if files["a/series-404.headers"] != "X-Application-Version: 4.0.20.3014\n" || files["a/missing.headers"] != "" {
		t.Fatalf("headers = %q, %q", files["a/series-404.headers"], files["a/missing.headers"])
	}
}

// The recording writes nothing when any scan finds a secret or deployment data, and
// its error names the file and the rule, never the value.
func TestWriteRefusesSecretsAndDeploymentData(t *testing.T) {
	const key = "s3cr3t-key-value"
	cases := map[string]string{
		"key":           key,
		"URL":           "see https://sonarr.example.invalid/x",
		"40-hex hash":   "0123456789abcdef0123456789abcdef01234567",
		"absolute path": "/config/sonarr.db",
		"windows path":  `C:\media\x.mkv`,
		"private":       "my-instance-name",
		"short private": "kids",
		"real size":     "size 1234567 bytes",
		"real date":     "at 2026-10-01T10:00:00Z",
	}
	for name, title := range cases {
		t.Run(name, func(t *testing.T) {
			r := plantedRecording(t)
			r.Secret(func(s string) bool { return strings.Contains(s, key) })
			r.Private("my-instance-name")
			r.AddJSON("a/title.json", map[string]any{"title": title}, reflect.TypeFor[series]())
			root := t.TempDir()
			n, err := r.Write(root)
			if err == nil {
				t.Fatalf("Write accepted a title holding %s", name)
			}
			if n != 0 || len(readTree(t, root)) != 0 {
				t.Fatal("a refused recording wrote files")
			}
			if strings.Contains(err.Error(), title) || !strings.Contains(err.Error(), "a/title.json") {
				t.Fatalf("error must name the file and not the value: %v", err)
			}
		})
	}
}

// A private string under 8 bytes could occur in public text by chance, so it is
// refused only as a whole string.
func TestWriteAllowsAShortPrivateStringInsidePublicText(t *testing.T) {
	r := plantedRecording(t)
	r.AddJSON("a/title.json", map[string]any{"title": "Shows for kids and grown-ups"}, reflect.TypeFor[series]())
	if _, err := r.Write(t.TempDir()); err != nil {
		t.Fatalf("Write refused public text holding a short private string: %v", err)
	}
}

func TestCheck(t *testing.T) {
	clean := map[string][]byte{
		"v4/system-status.json": []byte(`{"appName":"Sonarr","version":"4.0.20.3014","arrapiUnknownField":1}`),
		"v4/history-since.json": []byte(`[{"date":"2001-01-01T02:00:00Z","id":1,"eventType":"grabbed"}]`),
		"v4/files.json":         []byte(`[{"size":0,"path":"/tv/folder-1/Season 01/x.mkv"},{"size":1003000000,"path":"/movies/folder-1/x.mkv"}]`),
		"v4/series.json":        []byte(`[{"title":"Fate/Zero","relativePath":"Season 01/x.mkv","audioLanguages":"English / Japanese","t":"a /"}]`),
		"v4/series-404.headers": []byte("X-Application-Version: 4.0.20.3014\n"),
		"v5/missing.headers":    []byte(""),
	}
	if err := fixturegen.Check(clean); err != nil {
		t.Fatalf("Check refused clean fixtures: %v", err)
	}
	bad := map[string]map[string][]byte{
		"planted URL":         {"v4/series.json": []byte(`[{"title":"http://host.invalid"}]`)},
		"real-looking date":   {"v4/history-since.json": []byte(`[{"date":"2026-10-16T12:00:00Z"}]`)},
		"extra status field":  {"v4/system-status.json": []byte(`{"appName":"Sonarr","version":"4.0","instanceName":"x"}`)},
		"history data":        {"v4/history-since.json": []byte(`[{"date":"2001-01-01T00:00:00Z","data":{}}]`)},
		"non-synthetic size":  {"v4/files.json": []byte(`[{"size":1234567}]`)},
		"absolute path":       {"v4/files.json": []byte(`[{"path":"/data/x.mkv"}]`)},
		"40-hex":              {"v4/files.json": []byte(`[{"sceneName":"x-0123456789abcdef0123456789abcdef01234567"}]`)},
		"extra header line":   {"v4/series-404.headers": []byte("X-Application-Version: 4.0\nServer: Kestrel\n")},
		"unexpected file":     {"v4/notes.txt": []byte("x")},
		"not JSON":            {"v4/series.json": []byte(`{`)},
		"real date as string": {"v4/series.json": []byte(`[{"date":"2026-10-16"}]`)},
	}
	for name, files := range bad {
		t.Run(name, func(t *testing.T) {
			if err := fixturegen.Check(files); err == nil {
				t.Fatalf("Check accepted %s", name)
			}
		})
	}
}

func TestCheckTreeOnPlantedCopy(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sonarr", "v4-api-v3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	status := filepath.Join(dir, "system-status.json")
	if err := os.WriteFile(status, []byte(`{"appName":"Sonarr","version":"4.0.20.3014","arrapiUnknownField":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fixturegen.CheckTree(root); err != nil {
		t.Fatalf("CheckTree: %v", err)
	}
	if err := os.WriteFile(status, []byte(`{"appName":"Sonarr","version":"4.0","startupPath":"/app"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fixturegen.CheckTree(root); err == nil {
		t.Fatal("CheckTree accepted a planted status field")
	}
}

func TestSampleCoversEveryKind(t *testing.T) {
	recs := decode(t, `[{"id":1,"eventType":"grabbed"},{"id":2,"eventType":"grabbed"},{"id":3,"eventType":"deleted"},
		{"id":4,"eventType":"grabbed"},{"id":5,"eventType":"imported"},{"id":6,"eventType":"grabbed"}]`).([]any)
	got := render(t, fixturegen.Sample(recs, "eventType", 4))
	if want := `[{"eventType":"grabbed","id":1},{"eventType":"grabbed","id":2},{"eventType":"deleted","id":3},{"eventType":"imported","id":5}]`; got != want {
		t.Fatalf("Sample\n got %s\nwant %s", got, want)
	}
	if got := fixturegen.Sample(recs, "eventType", 10); len(got) != len(recs) {
		t.Fatalf("Sample under the bound kept %d of %d", len(got), len(recs))
	}
}

func TestFind(t *testing.T) {
	l := decode(t, `[{"id":7,"tvdbId":1},{"id":8,"tvdbId":79525}]`).([]any)
	obj, ok := fixturegen.Find(l, "tvdbId", 79525)
	if !ok {
		t.Fatal("Find missed the series")
	}
	if id, _ := fixturegen.Int(obj["id"]); id != 8 {
		t.Fatalf("id = %d", id)
	}
	if _, ok := fixturegen.Find(l, "tvdbId", 2); ok {
		t.Fatal("Find found an absent id")
	}
}

// server answers each path with its header and body; a path absent from the map 404s
// with no header.
type server map[string]struct {
	version string
	body    string
	code    int
}

func (s server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := s[r.URL.RequestURI()]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if a.version != "" {
		w.Header().Set("X-Application-Version", a.version)
	}
	w.WriteHeader(a.code)
	_, _ = io.WriteString(w, a.body)
}

func sonarr4(extra server) server {
	s := server{
		"/api":                  {"4.0.20.3014", `{"current":"v3"}`, 200},
		"/api/v3/system/status": {"4.0.20.3014", `{"appName":"Sonarr","version":"4.0.20.3014"}`, 200},
	}
	maps.Copy(s, extra)
	return s
}

func detection(t *testing.T, h http.Handler) (*transport.Client, *transport.Detection) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	tc, err := transport.New(srv.URL, arrapi.APIKey("test-key-5d1c"), transport.Config{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := detect.NewSession(tc, detect.AppSonarr, func(*transport.Detection) struct{} { return struct{}{} }).Info(t.Context())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	return tc, d
}

func TestGetReadsOnTheDetection(t *testing.T) {
	tc, d := detection(t, sonarr4(server{
		"/api/v3/tag":    {"4.0.20.3014", `[{"id":1,"label":"x"}]`, 200},
		"/api/v3/series": {"5.0.0.2635", `[]`, 200},
	}))
	v, err := fixturegen.Get(t.Context(), tc, d, "/api/v3/tag", transport.MaxListBytes)
	if err != nil || render(t, v) != `[{"id":1,"label":"x"}]` {
		t.Fatalf("Get = %s, %v", render(t, v), err)
	}
	if _, err := fixturegen.Get(t.Context(), tc, d, "/api/v3/series", transport.MaxListBytes); err == nil {
		t.Fatal("Get accepted an answer from another server version")
	} else if _, ok := errors.AsType[*arrapi.ServerChangedError](err); !ok {
		t.Fatalf("Get error = %v, want *arrapi.ServerChangedError", err)
	}
}

func TestGetErrorChecksTheHeaderItExpects(t *testing.T) {
	ctx := t.Context()
	h := sonarr4(server{
		"/api/v3/series/9":               {"4.0.20.3014", `{"title":"Not Found","traceId":"00-x"}`, 404},
		"/api/v3/episodefile?seriesId=9": {"", `{"message":"Series with ID 9 does not exist"}`, 404},
		"/api/v3/series/8":               {"5.0.0.2635", ``, 404},
		"/api/v3/tag":                    {"4.0.20.3014", `[]`, 200},
	})

	tc, d := detection(t, h)
	a, err := fixturegen.GetError(ctx, tc, d, "/api/v3/series/9", true)
	if err != nil || a.Code != 404 || a.AppVersion != "4.0.20.3014" || render(t, a.Body) != `{"title":"Not Found","traceId":"00-x"}` {
		t.Fatalf("GetError = %+v, %v", a, err)
	}
	if _, err := fixturegen.GetError(ctx, tc, d, "/api/v3/series/9", false); err == nil {
		t.Fatal("GetError accepted a header its step did not expect")
	}
	if _, err := fixturegen.GetError(ctx, tc, d, "/api/v3/tag", true); err == nil {
		t.Fatal("GetError accepted a success")
	}
	if _, err := fixturegen.GetError(ctx, tc, d, "/api/v3/series/8", true); err == nil {
		t.Fatal("GetError accepted an answer from another server version")
	}

	tc, d = detection(t, h)
	if _, err := fixturegen.GetError(ctx, tc, d, "/api/v3/episodefile?seriesId=9", true); err == nil {
		t.Fatal("GetError accepted a header-less answer its step expected with the header")
	}
	tc, d = detection(t, h)
	a, err = fixturegen.GetError(ctx, tc, d, "/api/v3/episodefile?seriesId=9", false)
	if err != nil || a.AppVersion != "" || render(t, a.Body) != `{"message":"Series with ID 9 does not exist"}` {
		t.Fatalf("GetError = %+v, %v", a, err)
	}
	// A header-less answer ends the detection, so no read follows on it.
	if _, err := fixturegen.Get(ctx, tc, d, "/api/v3/tag", transport.MaxListBytes); err == nil {
		t.Fatal("a read was sent on a detection a header-less answer ended")
	}
}

func TestGitleaksCountsFindings(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks is not installed; the recorder requires it, CI's repo job scans the tree itself")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "clean.json"), []byte(`{"title":"Code Geass"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := fixturegen.Gitleaks(t.Context(), dir, t.TempDir())
	if err != nil || n != 0 {
		t.Fatalf("clean tree: %d findings, %v", n, err)
	}
	// Built at run time, so the source holds no token-shaped literal.
	token := "ghp_" + strings.Repeat("aB3dE6gH9k", 4)[:36]
	if err := os.WriteFile(filepath.Join(dir, "leak.json"), []byte(`{"title":"`+token+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err = fixturegen.Gitleaks(t.Context(), dir, t.TempDir())
	if err != nil || n == 0 {
		t.Fatalf("planted token: %d findings, %v", n, err)
	}
}

// An answer whose declared member carries a value its DTO cannot decode fails the
// recording, so live drift never becomes a fixture the engine accepts.
func TestWriteRefusesAnAnswerItsDTOCannotDecode(t *testing.T) {
	cases := map[string]struct {
		typ    reflect.Type
		body   string
		member string
	}{
		"an object for a string":         {reflect.TypeFor[series](), `{"title": {"path": "/config/leak"}}`, "title"},
		"a string for an object":         {reflect.TypeFor[series](), `{"language": "/config/leak"}`, "language"},
		"an object for a list":           {reflect.TypeFor[series](), `{"seasons": {"a": "/config/leak"}}`, "seasons"},
		"a string for a list":            {reflect.TypeFor[series](), `{"tags": "/config/leak"}`, "tags"},
		"a string for an int":            {reflect.TypeFor[series](), `{"tvdbId": "/config/leak"}`, "tvdbId"},
		"a number for a string":          {reflect.TypeFor[series](), `{"title": 7}`, "title"},
		"a fraction for an int":          {reflect.TypeFor[series](), `{"id": 1.5}`, "id"},
		"a string for a bool, nested":    {reflect.TypeFor[series](), `{"seasons": [{"seasonNumber": 1}, {"monitored": "/config/leak"}]}`, "seasons[1].monitored"},
		"an object for a decoder member": {reflect.TypeFor[[]history](), `[{"eventType": {"leak": "/config/leak"}}]`, "[0].eventType"},
		"a value its decoder refuses":    {reflect.TypeFor[[]history](), `[{"date": 5}]`, "[0].date"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := plantedRecording(t)
			r.AddJSON("a/bad.json", decode(t, tc.body), tc.typ)
			root := t.TempDir()
			n, err := r.Write(root)
			if err == nil || n != 0 || len(readTree(t, root)) != 0 {
				t.Fatalf("Write = %d, %v; want a refusal and nothing written", n, err)
			}
			if !strings.Contains(err.Error(), "a/bad.json: member "+tc.member+":") || strings.Contains(err.Error(), "/config") {
				t.Fatalf("error must name the file and member %s, never the value: %v", tc.member, err)
			}
		})
	}
}

// A recording owns each top-level directory it writes: a file there it does not write
// is a stale answer, refused before anything is written.
func TestWriteRefusesStaleFixtures(t *testing.T) {
	root := t.TempDir()
	plant := func(name string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"title":"stale"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	recording := func() *fixturegen.Recording {
		r := plantedRecording(t)
		r.AddJSON("c/deep/tags.json", decode(t, `[{"id": 3, "label": "private-label"}]`), reflect.TypeFor[[]tag]())
		return r
	}
	plant("c/old/series.json")
	plant("other/keep.json")
	n, err := recording().Write(root)
	if err == nil || n != 0 || !strings.Contains(err.Error(), "c/old/series.json: a stale fixture") {
		t.Fatalf("Write = %d, %v; want the stale file refused", n, err)
	}
	if got := slices.Sorted(maps.Keys(readTree(t, root))); !slices.Equal(got, []string{"c/old/series.json", "other/keep.json"}) {
		t.Fatalf("a refused recording changed the tree: %q", got)
	}
	if err := os.RemoveAll(filepath.Join(root, "c", "old")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := recording().Write(root); err != nil {
			t.Fatalf("Write over its own files: %v", err)
		}
	}
	if _, ok := readTree(t, root)["other/keep.json"]; !ok {
		t.Fatal("a directory the recording does not write was touched")
	}
}

func TestDecodeReadsExactlyOneValue(t *testing.T) {
	for in, ok := range map[string]bool{
		`{"a":1}`: true, "[1]\n \t": true, `1`: true,
		`1]`: false, `{"a":1}}`: false, `1 2`: false, `{} []`: false, ``: false, `{`: false,
	} {
		if _, err := fixturegen.Decode([]byte(in)); (err == nil) != ok {
			t.Errorf("Decode(%q) error = %v, want ok %t", in, err, ok)
		}
	}
}

func titles(t *testing.T, ts ...string) map[string][]byte {
	t.Helper()
	list := make([]map[string]string, 0, len(ts))
	for _, s := range ts {
		list = append(list, map[string]string{"title": s})
	}
	b, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"v4/series.json": b}
}

func TestCheckFindsAPathAfterAnyBoundary(t *testing.T) {
	for name, title := range map[string]string{
		"a line feed":            "line\n/config/private.db",
		"a carriage return":      "line\r/config/private.db",
		"a vertical tab":         "line\v/config/private.db",
		"a no-break space":       "line\u00a0/config/private.db",
		"a non-ASCII quote":      "«/config/private.db»",
		"a closing bracket":      "(x)/config/private.db",
		"a non-ASCII segment":    "/données/x.db",
		"a non-ASCII start":      "/ébauche/x.db",
		"a symbol start":         "/🔥Media/private.mkv",
		"a bracket start":        "/[media]/x.mkv",
		"a double slash root":    "//nas/share/x.mkv",
		"a UNC path":             `\\nas\share\private`,
		"an extended path":       `\\?\C:\media\x.mkv`,
		"a device path":          `\\.\pipe\sonarr`,
		"a root-relative path":   `see \media\x.mkv`,
		"a drive path":           `C:\media\x.mkv`,
		"a drive root":           `C:\`,
		"a forward drive path":   "C:/media/x.mkv",
		"a climb out of /tv":     "/tv/../config/x.db",
		"a climb out of /movies": "/movies/a/../../etc",
		"a climb after a space":  "/tv/folder-1/Season 01/../../../config",
		"a backslash climb":      `/tv/folder-1\..\..\config`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := fixturegen.Check(titles(t, title)); err == nil {
				t.Fatalf("Check accepted %q", title)
			}
		})
	}
	words := titles(t, "東京/大阪", "東京/Zero", "Fate/Zero", "English / Japanese", "a /", "AC/DC",
		`Season 01\x.mkv`, "Season 01/x.mkv", "/tv/folder-1/Season 01/x.mkv", "/movies/folder-1/x.mkv")
	if err := fixturegen.Check(words); err != nil {
		t.Fatalf("Check refused a slash inside or between words, a relative path or a synthetic root: %v", err)
	}
}

// Check needs no recording: a committed error answer edited by hand to hold a server
// path is refused on its own.
func TestCheckRefusesAHandEditedErrorMessage(t *testing.T) {
	for name, msg := range map[string]string{
		"a POSIX path": `{"message":"Could not read /data/tv/x.mkv"}`,
		"a UNC path":   `{"message":"Could not read \\\\nas\\share\\x.mkv"}`,
	} {
		t.Run(name, func(t *testing.T) {
			err := fixturegen.Check(map[string][]byte{"sonarr/v5-api-v5/episodefile-missing-parent.json": []byte(msg)})
			want := "sonarr/v5-api-v5/episodefile-missing-parent.json: an absolute path outside the synthetic roots"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("Check = %v, want it to name %q", err, want)
			}
		})
	}
}
