//go:build integration

package sonarr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
)

// The live suite reads the two Sonarr servers GET only: ARRAPI_SONARR_V4_URL is the
// Sonarr 4.0 container, the only Sonarr v4 (API v3) target; ARRAPI_SONARR_V5_URL is a
// Sonarr 5.0, read only over /api/v5 except by TestIntegrationSonarrV5OverAPIv3Refused.

// liveSonarr is one live server: its client, URL and key, the key kept only to prove
// it reaches no error text.
type liveSonarr struct {
	c   *Client
	url string
	key string
}

// sonarrLive returns the server named by prefix, skipping the test when the pair is
// not set.
func sonarrLive(t *testing.T, prefix string) liveSonarr {
	t.Helper()
	u, key := os.Getenv(prefix+"_URL"), os.Getenv(prefix+"_KEY")
	if u == "" || key == "" {
		t.Skipf("set %s_URL and %s_KEY to run against a live server", prefix, prefix)
	}
	c, err := New(u, arrapi.APIKey(key))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	return liveSonarr{c: c, url: strings.TrimSuffix(u, "/"), key: key}
}

// check fails the test on err, and on a key found in its text or %+v rendering.
func (l liveSonarr) check(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), l.key) || strings.Contains(fmt.Sprintf("%+v", err), l.key) {
		t.Fatalf("%s: the error text carries the API key", what)
	}
	t.Fatalf("%s: %v", what, err)
}

// wantShape fails before any read unless Info reports api at major 4 exactly, or for
// API v5 at major 5 or later, so a swapped variable pair never reads a Sonarr 5.0's
// /api/v3.
func (l liveSonarr) wantShape(t *testing.T, env string, api arrapi.API, major int, label string) Info {
	t.Helper()
	info, err := l.c.Info(t.Context())
	l.check(t, "Info", err)
	got, _, _ := strings.Cut(info.Version, ".")
	n, err := strconv.Atoi(got)
	if info.API != api || err != nil || n < major || (major == 4 && n != 4) {
		t.Fatalf("%s answered Sonarr %s (API %s): the %s tests run only against a Sonarr %d.x", env, info.Version, info.API, label, major)
	}
	return info
}

func (l liveSonarr) seriesByTVDB(t *testing.T, list []Series, tvdb int) Series {
	t.Helper()
	for _, s := range list {
		if s.IDs.TVDB == tvdb {
			return s
		}
	}
	t.Fatalf("%s lists no series with TVDB id %d", l.url, tvdb)
	return Series{}
}

func TestIntegrationSonarrV4APIv3(t *testing.T) {
	l := sonarrLive(t, "ARRAPI_SONARR_V4")
	info := l.wantShape(t, "ARRAPI_SONARR_V4_URL", arrapi.APIv3, 4, "Sonarr v4 (API v3)")
	if want := (Features{}); info.Features != want {
		t.Errorf("Features = %+v, want %+v", info.Features, want)
	}
	readEverything(t, l, info, "/api/v3")
}

func TestIntegrationSonarrV5APIv5(t *testing.T) {
	l := sonarrLive(t, "ARRAPI_SONARR_V5")
	info := l.wantShape(t, "ARRAPI_SONARR_V5_URL", arrapi.APIv5, 5, "Sonarr v5 (API v5)")
	if want := (Features{SeriesOrders: true, SeasonTitles: true, Translations: true, AnimeIDs: true}); info.Features != want {
		t.Errorf("Features = %+v, want %+v", info.Features, want)
	}
	readEverything(t, l, info, "/api/v5")
}

// reads is the read surface Client and Pinned share.
type reads interface {
	Series(ctx context.Context) ([]Series, error)
	SeriesByID(ctx context.Context, id int) (Series, bool, error)
	Episodes(ctx context.Context, seriesID int) ([]Episode, error)
	EpisodesWithFiles(ctx context.Context, seriesID int) ([]Episode, error)
	EpisodeFiles(ctx context.Context, seriesID int) ([]EpisodeFile, error)
	HistorySince(ctx context.Context, since time.Time, types ...EventType) ([]HistoryRecord, error)
	Tags(ctx context.Context) ([]arrapi.Tag, error)
	RootFolders(ctx context.Context) ([]string, error)
}

// readEverything runs every read method of the client, and of a Pin of it reporting
// info, against Code Geass, then MediaCoverURL.
func readEverything(t *testing.T, l liveSonarr, info Info, prefix string) {
	t.Run("Client", func(t *testing.T) { readAll(t, l, l.c) })
	t.Run("Pinned", func(t *testing.T) {
		p, err := l.c.Pin(t.Context())
		l.check(t, "Pin", err)
		if got := p.Info(); got != info {
			t.Errorf("Pinned.Info = %+v, want Info's %+v", got, info)
		}
		readAll(t, l, p)
	})
	list, err := l.c.Series(t.Context())
	l.check(t, "Series", err)
	cg := l.seriesByTVDB(t, list, tvdbCodeGeass)
	cover, err := l.c.MediaCoverURL(t.Context(), cg.ID, "poster.jpg")
	l.check(t, "MediaCoverURL", err)
	if want := l.url + prefix + "/mediacover/" + strconv.Itoa(cg.ID) + "/poster.jpg"; cover != want {
		t.Errorf("MediaCoverURL = %q, want %q", cover, want)
	}
}

func readAll(t *testing.T, l liveSonarr, r reads) {
	ctx := t.Context()
	list, err := r.Series(ctx)
	l.check(t, "Series", err)
	cg := l.seriesByTVDB(t, list, tvdbCodeGeass)
	if !cg.Order.Official || cg.Order.Type != "official" {
		t.Errorf("Code Geass Order = %+v, want official", cg.Order)
	}

	byID, found, err := r.SeriesByID(ctx, cg.ID)
	l.check(t, "SeriesByID", err)
	if !found || byID.ID != cg.ID || byID.IDs.TVDB != tvdbCodeGeass || byID.Title != cg.Title {
		t.Errorf("SeriesByID(%d) = (%+v, %v), want Code Geass found", cg.ID, byID, found)
	}
	absent, found, err := r.SeriesByID(ctx, absentSeriesID)
	l.check(t, "SeriesByID absent", err)
	if found || absent.ID != 0 {
		t.Errorf("SeriesByID(%d) = (%+v, %v), want (zero, false)", absentSeriesID, absent, found)
	}

	plain, err := r.Episodes(ctx, cg.ID)
	l.check(t, "Episodes", err)
	with, err := r.EpisodesWithFiles(ctx, cg.ID)
	l.check(t, "EpisodesWithFiles", err)
	files, err := r.EpisodeFiles(ctx, cg.ID)
	l.check(t, "EpisodeFiles", err)
	if len(plain) == 0 || len(plain) != len(with) {
		t.Fatalf("Episodes %d, EpisodesWithFiles %d; want the same non-zero count", len(plain), len(with))
	}
	// Sonarr's episodeFileCount counts episodes with a file, a multi-episode file once
	// per episode (v4-develop SeriesStatisticsRepository.cs:81, develop :83).
	withFile := 0
	for _, e := range plain {
		if e.HasFile {
			withFile++
		}
	}
	if len(files) == 0 || cg.FileCount != withFile || len(files) > cg.FileCount {
		t.Errorf("EpisodeFiles = %d files, Series.FileCount %d, %d episodes with a file; want FileCount equal to those episodes and 1 to FileCount files", len(files), cg.FileCount, withFile)
	}
	fileIDs := map[int]bool{}
	for _, f := range files {
		fileIDs[f.ID] = true
		if f.ID <= 0 || f.Size <= 0 || !strings.HasSuffix(f.Path, f.RelativePath) || f.RelativePath == "" {
			t.Errorf("EpisodeFile %+v: want an id, a size and a path ending in its relative path", f)
		}
	}
	for _, e := range plain {
		if e.File != nil || e.SeriesID != cg.ID {
			t.Errorf("Episodes: episode %d has File %v and series %d, want nil and %d", e.ID, e.File, e.SeriesID, cg.ID)
		}
		if e.HasFile != (e.FileID != 0) || (e.HasFile && !fileIDs[e.FileID]) {
			t.Errorf("Episodes: episode %d HasFile %v FileID %d, want a FileID of the series' files exactly when HasFile", e.ID, e.HasFile, e.FileID)
		}
	}
	for _, e := range with {
		if (e.File != nil) != e.HasFile || (e.File != nil && e.File.ID != e.FileID) {
			t.Errorf("EpisodesWithFiles: episode %d HasFile %v File %v FileID %d, want File set exactly when HasFile", e.ID, e.HasFile, e.File, e.FileID)
		}
	}

	since := time.Now().Add(-24 * time.Hour)
	all, err := r.HistorySince(ctx, since)
	l.check(t, "HistorySince", err)
	if !slices.IsSortedFunc(all, func(a, b HistoryRecord) int { return a.Date.Compare(b.Date) }) {
		t.Error("HistorySince is not oldest first")
	}
	kinds := []EventType{EventFolderImported, EventDownloadImported, EventFileDeleted, EventFileRenamed}
	some, err := r.HistorySince(ctx, since, kinds...)
	l.check(t, "HistorySince filtered", err)
	for _, r := range slices.Concat(all, some) {
		if r.Date.Before(since.Truncate(time.Second)) {
			t.Errorf("history record %d dated %v, before %v", r.ID, r.Date, since)
		}
	}
	for _, r := range some {
		if !slices.Contains(kinds, r.EventType) {
			t.Errorf("filtered history record %d is %v", r.ID, r.EventType)
		}
	}
	t.Logf("history: %d records in 24 h, %d of the four import and file kinds", len(all), len(some))

	tags, err := r.Tags(ctx)
	l.check(t, "Tags", err)
	for _, tag := range tags {
		if tag.ID <= 0 {
			t.Errorf("tag %+v has no id", tag)
		}
	}
	roots, err := r.RootFolders(ctx)
	l.check(t, "RootFolders", err)
	if len(roots) == 0 || slices.ContainsFunc(roots, func(p string) bool { return !strings.HasPrefix(p, "/") }) {
		t.Errorf("RootFolders = %d paths, want absolute paths", len(roots))
	}

}

// liveCodeGeass reads what the engine of l reads of Code Geass, live.
func liveCodeGeass(t *testing.T, l liveSonarr) codeGeass {
	t.Helper()
	list, err := l.c.Series(t.Context())
	l.check(t, "Series", err)
	cg := codeGeass{series: l.seriesByTVDB(t, list, tvdbCodeGeass)}
	cg.episodes, err = l.c.EpisodesWithFiles(t.Context(), cg.series.ID)
	l.check(t, "EpisodesWithFiles", err)
	cg.files, err = l.c.EpisodeFiles(t.Context(), cg.series.ID)
	l.check(t, "EpisodeFiles", err)
	return cg
}

// TestIntegrationEngineEquivalenceCodeGeass runs TestEngineEquivalenceCodeGeass's
// oracle on the live servers: Code Geass, in official order on both, reads the same
// through either engine.
func TestIntegrationEngineEquivalenceCodeGeass(t *testing.T) {
	v4 := sonarrLive(t, "ARRAPI_SONARR_V4")
	v5 := sonarrLive(t, "ARRAPI_SONARR_V5")
	v4.wantShape(t, "ARRAPI_SONARR_V4_URL", arrapi.APIv3, 4, "Sonarr v4 (API v3)")
	v5.wantShape(t, "ARRAPI_SONARR_V5_URL", arrapi.APIv5, 5, "Sonarr v5 (API v5)")
	a, b := liveCodeGeass(t, v4), liveCodeGeass(t, v5)
	for _, d := range a.differences(b) {
		t.Error(d)
	}
	t.Logf("compared %d episodes and %d files", len(a.episodes), len(a.files))
}

// TestIntegrationSonarrV5Orders reads the five order series live; each value is the
// one recorded in testdata/sonarr/v5-api-v5 and pinned by order_test.go.
func TestIntegrationSonarrV5Orders(t *testing.T) {
	l := sonarrLive(t, "ARRAPI_SONARR_V5")
	l.wantShape(t, "ARRAPI_SONARR_V5_URL", arrapi.APIv5, 5, "Sonarr v5 (API v5)")
	list, err := l.c.Series(t.Context())
	l.check(t, "Series", err)
	for _, o := range []struct {
		name string
		tvdb int
		want Order
	}{
		{"Firefly", tvdbFirefly, Order{Type: "dvd", Name: "DVD Order"}},
		{"The Office", 73244, Order{Type: "alttwo", Name: "Superfan Order"}},
		{"Monogatari", 102261, Order{Type: "alternate", Name: "Book Order"}},
		{"Haruhi", 79414, Order{Type: "alternate", Name: "2009 Series"}},
		{"Star Wars Resistance", 351575, Order{Type: "alternate", Name: "Disney+ Order"}},
	} {
		if got := l.seriesByTVDB(t, list, o.tvdb).Order; got != o.want {
			t.Errorf("%s: Order = %+v, want %+v", o.name, got, o.want)
		}
	}
}

// TestIntegrationSonarrV5EpisodeTVDBIDs: SkyHook v2 fills every episode's tvdbId since
// Sonarr#9160 was fixed on its side, so a series Sonarr 5 refreshed since then reads a
// non-zero Episode.TVDBID on every episode; one not refreshed still reads 0 (unknown).
// The test reads series, Code Geass first, until one reads every id.
func TestIntegrationSonarrV5EpisodeTVDBIDs(t *testing.T) {
	l := sonarrLive(t, "ARRAPI_SONARR_V5")
	l.wantShape(t, "ARRAPI_SONARR_V5_URL", arrapi.APIv5, 5, "Sonarr v5 (API v5)")
	list, err := l.c.Series(t.Context())
	l.check(t, "Series", err)
	if i := slices.IndexFunc(list, func(s Series) bool { return s.IDs.TVDB == tvdbCodeGeass }); i > 0 {
		list[0], list[i] = list[i], list[0]
	}
	const maxSeries = 50
	unrefreshed, mixed := 0, 0
	for _, s := range list[:min(len(list), maxSeries)] {
		eps, err := l.c.Episodes(t.Context(), s.ID)
		l.check(t, "Episodes", err)
		known := 0
		for _, e := range eps {
			if e.TVDBID != 0 {
				known++
			}
		}
		switch {
		case len(eps) > 0 && known == len(eps):
			t.Logf("TVDB %d: %d of %d episodes carry a TVDB id, after %d series reading none and %d reading some", s.IDs.TVDB, known, len(eps), unrefreshed, mixed)
			return
		case known == 0:
			unrefreshed++
		default:
			mixed++
		}
	}
	t.Fatalf("no series of the first %d reads a TVDB id on every episode (%d read none, %d some)", maxSeries, unrefreshed, mixed)
}

// TestIntegrationSonarrV5OverAPIv3Refused puts a proxy in front of the Sonarr 5.0 that
// hides /api and every /api/v5 path and forwards only GET /api/v3/system/status, so
// no other /api/v3 path of it is reached; Info must refuse it (requirement 3).
func TestIntegrationSonarrV5OverAPIv3Refused(t *testing.T) {
	l := sonarrLive(t, "ARRAPI_SONARR_V5")
	l.wantShape(t, "ARRAPI_SONARR_V5_URL", arrapi.APIv5, 5, "Sonarr v5 (API v5) refusal")
	target, err := url.Parse(l.url)
	if err != nil {
		t.Fatal(err)
	}
	forward := httputil.NewSingleHostReverseProxy(target)
	var (
		mu  sync.Mutex
		log []string
	)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		log = append(log, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/v5/"):
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/system/status":
			forward.ServeHTTP(w, r)
		default:
			http.Error(w, "forbidden by the test proxy", http.StatusForbidden)
		}
	}))
	t.Cleanup(proxy.Close)
	c, err := New(proxy.URL, arrapi.APIKey(l.key))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	_, err = c.Info(t.Context())
	if err != nil && strings.Contains(fmt.Sprintf("%v %+v", err, err), l.key) {
		t.Fatal("the refusal's text carries the API key")
	}
	if e, ok := errors.AsType[*arrapi.UnsupportedAPIError](err); !ok || e.Reason != arrapi.ReasonV5Unreachable {
		t.Fatalf("Info = %v, want *arrapi.UnsupportedAPIError with ReasonV5Unreachable", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"GET /api", "GET /api/v5/system/status", "GET /api/v3/system/status"}; !slices.Equal(log, want) {
		t.Errorf("proxy saw %q, want %q", log, want)
	}
}
