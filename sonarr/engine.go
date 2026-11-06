package sonarr

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/coverfile"
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
	commands(ctx context.Context, d *transport.Detection) ([]Command, transport.Result)
	queue(ctx context.Context, d *transport.Detection) ([]QueueItem, transport.Result)
	releases(ctx context.Context, d *transport.Detection, q releaseQuery) ([]Release, transport.Result)
	grab(ctx context.Context, d *transport.Detection, g releaseGrab) transport.Result
	rescan(ctx context.Context, d *transport.Detection, seriesID int) transport.Result
	search(ctx context.Context, d *transport.Detection, cmd searchCommand) (int, transport.Result)
	mediaCover(ctx context.Context, d *transport.Detection, id int, file string, limit int64) (arrapi.Cover, transport.Result)
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

// searchCommand is the body of Sonarr's SeriesSearch, SeasonSearch and EpisodeSearch
// commands. SeasonNumber is a pointer so that season 0, the specials, is sent.
type searchCommand struct {
	Name         string `json:"name"`
	SeasonNumber *int   `json:"seasonNumber,omitempty"`
	EpisodeIDs   []int  `json:"episodeIds,omitempty"`
	SeriesID     int    `json:"seriesId,omitempty"`
}

func postRescan(ctx context.Context, t *transport.Client, d *transport.Detection, prefix string, seriesID int) transport.Result {
	return t.Post(ctx, d, prefix+"/command", rescanCommand{Name: "RescanSeries", SeriesID: seriesID})
}

func mediaCoverPath(prefix string, id int, file string) string {
	return prefix + "/mediacover/" + strconv.Itoa(id) + "/" + file
}

func getCover(ctx context.Context, t *transport.Client, d *transport.Detection, prefix string, id int, file string, limit int64) (arrapi.Cover, transport.Result) {
	mediaType := coverfile.MediaType(file)
	var data []byte
	r := t.GetFile(ctx, d, mediaCoverPath(prefix, id, file), limit, mediaType, &data)
	if !r.Accepted() {
		return arrapi.Cover{}, r
	}
	return arrapi.Cover{ContentType: mediaType, Data: data}, r
}

// releaseQuery is one interactive search: of one episode when Episode, else of one
// season.
type releaseQuery struct {
	SeriesID, SeasonNumber, EpisodeID int
	Episode                           bool
}

func (q releaseQuery) path(prefix string) string {
	v := url.Values{}
	if q.Episode {
		v.Set("episodeId", strconv.Itoa(q.EpisodeID))
	} else {
		v.Set("seriesId", strconv.Itoa(q.SeriesID))
		v.Set("seasonNumber", strconv.Itoa(q.SeasonNumber))
	}
	return prefix + "/release?" + v.Encode()
}

// releaseGrab is the grab body of both Sonarr wires: the two members that key Sonarr's
// release cache, and nothing that overrides what Sonarr mapped.
type releaseGrab struct {
	GUID      string `json:"guid"`
	IndexerID int    `json:"indexerId"`
}

// sonarrReleaseEpisode is one mappedEpisodeInfo item, alike on both wires.
type sonarrReleaseEpisode struct {
	ID                    int `json:"id"`
	SeasonNumber          int `json:"seasonNumber"`
	EpisodeNumber         int `json:"episodeNumber"`
	AbsoluteEpisodeNumber int `json:"absoluteEpisodeNumber"`
}

// sonarrReleaseQuality is a release's QualityModel, of which only the name is read.
type sonarrReleaseQuality struct {
	Quality sonarrQualityName `json:"quality"`
}

type sonarrQualityName struct {
	Name string `json:"name"`
}
