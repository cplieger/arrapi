package sonarr

import (
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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/detect"
	"github.com/cplieger/arrapi/v3/internal/fixturegen"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// The fixture series, found in each library by public TVDB id, never by row id.
const (
	tvdbCodeGeass  = 79525
	tvdbFirefly    = 78874
	absentSeriesID = 99999999
)

var orderFixtures = []struct {
	role string
	tvdb int64
}{
	{"firefly", tvdbFirefly},
	{"office", 73244},
	{"monogatari", 102261},
	{"haruhi", 79414},
	{"swresistance", 351575},
}

const (
	dirSonarrV4APIv3  = "sonarr/v4-api-v3"
	dirSonarrV5APIv5  = "sonarr/v5-api-v5"
	dirSonarrV5Refuse = "sonarr/v5-api-v3-refusal"
	historySample     = 8
)

// recordTarget is one server the recorder reads; opts carry a test's transport.
type recordTarget struct {
	url  string
	key  arrapi.APIKey
	opts []Option
}

// errorStep is one error read and whether its answer carries X-Application-Version.
type errorStep struct {
	name   string
	path   string
	header bool
}

// sonarrShape is what differs between the two Sonarr fixture directories.
type sonarrShape struct {
	accept     func(major int) bool
	types      sonarrTypes
	env        string
	label      string
	from       string
	dir        string
	prefix     string
	filesQuery string
	api        arrapi.API
	errors     []errorStep
	orders     bool
}

type sonarrTypes struct {
	series, seriesList, episodes, files, history, tags, roots reflect.Type
}

func sonarrV4APIv3Shape() sonarrShape {
	return sonarrShape{
		env: "ARRAPI_SONARR_V4_URL", label: "Sonarr v4 (API v3)", from: "a Sonarr 4.x",
		dir: dirSonarrV4APIv3, prefix: sonarrV4Prefix, filesQuery: "&includeEpisodeFile=true", api: arrapi.APIv3,
		accept: func(major int) bool { return major == 4 },
		types: sonarrTypes{
			series: reflect.TypeFor[sonarrV4Series](), seriesList: reflect.TypeFor[[]sonarrV4Series](),
			episodes: reflect.TypeFor[[]sonarrV4Episode](), files: reflect.TypeFor[[]sonarrV4EpisodeFile](),
			history: reflect.TypeFor[[]sonarrV4History](), tags: reflect.TypeFor[[]sonarrV4Tag](),
			roots: reflect.TypeFor[[]sonarrV4RootFolder](),
		},
		errors: []errorStep{
			{"v5-system-status-404", "/api/v5/system/status", true},
			{"series-404", fmt.Sprintf("%s/series/%d", sonarrV4Prefix, absentSeriesID), true},
		},
	}
}

func sonarrV5APIv5Shape() sonarrShape {
	return sonarrShape{
		env: "ARRAPI_SONARR_V5_URL", label: "Sonarr v5 (API v5)", from: "a Sonarr 5 or later",
		dir: dirSonarrV5APIv5, prefix: sonarrV5Prefix, filesQuery: "&includeSubresources=episodeFile", api: arrapi.APIv5,
		accept: func(major int) bool { return major >= 5 },
		types: sonarrTypes{
			series: reflect.TypeFor[sonarrV5Series](), seriesList: reflect.TypeFor[[]sonarrV5Series](),
			episodes: reflect.TypeFor[[]sonarrV5Episode](), files: reflect.TypeFor[[]sonarrV5EpisodeFile](),
			history: reflect.TypeFor[[]sonarrV5History](), tags: reflect.TypeFor[[]sonarrV5Tag](),
			roots: reflect.TypeFor[[]sonarrV5RootFolder](),
		},
		errors: []errorStep{
			{"series-404", fmt.Sprintf("%s/series/%d", sonarrV5Prefix, absentSeriesID), true},
			// Sonarr 5.0.0.2701 and later send the header on this exception-pipeline answer
			// (Sonarr#9161); recording from an earlier Sonarr 5 fails this step.
			{"episodefile-missing-parent", seriesQuery(sonarrV5Prefix, "episodefile", absentSeriesID), true},
		},
		orders: true,
	}
}

var sonarrFixtureRules = func() fixturegen.Rules {
	id := func(role string) fixturegen.Rule { return fixturegen.Rule{Kind: fixturegen.ID, Role: role} }
	series := map[string]fixturegen.Rule{"id": id("series"), "tags": id("tag")}
	stats := map[string]fixturegen.Rule{"episodeFileCount": {Kind: fixturegen.FileCount, Role: "series"}}
	episode := map[string]fixturegen.Rule{"id": id("episode"), "seriesId": id("series"), "episodeFileId": id("episodefile")}
	file := map[string]fixturegen.Rule{
		"id":           id("episodefile"),
		"size":         {Kind: fixturegen.Size},
		"relativePath": {Kind: fixturegen.RelPath, Role: "episode-file"},
		"path":         {Kind: fixturegen.Path, Role: "episode-file", Root: "/tv"},
		"sceneName":    {Kind: fixturegen.Pseudo, Role: "scene"},
		"releaseGroup": {Kind: fixturegen.Pseudo, Role: "group"},
	}
	history := map[string]fixturegen.Rule{"id": id("history"), "seriesId": id("series"), "date": {Kind: fixturegen.Date}}
	tag := map[string]fixturegen.Rule{"id": id("tag"), "label": {Kind: fixturegen.Pseudo, Role: "tag"}}
	root := map[string]fixturegen.Rule{"path": {Kind: fixturegen.RootPath, Root: "/tv"}}
	return fixturegen.Rules{
		reflect.TypeFor[sonarrV4Series](): series, reflect.TypeFor[sonarrV5Series](): series,
		reflect.TypeFor[sonarrV4Statistics](): stats, reflect.TypeFor[sonarrV5Statistics](): stats,
		reflect.TypeFor[sonarrV4Episode](): episode, reflect.TypeFor[sonarrV5Episode](): episode,
		reflect.TypeFor[sonarrV4EpisodeFile](): file, reflect.TypeFor[sonarrV5EpisodeFile](): file,
		reflect.TypeFor[sonarrV4History](): history, reflect.TypeFor[sonarrV5History](): history,
		reflect.TypeFor[sonarrV4Tag](): tag, reflect.TypeFor[sonarrV5Tag](): tag,
		reflect.TypeFor[sonarrV4RootFolder](): root, reflect.TypeFor[sonarrV5RootFolder](): root,
	}
}()

// recordSonarr records testdata/sonarr under root: the Sonarr v4 (API v3) directory
// from v4 only, the Sonarr v5 (API v5) directory and the refusal input from v5 only,
// with one synthesis state over all three. It writes nothing unless every read and
// every check passed.
func recordSonarr(ctx context.Context, root string, v4, v5 recordTarget, since time.Time) (recorded, error) {
	return recordSonarrShapes(ctx, root, since, []sonarrRun{{v4, sonarrV4APIv3Shape()}, {v5, sonarrV5APIv5Shape()}})
}

type sonarrRun struct {
	target recordTarget
	shape  sonarrShape
}

// recorded is what a recording wrote: the file count and how many real values each
// synthesis map replaced, never the values.
type recorded struct {
	counts map[string]int
	files  int
}

func recordSonarrShapes(ctx context.Context, root string, since time.Time, runs []sonarrRun) (recorded, error) {
	rec := fixturegen.NewRecording(sonarrFixtureRules)
	for _, run := range runs {
		if err := recordSonarrTarget(ctx, rec, run.target, run.shape, since); err != nil {
			return recorded{}, err
		}
	}
	n, err := rec.Write(root)
	return recorded{files: n, counts: rec.Counts()}, err
}

func recordSonarrTarget(ctx context.Context, rec *fixturegen.Recording, tgt recordTarget, sh sonarrShape, since time.Time) error {
	c, err := New(tgt.url, tgt.key, tgt.opts...)
	if err != nil {
		return err
	}
	defer c.Close()
	rec.Secret(c.t.ContainsKey)
	if u, err := url.Parse(tgt.url); err == nil {
		rec.Private(u.Hostname())
	}
	d, err := sh.identify(ctx, c)
	if err != nil {
		return err
	}

	r := fixturegen.NewReader(rec, c.t, d, sh.dir)
	r.Read(ctx, "api.json", "/api", transport.MaxObjectBytes, reflect.TypeFor[detect.APIDoc]())
	status := r.Read(ctx, "system-status.json", sh.prefix+"/system/status", transport.MaxObjectBytes, reflect.TypeFor[detect.StatusDoc]())
	rec.Private(statusPrivates(status)...)

	list := r.List(ctx, sh.prefix+"/series")
	cg := r.Find(list, "tvdbId", tvdbCodeGeass)
	r.Add("series-codegeass.json", []any{cg}, sh.types.seriesList)
	id, _ := fixturegen.Int(memberOf(cg, "id"))
	r.Read(ctx, "series-by-id-codegeass.json", fmt.Sprintf("%s/series/%d", sh.prefix, id), transport.MaxObjectBytes, sh.types.series)
	r.Read(ctx, "episodes-codegeass-files.json", seriesQuery(sh.prefix, "episode", int(id))+sh.filesQuery, transport.MaxListBytes, sh.types.episodes)
	eps := r.Read(ctx, "episodes-codegeass.json", seriesQuery(sh.prefix, "episode", int(id)), transport.MaxListBytes, sh.types.episodes)
	r.Add("episodefiles-codegeass.json", r.List(ctx, seriesQuery(sh.prefix, "episodefile", int(id))), sh.types.files)
	rec.Carries(sh.dir, id, episodesWithFile(eps))
	if sh.orders {
		for _, o := range orderFixtures {
			s := r.Find(list, "tvdbId", o.tvdb)
			r.Add("series-"+o.role+".json", []any{s}, sh.types.seriesList)
			if o.tvdb != tvdbFirefly {
				continue
			}
			fid, _ := fixturegen.Int(memberOf(s, "id"))
			feps := r.Read(ctx, "episodes-firefly.json", seriesQuery(sh.prefix, "episode", int(fid)), transport.MaxListBytes, sh.types.episodes)
			r.Add("episodefiles-firefly.json", r.List(ctx, seriesQuery(sh.prefix, "episodefile", int(fid))), sh.types.files)
			rec.Carries(sh.dir, fid, episodesWithFile(feps))
		}
	}
	r.Add("history-since.json", fixturegen.Sample(r.List(ctx, sinceQuery(sh.prefix, since)), "eventType", historySample), sh.types.history)
	r.Read(ctx, "tags.json", sh.prefix+"/tag", transport.MaxListBytes, sh.types.tags)
	r.Read(ctx, "rootfolders.json", sh.prefix+"/rootfolder", transport.MaxListBytes, sh.types.roots)
	// A header-less answer ends the detection it was read on, so those error reads
	// come after every other read, each on a detection of its own.
	for _, step := range sh.errors {
		if step.header {
			r.Error(ctx, step.name, step.path, true)
		}
	}
	if r.Err != nil {
		return fmt.Errorf("%s: %w", sh.label, r.Err)
	}
	for _, step := range sh.errors {
		if step.header {
			continue
		}
		if err := sh.errorOnOwnDetection(ctx, rec, c, step); err != nil {
			return err
		}
	}
	if sh.api == arrapi.APIv5 {
		return recordRefusalInput(ctx, rec, c, sh)
	}
	return nil
}

// identify runs Info and returns its detection only when the server is the shape's:
// a swapped pair of URLs stops here, before any data GET reaches a Sonarr 5's /api/v3.
func (sh sonarrShape) identify(ctx context.Context, c *Client) (*transport.Detection, error) {
	d, err := c.s.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: Info: %w", sh.env, err)
	}
	major, _ := strconv.Atoi(strings.SplitN(d.Identity.AppVersion, ".", 2)[0])
	if d.API != sh.api || !sh.accept(major) {
		return nil, fmt.Errorf("%s answered Sonarr %s (API %s): the %s fixtures come only from %s", sh.env, d.Version, d.API, sh.label, sh.from)
	}
	return d, nil
}

func (sh sonarrShape) errorOnOwnDetection(ctx context.Context, rec *fixturegen.Recording, c *Client, step errorStep) error {
	d, err := sh.identify(ctx, c)
	if err != nil {
		return err
	}
	r := fixturegen.NewReader(rec, c.t, d, sh.dir)
	r.Error(ctx, step.name, step.path, step.header)
	if r.Err != nil {
		return fmt.Errorf("%s: %w", sh.label, r.Err)
	}
	return nil
}

// recordRefusalInput is the one /api/v3 GET to a Sonarr 5: its system/status, the
// answer detection must refuse, read only after Info identified the server as Sonarr
// 5 or later. It is never Sonarr v4 (API v3) evidence.
func recordRefusalInput(ctx context.Context, rec *fixturegen.Recording, c *Client, sh sonarrShape) error {
	d, err := sh.identify(ctx, c)
	if err != nil {
		return err
	}
	r := fixturegen.NewReader(rec, c.t, d, dirSonarrV5Refuse)
	r.Read(ctx, "system-status.json", sonarrV4Prefix+"/system/status", transport.MaxObjectBytes, reflect.TypeFor[detect.StatusDoc]())
	if r.Err != nil {
		return fmt.Errorf("refusal input: %w", r.Err)
	}
	return nil
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

// episodesWithFile counts the items of an episode list answer whose hasFile is true:
// Sonarr's episodeFileCount counts those episodes, not files (v4-develop
// SeriesStatisticsRepository.cs:81).
func episodesWithFile(v any) int {
	list, _ := v.([]any)
	n := 0
	for _, e := range list {
		if ep, ok := e.(map[string]any); ok && ep["hasFile"] == true {
			n++
		}
	}
	return n
}

func memberOf(v map[string]any, name string) any {
	if v == nil {
		return nil
	}
	return v[name]
}

// uriFake answers each request by method and request URI, query included; an
// unrouted request gets a header-less 404, as Sonarr's exception pipeline writes one.
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

// library is a fake Sonarr's answers under prefix at version v, in that generation's
// wire shape; every deployment value is planted.
func library(v, prefix string, five bool) map[string]step {
	file := `{"id":987654,"seriesId":987654,"seasonNumber":1,"size":7777777777,
		"relativePath":"Season 01/Code Geass - S01E01-E02 - Pilot.mkv",
		"path":"/data/tv/Code Geass/Season 01/Code Geass - S01E01-E02 - Pilot.mkv",
		"sceneName":"Code.Geass.S01E01E02.1080p.BluRay-SHADOWGRP","releaseGroup":"SHADOWGRP",
		"quality":{"quality":{"id":7,"name":"Bluray-1080p"},"revision":{"version":2,"real":0,"isRepack":true}},
		"mediaInfo":` + mediaInfo(five) + `}`
	otherFile := `{"id":600,"seriesId":987654,"seasonNumber":1,"size":1234567,"relativePath":"Season 01/Code Geass - S01E03.mkv",
		"path":"/data/tv/Code Geass/Season 01/Code Geass - S01E03.mkv","sceneName":"","releaseGroup":"OTHERGRP"}`
	order := ""
	if five {
		order = `"seasonType":"official","seasonTypes":[],"language":{"id":8,"name":"Japanese"},"aniListIds":[1575],`
	}
	cg := `{"id":987654,"title":"Code Geass","titleSlug":"code-geass","tvdbId":79525,"tmdbId":31724,"year":2006,` + order +
		`"tags":[3],"path":"/data/tv/Code Geass","seasons":[{"seasonNumber":1,"monitored":true}],
		"statistics":{"episodeFileCount":51,"sizeOnDisk":99}}`
	list := `[` + cg + `,{"id":5,"title":"Other Show","tvdbId":1,"statistics":{"episodeFileCount":0}}`
	if five {
		for i, o := range orderFixtures {
			list += fmt.Sprintf(`,{"id":%d,"title":"Order %s","tvdbId":%d,"seasonType":"alternate","seasonTypes":[{"type":"alternate","name":"Book Order"}],"statistics":{"episodeFileCount":%d}}`,
				40+i, o.role, o.tvdb, i)
		}
	}
	list += `]`
	episodes := `[{"id":987654,"seriesId":987654,"episodeFileId":987654,"seasonNumber":1,"episodeNumber":1,"hasFile":true,"title":"The Day a New Demon Was Born","airDate":"2006-10-06","episodeFile":` + file + `},
		{"id":12,"seriesId":987654,"episodeFileId":987654,"seasonNumber":1,"episodeNumber":2,"hasFile":true,"title":"The White Knight Awakens","airDate":"2006-10-13","episodeFile":` + file + `},
		{"id":13,"seriesId":987654,"episodeFileId":600,"seasonNumber":1,"episodeNumber":3,"hasFile":true,"title":"The False Classmate","airDate":"2006-10-20","episodeFile":` + otherFile + `},
		{"id":14,"seriesId":987654,"episodeFileId":0,"seasonNumber":1,"episodeNumber":4,"hasFile":false,"title":"Refrain","airDate":"2006-10-27"}]`
	history := `[{"id":2,"seriesId":5,"episodeId":9,"date":"2031-07-09T10:00:00Z","eventType":"grabbed","data":{"downloadUrl":"https://indexer.invalid/api?apikey=CREDENTIAL-abc"}},
		{"id":987654,"seriesId":987654,"episodeId":987654,"date":"` + plantedDate + `","eventType":"downloadFolderImported","data":{"droppedPath":"/downloads/x.mkv"}}]`
	status := `{"appName":"Sonarr","version":"` + v + `","appData":"/config","startupPath":"/app/sonarr/bin","instanceName":"Example Sonarr","osName":"debian"}`
	s := func(body string) step { return ans(http.StatusOK, v, body) }
	routes := map[string]step{
		"GET /api":                                       s(apiBody(map[bool]string{false: "v3", true: "v5"}[five])),
		"GET " + prefix + "/system/status":               s(status),
		"GET " + prefix + "/series":                      s(list),
		"GET " + prefix + "/series/987654":               s(cg),
		"GET " + prefix + "/episode?seriesId=987654":     s(episodes),
		"GET " + prefix + "/episodefile?seriesId=987654": s(`[` + file + `,` + otherFile + `]`),
		"GET " + sinceQuery(prefix, recordSince):         s(history),
		"GET " + prefix + "/tag":                         s(`[{"id":3,"label":"private-label"}]`),
		"GET " + prefix + "/rootfolder":                  s(`[{"id":1,"path":"/data/tv/","freeSpace":1}]`),
	}
	if five {
		routes["GET "+prefix+"/episode?seriesId=987654&includeSubresources=episodeFile"] = s(episodes)
		routes["GET "+prefix+"/episode?seriesId=40"] = s(`[{"id":50,"seriesId":40,"episodeFileId":0,"seasonNumber":1,"episodeNumber":1,"title":"Serenity"}]`)
		routes["GET "+prefix+"/episodefile?seriesId=40"] = s(`[]`)
		routes["GET "+prefix+"/series/99999999"] = ans(http.StatusNotFound, v, "")
		routes["GET /api/v3/system/status"] = s(status)
	} else {
		routes["GET "+prefix+"/episode?seriesId=987654&includeEpisodeFile=true"] = s(episodes)
		routes["GET /api/v5/system/status"] = ans(http.StatusNotFound, v, "")
		routes["GET "+prefix+"/series/99999999"] = ans(http.StatusNotFound, v,
			`{"type":"https://tools.ietf.org/html/rfc7231#section-6.5.4","title":"Not Found","status":404,"traceId":"00-b796a856f1a02fc5188b471b4de943ca-b5dc73fac4bb0cf9-00"}`)
	}
	return routes
}

func mediaInfo(five bool) string {
	if five {
		return `{"videoCodec":"x265","audioStreams":[{"language":"jpn","codec":"flac"},{"language":"eng"}]}`
	}
	return `{"videoCodec":"x265","audioLanguages":"jpn/eng","audioCodec":"FLAC"}`
}

func sonarr4Library() *uriFake { return &uriFake{routes: library(v4, sonarrV4Prefix, false)} }

// sonarr5Library answers as Sonarr 5.0.0.2701 or later, whose exception pipeline sends
// X-Application-Version (Sonarr#9161).
func sonarr5Library() *uriFake {
	f := &uriFake{routes: library(v5, sonarrV5Prefix, true)}
	f.routes["GET /api/v5/episodefile?seriesId=99999999"] = ans(http.StatusNotFound, v5, missingParentBody)
	return f
}

const missingParentBody = `{"message":"Series with ID 99999999 does not exist","description":"NzbDrone.Core.Datastore.ModelNotFoundException"}`

func target(f http.RoundTripper, key arrapi.APIKey) recordTarget {
	return recordTarget{url: fakeBase, key: key, opts: []Option{WithHTTPClient(&http.Client{Transport: f}), WithLogger(slog.New(slog.DiscardHandler))}}
}

// sonarrFixtureFiles is every file design section 11 names for testdata/sonarr.
var sonarrFixtureFiles = []string{
	"sonarr/v4-api-v3/api.json", "sonarr/v4-api-v3/episodefiles-codegeass.json",
	"sonarr/v4-api-v3/episodes-codegeass-files.json", "sonarr/v4-api-v3/episodes-codegeass.json",
	"sonarr/v4-api-v3/history-since.json", "sonarr/v4-api-v3/rootfolders.json",
	"sonarr/v4-api-v3/series-404.headers", "sonarr/v4-api-v3/series-404.json",
	"sonarr/v4-api-v3/series-by-id-codegeass.json", "sonarr/v4-api-v3/series-codegeass.json",
	"sonarr/v4-api-v3/system-status.json", "sonarr/v4-api-v3/tags.json",
	"sonarr/v4-api-v3/v5-system-status-404.headers",
	"sonarr/v5-api-v3-refusal/system-status.json",
	"sonarr/v5-api-v5/api.json", "sonarr/v5-api-v5/episodefile-missing-parent.headers",
	"sonarr/v5-api-v5/episodefile-missing-parent.json", "sonarr/v5-api-v5/episodefiles-codegeass.json",
	"sonarr/v5-api-v5/episodefiles-firefly.json", "sonarr/v5-api-v5/episodes-codegeass-files.json",
	"sonarr/v5-api-v5/episodes-codegeass.json", "sonarr/v5-api-v5/episodes-firefly.json",
	"sonarr/v5-api-v5/history-since.json", "sonarr/v5-api-v5/rootfolders.json",
	"sonarr/v5-api-v5/series-404.headers", "sonarr/v5-api-v5/series-by-id-codegeass.json",
	"sonarr/v5-api-v5/series-codegeass.json", "sonarr/v5-api-v5/series-firefly.json",
	"sonarr/v5-api-v5/series-haruhi.json", "sonarr/v5-api-v5/series-monogatari.json",
	"sonarr/v5-api-v5/series-office.json", "sonarr/v5-api-v5/series-swresistance.json",
	"sonarr/v5-api-v5/system-status.json", "sonarr/v5-api-v5/tags.json",
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

func recordFakes(t *testing.T, f4, f5 http.RoundTripper) (map[string]string, error) {
	t.Helper()
	root := t.TempDir()
	got, err := recordSonarr(t.Context(), root, target(f4, testKey), target(f5, testKey), recordSince)
	files := readFixtures(t, root)
	if n := got.files; n != len(files) {
		t.Errorf("recordSonarr reported %d files, wrote %d", n, len(files))
	}
	return files, err
}

func TestRecordSonarrWritesEveryFixture(t *testing.T) {
	f4, f5 := sonarr4Library(), sonarr5Library()
	files, err := recordFakes(t, f4, f5)
	if err != nil {
		t.Fatalf("recordSonarr: %v", err)
	}
	if got := slices.Sorted(maps.Keys(files)); !slices.Equal(got, sonarrFixtureFiles) {
		t.Fatalf("files =\n  %q\nwant\n  %q", got, sonarrFixtureFiles)
	}
	for _, r := range f4.requests() {
		if !strings.HasPrefix(r, "GET /api/v3/") && r != "GET /api" && r != "GET /api/v5/system/status" {
			t.Errorf("the Sonarr v4 (API v3) fake got %s", r)
		}
	}
	// The one /api/v3 request to the Sonarr 5.0 is the refusal input, after an Info.
	got := f5.requests()
	var v3 []string
	for _, r := range got {
		if strings.HasPrefix(r, "GET /api/v3/") {
			v3 = append(v3, r)
		}
	}
	tail := []string{"GET /api", "GET /api/v5/system/status", "GET /api/v3/system/status"}
	if !slices.Equal(v3, []string{"GET /api/v3/system/status"}) || !slices.Equal(got[len(got)-3:], tail) {
		t.Fatalf("Sonarr v5 (API v5) requests = %q", got)
	}
	if want := "X-Application-Version: " + v4 + "\n"; files["sonarr/v4-api-v3/series-404.headers"] != want {
		t.Errorf("series-404.headers = %q, want %q", files["sonarr/v4-api-v3/series-404.headers"], want)
	}
	if want := "X-Application-Version: " + v5 + "\n"; files["sonarr/v5-api-v5/episodefile-missing-parent.headers"] != want {
		t.Errorf("episodefile-missing-parent.headers = %q, want %q", files["sonarr/v5-api-v5/episodefile-missing-parent.headers"], want)
	}
}

// Every header-less error read runs after every other read of its server, each on a
// detection of its own, and nothing is read on a detection a header-less answer ended.
func TestRecordSonarrHeaderlessErrorReadsComeLast(t *testing.T) {
	f4, f5 := sonarr4Library(), sonarr5Library()
	f5.routes["GET /api/v5/episode?seriesId=99999999"] = ans(http.StatusNotFound, "", `{"message":"Series with ID 99999999 does not exist"}`)
	sh := sonarrV5APIv5Shape()
	sh.errors = append(sh.errors, errorStep{"episode-missing-parent", seriesQuery(sonarrV5Prefix, "episode", absentSeriesID), false})
	root := t.TempDir()
	_, err := recordSonarrShapes(t.Context(), root, recordSince, []sonarrRun{
		{target(f4, testKey), sonarrV4APIv3Shape()}, {target(f5, testKey), sh},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	files := readFixtures(t, root)
	if got := files["sonarr/v5-api-v5/episode-missing-parent.headers"]; got != "" {
		t.Errorf("episode-missing-parent.headers = %q, want empty for a header-less answer", got)
	}
	if _, ok := files["sonarr/v5-api-v5/episode-missing-parent.json"]; !ok {
		t.Error("episode-missing-parent.json not written")
	}
	got := f5.requests()
	want := []string{
		"GET /api", "GET /api/v5/system/status", "GET /api/v5/episode?seriesId=99999999",
		"GET /api", "GET /api/v5/system/status", "GET /api/v3/system/status",
	}
	if len(got) < len(want) || !slices.Equal(got[len(got)-len(want):], want) {
		t.Fatalf("Sonarr v5 (API v5) requests end\n  %q\nwant\n  %q", got, want)
	}
	for _, headered := range []string{"GET /api/v5/series/99999999", "GET /api/v5/episodefile?seriesId=99999999"} {
		if i := slices.Index(got, headered); i < 0 || i > len(got)-len(want) {
			t.Fatalf("the headered error read %s is not among the first phase: %q", headered, got)
		}
	}
}

func TestRecordSonarrStopsOnTheWrongServer(t *testing.T) {
	t.Run("a Sonarr v5 (API v5) behind ARRAPI_SONARR_V4_URL", func(t *testing.T) {
		f4, f5 := sonarr5Library(), sonarr5Library()
		files, err := recordFakes(t, f4, f5)
		want := "ARRAPI_SONARR_V4_URL answered Sonarr " + v5 + " (API v5): the Sonarr v4 (API v3) fixtures come only from a Sonarr 4.x"
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %q", err, want)
		}
		if got := f4.requests(); !slices.Equal(got, detectV5) {
			t.Errorf("requests = %q, want only the detection %q", got, detectV5)
		}
		if len(f5.requests()) != 0 || len(files) != 0 {
			t.Error("the recorder went on after the identity check failed")
		}
	})
	t.Run("a Sonarr v4 (API v3) behind ARRAPI_SONARR_V5_URL", func(t *testing.T) {
		f4, f5 := sonarr4Library(), sonarr4Library()
		files, err := recordFakes(t, f4, f5)
		want := "ARRAPI_SONARR_V5_URL answered Sonarr " + v4 + " (API v3): the Sonarr v5 (API v5) fixtures come only from a Sonarr 5 or later"
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %q", err, want)
		}
		if got := f5.requests(); !slices.Equal(got, detectV4) {
			t.Errorf("requests = %q, want only the detection %q", got, detectV4)
		}
		if len(files) != 0 {
			t.Error("files written after a failed identity check")
		}
	})
}

func TestRecordSonarrErrorHeaderMismatchWritesNothing(t *testing.T) {
	t.Run("a header where none is expected", func(t *testing.T) {
		f4, f5 := sonarr4Library(), sonarr5Library()
		f5.routes["GET /api/v5/episode?seriesId=99999999"] = ans(http.StatusNotFound, v5, `{"message":"x"}`)
		sh := sonarrV5APIv5Shape()
		sh.errors = append(sh.errors, errorStep{"episode-missing-parent", seriesQuery(sonarrV5Prefix, "episode", absentSeriesID), false})
		root := t.TempDir()
		_, err := recordSonarrShapes(t.Context(), root, recordSince, []sonarrRun{
			{target(f4, testKey), sonarrV4APIv3Shape()}, {target(f5, testKey), sh},
		})
		if files := readFixtures(t, root); err == nil || len(files) != 0 {
			t.Fatalf("err = %v, files = %d; want a failure and nothing written", err, len(files))
		}
		if got := f5.requests(); got[len(got)-1] != "GET /api/v5/episode?seriesId=99999999" {
			t.Errorf("a read followed the failed step: %q", got)
		}
	})
	// A Sonarr 5 before 5.0.0.2701 writes the missing-parent answer without the header
	// (Sonarr#9161), so its recording stops there.
	t.Run("a Sonarr 5 before 5.0.0.2701", func(t *testing.T) {
		f4, f5 := sonarr4Library(), sonarr5Library()
		f5.routes["GET /api/v5/episodefile?seriesId=99999999"] = ans(http.StatusNotFound, "", missingParentBody)
		files, err := recordFakes(t, f4, f5)
		want := "X-Application-Version present is false, the recording expects true"
		if err == nil || !strings.Contains(err.Error(), want) || len(files) != 0 {
			t.Fatalf("err = %v, files = %d; want %q and nothing written", err, len(files), want)
		}
		if got := f5.requests(); got[len(got)-1] != "GET /api/v5/episodefile?seriesId=99999999" {
			t.Errorf("a read followed the failed step: %q", got)
		}
	})
	t.Run("no header where one is expected", func(t *testing.T) {
		f4, f5 := sonarr4Library(), sonarr5Library()
		f4.routes["GET /api/v3/series/99999999"] = ans(http.StatusNotFound, "", `{"title":"Not Found"}`)
		files, err := recordFakes(t, f4, f5)
		if err == nil || len(files) != 0 {
			t.Fatalf("err = %v, files = %d; want a failure and nothing written", err, len(files))
		}
		if got := f4.requests(); got[len(got)-1] != "GET /api/v3/series/99999999" || len(f5.requests()) != 0 {
			t.Errorf("a read followed the failed step: %q", got)
		}
	})
}

func TestRecordSonarrRefusesAnswersFromAnotherVersion(t *testing.T) {
	for name, key := range map[string]string{
		"a data answer":   "GET /api/v3/series",
		"an error answer": "GET /api/v3/series/99999999",
	} {
		t.Run(name, func(t *testing.T) {
			f4, f5 := sonarr4Library(), sonarr5Library()
			f4.routes[key] = ans(http.StatusOK, "5.0.0.1", `[]`)
			if key == "GET /api/v3/series/99999999" {
				f4.routes[key] = ans(http.StatusNotFound, "5.0.0.1", ``)
			}
			files, err := recordFakes(t, f4, f5)
			if _, ok := errors.AsType[*arrapi.ServerChangedError](err); !ok || len(files) != 0 {
				t.Fatalf("err = %v, files = %d; want *arrapi.ServerChangedError and nothing written", err, len(files))
			}
		})
	}
}

func TestRecordSonarrProjectsAwayDeploymentMembers(t *testing.T) {
	files, err := recordFakes(t, sonarr4Library(), sonarr5Library())
	if err != nil {
		t.Fatalf("recordSonarr: %v", err)
	}
	for name, body := range files {
		for _, p := range []string{
			"CREDENTIAL", "apikey", "downloadUrl", `"data"`, "appData", "startupPath", "instanceName",
			"Example Sonarr", "/config", "/downloads", "traceId", "rfc7231", "sizeOnDisk", "debian", "description", "flac", "FLAC",
		} {
			if strings.Contains(body, p) {
				t.Errorf("%s holds %q", name, p)
			}
		}
	}
	status := map[string]any{}
	if err := json.Unmarshal([]byte(files["sonarr/v4-api-v3/system-status.json"]), &status); err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(status)); !slices.Equal(got, []string{"appName", "arrapiUnknownField", "version"}) {
		t.Errorf("system-status.json members = %q", got)
	}
}

func TestRecordSonarrPseudonymizesConsistently(t *testing.T) {
	files, err := recordFakes(t, sonarr4Library(), sonarr5Library())
	if err != nil {
		t.Fatalf("recordSonarr: %v", err)
	}
	for name, body := range files {
		for _, p := range []string{"/data", "SHADOWGRP", "OTHERGRP", "Code.Geass", "private-label", "Pilot.mkv", "S01E03", "sonarr.test"} {
			if strings.Contains(body, p) {
				t.Errorf("%s holds %q", name, p)
			}
		}
	}
	var f4, f5 []sonarrV4EpisodeFile
	decodeInto(t, files["sonarr/v4-api-v3/episodefiles-codegeass.json"], &f4)
	decodeInto(t, files["sonarr/v5-api-v5/episodefiles-codegeass.json"], &f5)
	if len(f4) != 2 || len(f5) != 2 {
		t.Fatalf("file lists = %d, %d", len(f4), len(f5))
	}
	// Real ids 600 < 987654 and sizes 1234567 < 7777777777 keep their order.
	want := sonarrV4EpisodeFile{
		ID: 2, SeasonNumber: 1, Size: 1001000000, RelativePath: "Season 01/episode-file-2.mkv",
		Path: "/tv/folder-1/Season 01/episode-file-2.mkv", SceneName: "scene-1", ReleaseGroup: "group-2",
	}
	for i, got := range []sonarrV4EpisodeFile{f4[0], f5[0]} {
		got.Quality, got.MediaInfo = nil, nil
		if got != want {
			t.Errorf("directory %d file = %+v, want %+v", i, got, want)
		}
	}
	var tags []sonarrV4Tag
	decodeInto(t, files["sonarr/v4-api-v3/tags.json"], &tags)
	if len(tags) != 1 || tags[0].Label != "tag-1" {
		t.Errorf("tags = %+v", tags)
	}
	var roots []sonarrV4RootFolder
	decodeInto(t, files["sonarr/v5-api-v5/rootfolders.json"], &roots)
	if len(roots) != 1 || roots[0].Path != "/tv/root-1" {
		t.Errorf("root folders = %+v", roots)
	}
	var cg []sonarrV5Series
	decodeInto(t, files["sonarr/v5-api-v5/series-codegeass.json"], &cg)
	if len(cg) != 1 || cg[0].Title != "Code Geass" || cg[0].TitleSlug != "code-geass" || cg[0].Language.Name != "Japanese" {
		t.Errorf("public metadata not kept: %+v", cg)
	}
}

func TestRecordSonarrSynthesizesPlantedValues(t *testing.T) {
	files, err := recordFakes(t, sonarr4Library(), sonarr5Library())
	if err != nil {
		t.Fatalf("recordSonarr: %v", err)
	}
	for name, body := range files {
		for _, p := range []string{plantedDate, plantedSize, plantedID, "2031-07-09", "1234567"} {
			if strings.Contains(body, p) {
				t.Errorf("%s holds the planted %s", name, p)
			}
		}
	}
	for _, dir := range []string{dirSonarrV4APIv3, dirSonarrV5APIv5} {
		var series []sonarrV5Series
		var eps []sonarrV5Episode
		var efs []sonarrV5EpisodeFile
		var hist []sonarrV5History
		decodeInto(t, files[dir+"/series-codegeass.json"], &series)
		decodeInto(t, files[dir+"/episodes-codegeass-files.json"], &eps)
		decodeInto(t, files[dir+"/episodefiles-codegeass.json"], &efs)
		decodeInto(t, files[dir+"/history-since.json"], &hist)
		if len(series) != 1 || len(eps) != 4 || len(efs) != 2 || len(hist) != 2 {
			t.Fatalf("%s: counts %d %d %d %d", dir, len(series), len(eps), len(efs), len(hist))
		}
		s := series[0]
		// Sonarr counts episodes with a file, a two-episode file twice (v4-develop
		// SeriesStatisticsRepository.cs:81): 3 of the 4 here, from 2 files.
		if s.Statistics.EpisodeFileCount != 3 {
			t.Errorf("%s: episodeFileCount = %d, want the 3 episodes with a file the fixture carries", dir, s.Statistics.EpisodeFileCount)
		}
		if eps[0].EpisodeFileID != efs[0].ID || eps[1].EpisodeFileID != efs[0].ID || eps[2].EpisodeFileID != efs[1].ID {
			t.Errorf("%s: episodeFileIds %d %d %d, file id %d", dir, eps[0].EpisodeFileID, eps[1].EpisodeFileID, eps[2].EpisodeFileID, efs[0].ID)
		}
		if eps[0].SeriesID != s.ID || hist[1].SeriesID != s.ID || eps[0].EpisodeFile.ID != efs[0].ID {
			t.Errorf("%s: a foreign key lost its target", dir)
		}
		if !hist[0].Date.Before(hist[1].Date) || hist[0].Date.Year() != 2001 {
			t.Errorf("%s: history dates %v %v", dir, hist[0].Date, hist[1].Date)
		}
		// Real ids 987654, 12 and 13 keep their order: 12 < 13 < 987654.
		if eps[1].ID >= eps[2].ID || eps[2].ID >= eps[0].ID || s.Tags[0] != 1 {
			t.Errorf("%s: ids not ascending or the tag foreign key not mapped: %d %d %d %v", dir, eps[0].ID, eps[1].ID, eps[2].ID, s.Tags)
		}
	}
}

// A kept field echoing the key, a URL, a hash or an absolute path
// stops the recording before anything is written.
func TestRecordSonarrRefusesKeyAndDeploymentData(t *testing.T) {
	const key arrapi.APIKey = "test.key_5d1c"
	planted := map[string]string{
		"the key":           `"` + string(key) + `"`,
		"a URL":             `"see http://sonarr.invalid:8989/x"`,
		"a 40-hex hash":     `"0123456789abcdef0123456789abcdef01234567"`,
		"an absolute path":  `"/config/sonarr.db"`,
		"the instance name": `"Example Sonarr"`,
		"the host name":     `"sonarr.test"`,
	}
	for name, title := range planted {
		t.Run(name, func(t *testing.T) {
			f4, f5 := sonarr4Library(), sonarr5Library()
			f5.routes["GET /api/v5/series/987654"] = ans(http.StatusOK, v5, `{"id":987654,"title":`+title+`,"tvdbId":79525}`)
			root := t.TempDir()
			got, err := recordSonarr(t.Context(), root, target(f4, key), target(f5, key), recordSince)
			if err == nil || got.files != 0 || len(readFixtures(t, root)) != 0 {
				t.Fatalf("err = %v, n = %d; want a refusal and nothing written", err, got.files)
			}
			if strings.Contains(err.Error(), "5d1c") || strings.Contains(err.Error(), "0123456789abcdef") {
				t.Fatalf("the refusal renders the value: %v", err)
			}
		})
	}
}

// An error answer's message comes from a server exception and its title from whatever
// framed the answer, so both are written as synthetic values on both Sonarr directories.
func TestRecordSonarrReplacesErrorText(t *testing.T) {
	for name, text := range map[string]string{
		"arbitrary text":    "Alice Example stores shows on volume wintermute",
		"a UNC path":        `Could not read \\nas\share\private`,
		"a symbol-led path": "Could not read /🔥Media/private.mkv",
	} {
		t.Run(name, func(t *testing.T) {
			f4, f5 := sonarr4Library(), sonarr5Library()
			msg, err := json.Marshal(text)
			if err != nil {
				t.Fatal(err)
			}
			f4.routes["GET /api/v3/series/99999999"] = ans(http.StatusNotFound, v4, `{"title":`+string(msg)+`}`)
			f5.routes["GET /api/v5/episodefile?seriesId=99999999"] = ans(http.StatusNotFound, v5, `{"message":`+string(msg)+`}`)
			files, err := recordFakes(t, f4, f5)
			if err != nil {
				t.Fatalf("recordSonarr: %v", err)
			}
			for f, body := range files {
				if strings.Contains(body, "private") || strings.Contains(body, "wintermute") || strings.Contains(body, "Could not read") {
					t.Errorf("%s holds the error text", f)
				}
			}
			for f, member := range map[string]string{
				"sonarr/v4-api-v3/series-404.json":                 "title",
				"sonarr/v5-api-v5/episodefile-missing-parent.json": "message",
			} {
				doc := map[string]any{}
				decodeInto(t, files[f], &doc)
				if doc[member] != "error-1" {
					t.Errorf("%s %s = %v, want error-1", f, member, doc[member])
				}
			}
		})
	}
}

func decodeInto(t *testing.T, body string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), out); err != nil {
		t.Fatalf("decode %T: %v", out, err)
	}
}

// A live answer whose declared member its DTO cannot decode stops the recording, so a
// malformed answer never becomes a fixture the engine accepts.
func TestRecordSonarrRefusesAnswersItsDTOsCannotDecode(t *testing.T) {
	for name, tc := range map[string]struct{ route, body, refusal string }{
		"an object eventType": {
			"GET " + sinceQuery(sonarrV5Prefix, recordSince),
			`[{"id":2,"seriesId":5,"date":"2031-07-09T10:00:00Z","eventType":{"leak":"/config/x"}}]`,
			"sonarr/v5-api-v5/history-since.json: member [0].eventType:",
		},
		"a string tvdbId": {
			"GET /api/v5/series/987654", `{"id":987654,"title":"Code Geass","tvdbId":"/config/x"}`,
			"sonarr/v5-api-v5/series-by-id-codegeass.json: member tvdbId:",
		},
		"a number title": {
			"GET /api/v5/series/987654", `{"id":987654,"title":7,"tvdbId":79525}`,
			"sonarr/v5-api-v5/series-by-id-codegeass.json: member title:",
		},
		"an object in the deprecated list": {
			"GET /api", `{"current":"v5","deprecated":["v3",{"leak":"/config/x"}]}`,
			"sonarr/v5-api-v5/api.json: member deprecated[1]:",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f4, f5 := sonarr4Library(), sonarr5Library()
			f5.routes[tc.route] = ans(http.StatusOK, v5, tc.body)
			files, err := recordFakes(t, f4, f5)
			if err == nil || len(files) != 0 {
				t.Fatalf("err = %v, files = %d; want a refusal and nothing written", err, len(files))
			}
			if !strings.Contains(err.Error(), tc.refusal) {
				t.Fatalf("err = %v, want it to name %q", err, tc.refusal)
			}
			if strings.Contains(err.Error(), "/config") {
				t.Fatalf("the refusal renders the value: %v", err)
			}
		})
	}
}

// A file left in testdata/sonarr that the recording does not write, such as a second
// Sonarr v5 (API v5) answer read over the /api/v3 refusal path, stops the recording.
func TestRecordSonarrRefusesStaleFixtures(t *testing.T) {
	for _, name := range []string{"sonarr/v5-api-v3-refusal/series.json", "sonarr/v5-api-v3/series.json"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			stale := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stale, []byte(`[]`), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := recordSonarr(t.Context(), root, target(sonarr4Library(), testKey), target(sonarr5Library(), testKey), recordSince)
			if err == nil || got.files != 0 || !strings.Contains(err.Error(), name+": a stale fixture") {
				t.Fatalf("record = %d files, %v; want the stale file refused", got.files, err)
			}
			if files := readFixtures(t, root); len(files) != 1 {
				t.Fatalf("a refused recording changed the tree: %d files", len(files))
			}
		})
	}
}
