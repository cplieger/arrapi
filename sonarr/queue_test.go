package sonarr

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

// queueBodies holds, per engine prefix, one GET {prefix}/queue/details answer: a
// downloading item with two status message groups, an importBlocked item, a held-back
// release (a null trackedDownloadState and no downloadId), and a download matched to
// no series and no episode. Sonarr 4 writes one row per episode with episodeId, Sonarr 5
// one row per download with episodeIds and spells sizeLeft with a capital L.
var queueBodies = map[string]string{
	sonarrV4Prefix: `[
		{"id":901,"title":"Code.Geass.S01E01.1080p","downloadId":"SABnzbd_nzo_1","downloadClient":"SABnzbd",
			"trackedDownloadState":"downloading","trackedDownloadStatus":"ok","status":"downloading","protocol":"usenet",
			"statusMessages":[{"title":"Code.Geass.S01E01.1080p","messages":["first line","second line"]},{"title":"sample","messages":[]}],
			"seriesId":41,"episodeId":411,"size":1.5e9,"sizeleft":2.5e8},
		{"id":902,"title":"Code.Geass.S01E02","downloadId":"qbit_2","downloadClient":"qBittorrent","trackedDownloadState":"importBlocked",
			"statusMessages":[{"title":"Code.Geass.S01E02.mkv","messages":["Not an upgrade for existing episode file"]}],
			"seriesId":41,"episodeId":412,"size":700000000,"sizeleft":0},
		{"id":903,"title":"Code.Geass.S01E03","trackedDownloadState":null,"statusMessages":[],
			"seriesId":41,"episodeId":413,"size":500000000,"sizeleft":500000000},
		{"id":904,"title":"Unknown.Show.S01E01","downloadId":"SABnzbd_nzo_4","downloadClient":"SABnzbd","trackedDownloadState":"importPending",
			"seriesId":null,"episodeId":null,"size":0,"sizeleft":0}
	]`,
	sonarrV5Prefix: `[
		{"id":901,"title":"Code.Geass.S01E01.1080p","downloadId":"SABnzbd_nzo_1","downloadClient":"SABnzbd",
			"trackedDownloadState":"downloading","trackedDownloadStatus":"ok","status":"downloading","protocol":"usenet",
			"statusMessages":[{"title":"Code.Geass.S01E01.1080p","messages":["first line","second line"]},{"title":"sample","messages":[]}],
			"seriesId":41,"episodeIds":[411],"size":1.5e9,"sizeLeft":2.5e8},
		{"id":902,"title":"Code.Geass.S01E02","downloadId":"qbit_2","downloadClient":"qBittorrent","trackedDownloadState":"importBlocked",
			"statusMessages":[{"title":"Code.Geass.S01E02.mkv","messages":["Not an upgrade for existing episode file"]}],
			"seriesId":41,"episodeIds":[412],"size":700000000,"sizeLeft":0},
		{"id":903,"title":"Code.Geass.S01E03","trackedDownloadState":null,"statusMessages":[],
			"seriesId":41,"episodeIds":[413],"size":500000000,"sizeLeft":500000000},
		{"id":904,"title":"Unknown.Show.S01E01","downloadId":"SABnzbd_nzo_4","downloadClient":"SABnzbd","trackedDownloadState":"importPending",
			"seriesId":null,"episodeIds":[],"size":0,"sizeLeft":0}
	]`,
}

var queueWant = []QueueItem{
	{
		Title: "Code.Geass.S01E01.1080p", DownloadID: "SABnzbd_nzo_1", DownloadClient: "SABnzbd", State: arrapi.DownloadDownloading,
		Messages: []arrapi.StatusMessage{
			{Title: "Code.Geass.S01E01.1080p", Messages: []string{"first line", "second line"}},
			{Title: "sample", Messages: []string{}},
		},
		EpisodeIDs: []int{411}, SeriesID: 41, Size: 1_500_000_000, SizeLeft: 250_000_000,
	},
	{
		Title: "Code.Geass.S01E02", DownloadID: "qbit_2", DownloadClient: "qBittorrent", State: arrapi.DownloadImportBlocked,
		Messages:   []arrapi.StatusMessage{{Title: "Code.Geass.S01E02.mkv", Messages: []string{"Not an upgrade for existing episode file"}}},
		EpisodeIDs: []int{412}, SeriesID: 41, Size: 700_000_000,
	},
	{Title: "Code.Geass.S01E03", EpisodeIDs: []int{413}, SeriesID: 41, Size: 500_000_000, SizeLeft: 500_000_000},
	{Title: "Unknown.Show.S01E01", DownloadID: "SABnzbd_nzo_4", DownloadClient: "SABnzbd", State: arrapi.DownloadImportPending},
}

func wantQueue(t *testing.T, got, want []QueueItem) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Queue =\n%#v\nwant\n%#v", got, want)
	}
}

func TestQueueReadsTheDetails(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			f.on("GET "+ec.prefix+"/queue/details", ans(200, ec.version, queueBodies[ec.prefix]))
			got, err := c.Queue(t.Context())
			if err != nil {
				t.Fatalf("Queue: %v", err)
			}
			wantQueue(t, got, queueWant)
			wantURIs(t, f, "GET "+ec.prefix+"/queue/details")
		})
	}
}

// A season pack is three rows sharing one downloadId on Sonarr 4 and one row listing
// three episodes on Sonarr 5; both read as one item.
func TestQueueReadsASeasonPackAsOneItem(t *testing.T) {
	bodies := map[string]string{
		sonarrV4Prefix: `[
			{"title":"Code.Geass.S02","downloadId":"qbit_9","downloadClient":"qBittorrent","trackedDownloadState":"downloading","seriesId":41,"episodeId":421,"size":3e9,"sizeleft":1e9},
			{"title":"Code.Geass.S02","downloadId":"qbit_9","downloadClient":"qBittorrent","trackedDownloadState":"downloading","seriesId":41,"episodeId":422,"size":3e9,"sizeleft":1e9},
			{"title":"Code.Geass.S02","downloadId":"qbit_9","downloadClient":"qBittorrent","trackedDownloadState":"downloading","seriesId":41,"episodeId":423,"size":3e9,"sizeleft":1e9}
		]`,
		sonarrV5Prefix: `[
			{"title":"Code.Geass.S02","downloadId":"qbit_9","downloadClient":"qBittorrent","trackedDownloadState":"downloading","seriesId":41,"episodeIds":[421,422,423],"size":3e9,"sizeLeft":1e9}
		]`,
	}
	want := []QueueItem{{
		Title: "Code.Geass.S02", DownloadID: "qbit_9", DownloadClient: "qBittorrent", State: arrapi.DownloadDownloading,
		EpisodeIDs: []int{421, 422, 423}, SeriesID: 41, Size: 3_000_000_000, SizeLeft: 1_000_000_000,
	}}
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			f.on("GET "+ec.prefix+"/queue/details", ans(200, ec.version, bodies[ec.prefix]))
			got, err := c.Queue(t.Context())
			if err != nil {
				t.Fatalf("Queue: %v", err)
			}
			wantQueue(t, got, want)
		})
	}
}

// sizeLeftKey is the member each engine's QueueResource spells the bytes left with.
func sizeLeftKey(prefix string) string {
	if prefix == sonarrV5Prefix {
		return "sizeLeft"
	}
	return "sizeleft"
}

func TestQueueClampsTheSizes(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			left := sizeLeftKey(ec.prefix)
			f.on("GET "+ec.prefix+"/queue/details", ans(200, ec.version,
				`[{"downloadId":"a","size":-1,"`+left+`":-1},{"downloadId":"b","size":1e20,"`+left+`":1e308},{"downloadId":"c","size":0.5,"`+left+`":0.4}]`))
			got, err := c.Queue(t.Context())
			if err != nil {
				t.Fatalf("Queue: %v", err)
			}
			want := [][2]int64{{0, 0}, {math.MaxInt64, math.MaxInt64}, {1, 0}}
			if len(got) != len(want) {
				t.Fatalf("Queue returned %d items, want %d", len(got), len(want))
			}
			for i, q := range got {
				if q.Size != want[i][0] || q.SizeLeft != want[i][1] {
					t.Errorf("item %s Size, SizeLeft = %d, %d, want %d, %d", q.DownloadID, q.Size, q.SizeLeft, want[i][0], want[i][1])
				}
			}
		})
	}
}

// A state outside the enum is written as a number (STJson.cs registers
// JsonStringEnumConverter with allowIntegerValues), so it reads "" without failing the
// list; a kind no server writes fails it with the field named.
func TestQueueStateOverTheWire(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			f.on("GET "+ec.prefix+"/queue/details", ans(200, ec.version,
				`[{"downloadId":"a","trackedDownloadState":"paused"},{"downloadId":"b","trackedDownloadState":7},{"downloadId":"c","trackedDownloadState":"failedPending"},{"downloadId":"d"}]`))
			got, err := c.Queue(t.Context())
			if err != nil {
				t.Fatalf("Queue: %v", err)
			}
			want := []arrapi.DownloadState{"", "", arrapi.DownloadFailedPending, ""}
			if len(got) != len(want) {
				t.Fatalf("Queue returned %d items, want %d", len(got), len(want))
			}
			for i, q := range got {
				if q.State != want[i] {
					t.Errorf("item %s State = %q, want %q", q.DownloadID, q.State, want[i])
				}
			}

			f.on("GET "+ec.prefix+"/queue/details", ans(200, ec.version, `[{"trackedDownloadState":true}]`))
			_, err = c.Queue(t.Context())
			if want := "arrapi: decode " + ec.prefix + "/queue/details: field trackedDownloadState: a JSON bool cannot decode into arrapi.DownloadState"; err == nil || err.Error() != want {
				t.Errorf("Queue over a bool state = %v, want %q", err, want)
			}
		})
	}
}

func TestQueueRefusesANullItem(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			bodies := map[string]string{
				`[{"downloadId":"a"},null]`:                                  "list item 1 is null",
				`[{"statusMessages":[null]}]`:                                "statusMessages",
				`[{"statusMessages":[{"title":"x","messages":["y",null]}]}]`: "statusMessages.messages",
				`[{"size":"big"}]`:                                           "size",
			}
			if ec.prefix == sonarrV5Prefix {
				bodies[`[{"episodeIds":[411,null]}]`] = "episodeIds"
			}
			for body, want := range bodies {
				f.on("GET "+ec.prefix+"/queue/details", ans(200, ec.version, body))
				_, err := c.Queue(t.Context())
				if _, ok := errors.AsType[*arrapi.DecodeError](err); !ok || !strings.Contains(err.Error(), want) {
					t.Errorf("Queue over %s = %v, want a *DecodeError naming %q", body, err, want)
				}
			}
		})
	}
}

func TestPinnedQueue(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	p := pin(t, c)
	f.on("GET /api/v3/queue/details", ans(200, v4, queueBodies[sonarrV4Prefix]))
	got, err := p.Queue(t.Context())
	if err != nil {
		t.Fatalf("Pinned.Queue: %v", err)
	}
	wantQueue(t, got, queueWant)

	const upgraded = "4.0.21.1"
	f.clearLog()
	f.sonarr4(upgraded)
	f.on("GET /api/v3/queue/details", ans(200, upgraded, queueBodies[sonarrV4Prefix]))
	got, err = p.Queue(t.Context())
	wantChangedTo(t, err, upgraded)
	if got != nil {
		t.Errorf("Pinned.Queue = %+v, want nothing", got)
	}
}

func TestQueueBytes(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want int64
	}{
		{math.NaN(), 0},
		{math.Inf(-1), 0},
		{-1, 0},
		{0, 0},
		{0.4, 0},
		{0.5, 1},
		{1.5e9, 1_500_000_000},
		{1<<53 + 2, 1<<53 + 2},
		{float64(math.MaxInt64), math.MaxInt64},
		{1e20, math.MaxInt64},
		{1e308, math.MaxInt64},
		{math.Inf(1), math.MaxInt64},
	} {
		if got := queueBytes(tc.in); got != tc.want {
			t.Errorf("queueBytes(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
