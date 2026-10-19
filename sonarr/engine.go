package sonarr

import (
	"context"
	"strconv"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// engine reads one Sonarr wire generation. Each request method sends one attempt on
// the detection it is handed and returns that attempt's Result; its value is set only
// when Decide accepted the answer and it decoded.
type engine interface {
	features() Features
	series(ctx context.Context, d *transport.Detection) ([]Series, transport.Result)
	seriesByID(ctx context.Context, d *transport.Detection, id int) (Series, transport.Result)
	episodes(ctx context.Context, d *transport.Detection, seriesID int, withFiles bool) ([]Episode, transport.Result)
	episodeFiles(ctx context.Context, d *transport.Detection, seriesID int) ([]EpisodeFile, transport.Result)
	historySince(ctx context.Context, d *transport.Detection, since time.Time) ([]HistoryRecord, transport.Result)
	tags(ctx context.Context, d *transport.Detection) ([]arrapi.Tag, transport.Result)
	rootFolders(ctx context.Context, d *transport.Detection) ([]string, transport.Result)
	rescan(ctx context.Context, d *transport.Detection, seriesID int) transport.Result
	mediaCoverPath(id int, file string) string
}

func seriesQuery(prefix, resource string, seriesID int) string {
	return prefix + "/" + resource + "?seriesId=" + strconv.Itoa(seriesID)
}

func sinceQuery(prefix string, since time.Time) string {
	return prefix + "/history/since?date=" + since.UTC().Format(time.RFC3339)
}

type rescanCommand struct {
	Name     string `json:"name"`
	SeriesID int    `json:"seriesId"`
}

func postRescan(ctx context.Context, t *transport.Client, d *transport.Detection, prefix string, seriesID int) transport.Result {
	return t.Post(ctx, d, prefix+"/command", rescanCommand{Name: "RescanSeries", SeriesID: seriesID})
}

func mediaCoverPath(prefix string, id int, file string) string {
	return prefix + "/mediacover/" + strconv.Itoa(id) + "/" + file
}
