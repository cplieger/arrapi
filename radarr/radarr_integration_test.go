//go:build integration

package radarr

import (
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
)

const absentMovieID = 99999999

// TestIntegrationRadarrAPIv3 runs every read method of radarr.Client against the live
// Radarr named by ARRAPI_RADARR_URL, GET only.
func TestIntegrationRadarrAPIv3(t *testing.T) {
	u, key := os.Getenv("ARRAPI_RADARR_URL"), os.Getenv("ARRAPI_RADARR_KEY")
	if u == "" || key == "" {
		t.Skip("set ARRAPI_RADARR_URL and ARRAPI_RADARR_KEY to run against a live Radarr")
	}
	c, err := New(u, arrapi.APIKey(key))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	check := func(what string, err error) {
		t.Helper()
		if err == nil {
			return
		}
		if strings.Contains(err.Error(), key) || strings.Contains(fmt.Sprintf("%+v", err), key) {
			t.Fatalf("%s: the error text carries the API key", what)
		}
		t.Fatalf("%s: %v", what, err)
	}
	ctx := t.Context()

	info, err := c.Info(ctx)
	check("Info", err)
	if info.API != arrapi.APIv3 || info.Version == "" {
		t.Fatalf("Info = %+v, want Radarr (API v3) with a version", info)
	}

	movies, err := c.Movies(ctx)
	check("Movies", err)
	i := slices.IndexFunc(movies, func(m Movie) bool { return m.HasFile })
	if i < 0 {
		t.Fatalf("Movies lists %d movies, none with a file", len(movies))
	}
	m := movies[i]
	if m.File == nil || m.File.ID <= 0 || m.File.Size <= 0 || !strings.HasSuffix(m.File.Path, m.File.RelativePath) {
		t.Errorf("movie %d: File = %+v, want an id, a size and a path ending in its relative path", m.ID, m.File)
	}
	// hasFile is MovieFileCount > 0, a count of MovieFileId > 0, and movieFile the row
	// MovieFileId joins (Radarr develop MovieController.cs:309,
	// MovieStatisticsRepository.cs:67, MovieRepository.cs:105).
	for _, mv := range movies {
		if (mv.File != nil) != mv.HasFile {
			t.Errorf("movie %d: HasFile %v, File %v; want File set exactly when HasFile", mv.ID, mv.HasFile, mv.File)
		}
	}
	byID, found, err := c.MovieByID(ctx, m.ID)
	check("MovieByID", err)
	if !found || byID.ID != m.ID || byID.Title != m.Title || byID.IDs != m.IDs {
		t.Errorf("MovieByID(%d) = (%+v, %v), want the listed movie found", m.ID, byID, found)
	}
	absent, found, err := c.MovieByID(ctx, absentMovieID)
	check("MovieByID absent", err)
	if found || absent.ID != 0 {
		t.Errorf("MovieByID(%d) = (%+v, %v), want (zero, false)", absentMovieID, absent, found)
	}

	since := time.Now().Add(-7 * 24 * time.Hour)
	recs, err := c.HistorySince(ctx, since)
	check("HistorySince", err)
	if !slices.IsSortedFunc(recs, func(a, b HistoryRecord) int { return a.Date.Compare(b.Date) }) {
		t.Error("HistorySince is not oldest first")
	}
	for _, r := range recs {
		if r.Date.Before(since.Truncate(time.Second)) {
			t.Errorf("history record %d dated %v, before %v", r.ID, r.Date, since)
		}
	}
	t.Logf("history: %d records in 7 days", len(recs))

	tags, err := c.Tags(ctx)
	check("Tags", err)
	for _, tag := range tags {
		if tag.ID <= 0 {
			t.Errorf("tag %+v has no id", tag)
		}
	}
	roots, err := c.RootFolders(ctx)
	check("RootFolders", err)
	if len(roots) == 0 || slices.ContainsFunc(roots, func(p string) bool { return !strings.HasPrefix(p, "/") }) {
		t.Errorf("RootFolders = %d paths, want absolute paths", len(roots))
	}
	cover, err := c.MediaCoverURL(ctx, m.ID, "poster.jpg")
	check("MediaCoverURL", err)
	if want := strings.TrimSuffix(u, "/") + "/api/v3/mediacover/" + strconv.Itoa(m.ID) + "/poster.jpg"; cover != want {
		t.Errorf("MediaCoverURL = %q, want %q", cover, want)
	}
	img, found, err := c.MediaCover(ctx, m.ID, "poster-250.jpg", 2<<20)
	check("MediaCover", err)
	if !found || img.ContentType != "image/jpeg" || len(img.Data) == 0 {
		t.Errorf("MediaCover(%d, poster-250.jpg) = %q, %d bytes, found %v; want a JPEG found", m.ID, img.ContentType, len(img.Data), found)
	}
	none, found, err := c.MediaCover(ctx, absentMovieID, "poster.jpg", 2<<20)
	check("MediaCover absent", err)
	if found || none.ContentType != "" || none.Data != nil {
		t.Errorf("MediaCover(%d, poster.jpg) = %q, %d bytes, found %v; want (zero, false)", absentMovieID, none.ContentType, len(none.Data), found)
	}

	cmds, err := c.Commands(ctx)
	check("Commands", err)
	for _, cmd := range cmds {
		if cmd.ID <= 0 || cmd.Name == "" {
			t.Errorf("command %+v has no id or name", cmd)
		}
	}
	t.Logf("commands: %d held", len(cmds))
	queue, err := c.Queue(ctx)
	check("Queue", err)
	for i, q := range queue {
		if q.Size < 0 || q.SizeLeft < 0 {
			t.Errorf("queue item %d has a negative size: %d, %d left", i, q.Size, q.SizeLeft)
		}
	}
	t.Logf("queue: %d items", len(queue))

	// Check with the right key only: repeated 401s from one address can trip a ban in a
	// proxy in front of the server.
	viaCheck, viaInfo := newRequestLog(t), newRequestLog(t)
	nc, err := New(u, arrapi.APIKey(key), WithHTTPClient(&http.Client{Transport: viaInfo}))
	check("New", err)
	t.Cleanup(nc.Close)
	want, err := nc.Info(ctx)
	check("Info", err)
	got, err := Check(ctx, u, arrapi.APIKey(key), WithHTTPClient(&http.Client{Transport: viaCheck}))
	check("Check", err)
	if got != want {
		t.Errorf("Check = %+v, want Info's %+v", got, want)
	}
	if sent := viaCheck.requests(); !slices.Equal(sent, viaInfo.requests()) || slices.ContainsFunc(sent, func(r string) bool { return !strings.HasPrefix(r, "GET ") }) {
		t.Errorf("Check sent %q, want Info's GETs %q", sent, viaInfo.requests())
	}
}

// requestLog records each request's method and path before sending it on.
type requestLog struct {
	base http.RoundTripper
	mu   sync.Mutex
	sent []string
}

func (r *requestLog) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.sent = append(r.sent, req.Method+" "+req.URL.Path)
	r.mu.Unlock()
	return r.base.RoundTrip(req)
}

func (r *requestLog) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.sent)
}

func newRequestLog(t *testing.T) *requestLog {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(tr.CloseIdleConnections)
	return &requestLog{base: tr}
}
