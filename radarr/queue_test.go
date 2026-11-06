package radarr

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

// queueBody is one GET /api/v3/queue/details answer: a downloading item with two
// status message groups, an importBlocked item, a held-back release (a null
// trackedDownloadState and no downloadId), and a download matched to no movie.
const queueBody = `[
	{"id":901,"title":"Serenity.2005.1080p","downloadId":"SABnzbd_nzo_1","downloadClient":"SABnzbd",
		"trackedDownloadState":"downloading","trackedDownloadStatus":"ok","status":"downloading","protocol":"usenet",
		"statusMessages":[{"title":"Serenity.2005.1080p","messages":["first line","second line"]},{"title":"sample","messages":[]}],
		"movieId":41,"size":1.5e9,"sizeleft":2.5e8},
	{"id":902,"title":"Primer.2004","downloadId":"qbit_2","downloadClient":"qBittorrent","trackedDownloadState":"importBlocked",
		"statusMessages":[{"title":"Primer.2004.mkv","messages":["Not an upgrade for existing movie file"]}],
		"movieId":42,"size":700000000,"sizeleft":0},
	{"id":903,"title":"Moon.2009","trackedDownloadState":null,"statusMessages":[],"movieId":43,"size":500000000,"sizeleft":500000000},
	{"id":904,"title":"Unknown.Movie.2001","downloadId":"SABnzbd_nzo_4","downloadClient":"SABnzbd","trackedDownloadState":"importPending",
		"movieId":null,"size":0,"sizeleft":0}
]`

var queueWant = []QueueItem{
	{
		Title: "Serenity.2005.1080p", DownloadID: "SABnzbd_nzo_1", DownloadClient: "SABnzbd", State: arrapi.DownloadDownloading,
		Messages: []arrapi.StatusMessage{
			{Title: "Serenity.2005.1080p", Messages: []string{"first line", "second line"}},
			{Title: "sample", Messages: []string{}},
		},
		MovieID: 41, Size: 1_500_000_000, SizeLeft: 250_000_000,
	},
	{
		Title: "Primer.2004", DownloadID: "qbit_2", DownloadClient: "qBittorrent", State: arrapi.DownloadImportBlocked,
		Messages: []arrapi.StatusMessage{{Title: "Primer.2004.mkv", Messages: []string{"Not an upgrade for existing movie file"}}},
		MovieID:  42, Size: 700_000_000,
	},
	{Title: "Moon.2009", MovieID: 43, Size: 500_000_000, SizeLeft: 500_000_000},
	{Title: "Unknown.Movie.2001", DownloadID: "SABnzbd_nzo_4", DownloadClient: "SABnzbd", State: arrapi.DownloadImportPending},
}

func TestQueueReadsTheDetails(t *testing.T) {
	c, f := commandClient(t)
	f.on("GET /api/v3/queue/details", ans(200, vRadarr, queueBody))
	got, err := c.Queue(t.Context())
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if !reflect.DeepEqual(got, queueWant) {
		t.Errorf("Queue =\n%#v\nwant\n%#v", got, queueWant)
	}
	wantURIs(t, f, "GET /api/v3/queue/details")
}

func TestQueueClampsTheSizes(t *testing.T) {
	c, f := commandClient(t)
	f.on("GET /api/v3/queue/details", ans(200, vRadarr,
		`[{"downloadId":"a","size":-1,"sizeleft":-1},{"downloadId":"b","size":1e20,"sizeleft":1e308},{"downloadId":"c","size":0.5,"sizeleft":0.4}]`))
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
}

// A state outside the enum is written as a number (STJson.cs registers
// JsonStringEnumConverter with allowIntegerValues), so it reads "" without failing the
// list; a kind no server writes fails it with the field named.
func TestQueueStateOverTheWire(t *testing.T) {
	c, f := commandClient(t)
	f.on("GET /api/v3/queue/details", ans(200, vRadarr,
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
	f.on("GET /api/v3/queue/details", ans(200, vRadarr, `[{"trackedDownloadState":true}]`))
	_, err = c.Queue(t.Context())
	if want := "arrapi: decode /api/v3/queue/details: field trackedDownloadState: a JSON bool cannot decode into arrapi.DownloadState"; err == nil || err.Error() != want {
		t.Errorf("Queue over a bool state = %v, want %q", err, want)
	}
}

func TestQueueRefusesANullItem(t *testing.T) {
	c, f := commandClient(t)
	for body, want := range map[string]string{
		`[{"downloadId":"a"},null]`:                                  "list item 1 is null",
		`[{"statusMessages":[null]}]`:                                "statusMessages",
		`[{"statusMessages":[{"title":"x","messages":["y",null]}]}]`: "statusMessages.messages",
		`[{"size":"big"}]`:                                           "size",
	} {
		f.on("GET /api/v3/queue/details", ans(200, vRadarr, body))
		_, err := c.Queue(t.Context())
		if _, ok := errors.AsType[*arrapi.DecodeError](err); !ok || !strings.Contains(err.Error(), want) {
			t.Errorf("Queue over %s = %v, want a *DecodeError naming %q", body, err, want)
		}
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
