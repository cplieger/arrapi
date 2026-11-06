package sonarr

import (
	"context"
	"strconv"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/arrenum"
	"github.com/cplieger/arrapi/v3/internal/joinedmedia"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// sonarrV4APIv3 reads Sonarr 4.x over /api/v3. Its wire contract is Sonarr's v4-develop
// branch, never develop's Sonarr.Api.V3.
type sonarrV4APIv3 struct{ t *transport.Client }

var _ engine = sonarrV4APIv3{}

const sonarrV4Prefix = "/api/v3"

func (sonarrV4APIv3) features() Features { return Features{} }

func (e sonarrV4APIv3) series(ctx context.Context, d *transport.Detection) ([]Series, transport.Result) {
	return transport.GetList(ctx, e.t, d, sonarrV4Prefix+"/series", (*sonarrV4Series).series)
}

func (e sonarrV4APIv3) seriesByID(ctx context.Context, d *transport.Detection, id int) (Series, transport.Result) {
	return transport.GetOne(ctx, e.t, d, sonarrV4Prefix+"/series/"+strconv.Itoa(id), (*sonarrV4Series).series)
}

func (e sonarrV4APIv3) episodes(ctx context.Context, d *transport.Detection, seriesID int, withFiles bool) ([]Episode, transport.Result) {
	path := seriesQuery(sonarrV4Prefix, "episode", seriesID)
	if withFiles {
		path += "&includeEpisodeFile=true"
	}
	return transport.GetList(ctx, e.t, d, path, func(w *sonarrV4Episode) Episode { return w.episode(withFiles) })
}

func (e sonarrV4APIv3) episodeFiles(ctx context.Context, d *transport.Detection, seriesID int) ([]EpisodeFile, transport.Result) {
	return transport.GetList(ctx, e.t, d, seriesQuery(sonarrV4Prefix, "episodefile", seriesID), (*sonarrV4EpisodeFile).episodeFile)
}

func (e sonarrV4APIv3) historySince(ctx context.Context, d *transport.Detection, since time.Time) ([]HistoryRecord, transport.Result) {
	return transport.GetList(ctx, e.t, d, sinceQuery(sonarrV4Prefix, since), (*sonarrV4History).record)
}

func (e sonarrV4APIv3) tags(ctx context.Context, d *transport.Detection) ([]arrapi.Tag, transport.Result) {
	return transport.GetList(ctx, e.t, d, sonarrV4Prefix+"/tag", (*sonarrV4Tag).tag)
}

func (e sonarrV4APIv3) rootFolders(ctx context.Context, d *transport.Detection) ([]string, transport.Result) {
	return transport.GetList(ctx, e.t, d, sonarrV4Prefix+"/rootfolder", (*sonarrV4RootFolder).path)
}

func (e sonarrV4APIv3) commands(ctx context.Context, d *transport.Detection) ([]Command, transport.Result) {
	return transport.GetList(ctx, e.t, d, sonarrV4Prefix+"/command", (*sonarrV4Command).command)
}

func (e sonarrV4APIv3) queue(ctx context.Context, d *transport.Detection) ([]QueueItem, transport.Result) {
	rows, r := transport.GetList(ctx, e.t, d, sonarrV4Prefix+"/queue/details", func(w *sonarrV4QueueItem) *sonarrV4QueueItem { return w })
	if !r.Accepted() {
		return nil, r
	}
	return groupV4Queue(rows), r
}

func (e sonarrV4APIv3) releases(ctx context.Context, d *transport.Detection, q releaseQuery) ([]Release, transport.Result) {
	return transport.GetListOnce(ctx, e.t, d, q.path(sonarrV4Prefix), (*sonarrV4Release).release)
}

func (e sonarrV4APIv3) grab(ctx context.Context, d *transport.Detection, g releaseGrab) transport.Result {
	return e.t.Post(ctx, d, sonarrV4Prefix+"/release", g)
}

func (e sonarrV4APIv3) rescan(ctx context.Context, d *transport.Detection, seriesID int) transport.Result {
	return postRescan(ctx, e.t, d, sonarrV4Prefix, seriesID)
}

func (e sonarrV4APIv3) search(ctx context.Context, d *transport.Detection, cmd searchCommand) (int, transport.Result) {
	return e.t.PostCommand(ctx, d, sonarrV4Prefix+"/command", cmd)
}

func (sonarrV4APIv3) mediaCoverPath(id int, file string) string {
	return mediaCoverPath(sonarrV4Prefix, id, file)
}

func (e sonarrV4APIv3) mediaCover(ctx context.Context, d *transport.Detection, id int, file string, limit int64) (arrapi.Cover, transport.Result) {
	return getCover(ctx, e.t, d, sonarrV4Prefix, id, file, limit)
}

type sonarrV4Series struct {
	OriginalLanguage *sonarrV4Language        `json:"originalLanguage"`
	Title            string                   `json:"title"`
	TitleSlug        string                   `json:"titleSlug"`
	IMDbID           string                   `json:"imdbId"`
	SeriesType       seriesTypeToken          `json:"seriesType"`
	AlternateTitles  []sonarrV4AlternateTitle `json:"alternateTitles"`
	Seasons          []sonarrV4Season         `json:"seasons"`
	Tags             []int                    `json:"tags"`
	Statistics       sonarrV4Statistics       `json:"statistics"`
	ID               int                      `json:"id"`
	Year             int                      `json:"year"`
	TVDBID           int                      `json:"tvdbId"`
	TMDBID           int                      `json:"tmdbId"`
	Monitored        bool                     `json:"monitored"`
}

type sonarrV4Language struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

type sonarrV4AlternateTitle struct {
	Title string `json:"title"`
}

type sonarrV4Season struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

type sonarrV4Statistics struct {
	EpisodeFileCount int `json:"episodeFileCount"`
}

type sonarrV4Episode struct {
	EpisodeFile           *sonarrV4EpisodeFile `json:"episodeFile"`
	Title                 string               `json:"title"`
	AirDate               string               `json:"airDate"`
	ID                    int                  `json:"id"`
	SeriesID              int                  `json:"seriesId"`
	TVDBID                int                  `json:"tvdbId"`
	EpisodeFileID         int                  `json:"episodeFileId"`
	SeasonNumber          int                  `json:"seasonNumber"`
	EpisodeNumber         int                  `json:"episodeNumber"`
	AbsoluteEpisodeNumber int                  `json:"absoluteEpisodeNumber"`
	HasFile               bool                 `json:"hasFile"`
	Monitored             bool                 `json:"monitored"`
}

type sonarrV4EpisodeFile struct {
	Quality      *sonarrV4Quality   `json:"quality"`
	MediaInfo    *sonarrV4MediaInfo `json:"mediaInfo"`
	RelativePath string             `json:"relativePath"`
	Path         string             `json:"path"`
	SceneName    string             `json:"sceneName"`
	ReleaseGroup string             `json:"releaseGroup"`
	ID           int                `json:"id"`
	SeasonNumber int                `json:"seasonNumber"`
	Size         int64              `json:"size"`
}

type sonarrV4Quality struct {
	Revision *sonarrV4Revision `json:"revision"`
}

type sonarrV4Revision struct {
	Version  int  `json:"version"`
	IsRepack bool `json:"isRepack"`
}

type sonarrV4MediaInfo struct {
	VideoCodec     string `json:"videoCodec"`
	AudioLanguages string `json:"audioLanguages"`
}

type sonarrV4History struct {
	Date        time.Time           `json:"date"`
	DownloadID  string              `json:"downloadId"`
	SourceTitle string              `json:"sourceTitle"`
	Data        sonarrV4HistoryData `json:"data"`
	EventType   eventToken          `json:"eventType"`
	ID          int                 `json:"id"`
	SeriesID    int                 `json:"seriesId"`
	EpisodeID   int                 `json:"episodeId"`
}

// sonarrV4HistoryData is the record's data dictionary, of which only the grab fields are read.
type sonarrV4HistoryData struct {
	GUID          string `json:"guid"`
	Indexer       string `json:"indexer"`
	InfoURL       string `json:"nzbInfoUrl"`
	ReleaseSource string `json:"releaseSource"`
	Message       string `json:"message"`
}

// sonarrV4Command is a CommandResource; its body is the command class's own members.
type sonarrV4Command struct {
	Queued  time.Time           `json:"queued"`
	Started time.Time           `json:"started"`
	Ended   time.Time           `json:"ended"`
	Name    string              `json:"name"`
	Message string              `json:"message"`
	Status  commandStatusToken  `json:"status"`
	Body    sonarrV4CommandBody `json:"body"`
	ID      int                 `json:"id"`
}

type sonarrV4CommandBody struct {
	SeriesIDs   []int `json:"seriesIds"`
	SeriesID    int   `json:"seriesId"`
	IsNewSeries bool  `json:"isNewSeries"`
}

// sonarrV4QueueItem is a QueueResource. Its size left is "sizeleft": v4-develop
// comments out the SizeLeft property, which collides with it case-insensitively.
type sonarrV4QueueItem struct {
	Title                string                  `json:"title"`
	DownloadID           string                  `json:"downloadId"`
	DownloadClient       string                  `json:"downloadClient"`
	TrackedDownloadState downloadStateToken      `json:"trackedDownloadState"`
	StatusMessages       []sonarrV4StatusMessage `json:"statusMessages"`
	EpisodeID            int                     `json:"episodeId"`
	SeriesID             int                     `json:"seriesId"`
	Size                 float64                 `json:"size"`
	SizeLeft             float64                 `json:"sizeleft"`
}

type sonarrV4StatusMessage struct {
	Title    string   `json:"title"`
	Messages []string `json:"messages"`
}

// sonarrV4Release is a flat ReleaseResource. Its downloadUrl and magnetUrl are not
// declared, so neither link is ever decoded.
type sonarrV4Release struct {
	GUID                string                 `json:"guid"`
	Indexer             string                 `json:"indexer"`
	Title               string                 `json:"title"`
	InfoHash            string                 `json:"infoHash"`
	InfoURL             string                 `json:"infoUrl"`
	CommentURL          string                 `json:"commentUrl"`
	Quality             sonarrReleaseQuality   `json:"quality"`
	Rejections          []string               `json:"rejections"`
	MappedEpisodeInfo   []sonarrReleaseEpisode `json:"mappedEpisodeInfo"`
	Size                int64                  `json:"size"`
	IndexerID           int                    `json:"indexerId"`
	CustomFormatScore   int                    `json:"customFormatScore"`
	MappedSeriesID      int                    `json:"mappedSeriesId"`
	ReleaseWeight       int                    `json:"releaseWeight"`
	IndexerFlags        IndexerFlags           `json:"indexerFlags"`
	Approved            bool                   `json:"approved"`
	TemporarilyRejected bool                   `json:"temporarilyRejected"`
	Rejected            bool                   `json:"rejected"`
}

type sonarrV4Tag struct {
	Label string `json:"label"`
	ID    int    `json:"id"`
}

type sonarrV4RootFolder struct {
	Path string `json:"path"`
}

func (w *sonarrV4Series) series() Series {
	var seasons []Season
	if w.Seasons != nil {
		seasons = make([]Season, len(w.Seasons))
		for i, s := range w.Seasons {
			seasons[i] = Season{Number: s.SeasonNumber, Monitored: s.Monitored}
		}
	}
	var titles []string
	if w.AlternateTitles != nil {
		titles = make([]string, 0, len(w.AlternateTitles))
		for _, a := range w.AlternateTitles {
			if a.Title != "" {
				titles = append(titles, a.Title)
			}
		}
	}
	return Series{
		OriginalLanguage: w.OriginalLanguage.language(),
		Title:            w.Title,
		TitleSlug:        w.TitleSlug,
		IDs:              ExternalIDs{IMDb: w.IMDbID, TVDB: w.TVDBID, TMDB: w.TMDBID},
		// Sonarr 4 has no episode orders (no SeasonType in v4-develop's NzbDrone.Core):
		// every series is in TheTVDB's aired order.
		Order:           Order{Type: officialOrder, Official: true},
		AlternateTitles: titles,
		Seasons:         seasons,
		Tags:            w.Tags,
		Type:            seriesType(string(w.SeriesType)),
		ID:              w.ID,
		Year:            w.Year,
		FileCount:       w.Statistics.EpisodeFileCount,
		Monitored:       w.Monitored,
	}
}

func (l *sonarrV4Language) language() *arrapi.Language {
	if l == nil {
		return nil
	}
	return &arrapi.Language{Name: l.Name, ID: l.ID}
}

// episode maps w; File is set only when withFile is true and the server embedded one.
func (w *sonarrV4Episode) episode(withFile bool) Episode {
	var file *EpisodeFile
	if withFile && w.EpisodeFile != nil {
		f := w.EpisodeFile.episodeFile()
		file = &f
	}
	return Episode{
		File:      file,
		Title:     w.Title,
		AirDate:   w.AirDate,
		ID:        w.ID,
		SeriesID:  w.SeriesID,
		Season:    w.SeasonNumber,
		Number:    w.EpisodeNumber,
		Absolute:  w.AbsoluteEpisodeNumber,
		TVDBID:    w.TVDBID,
		FileID:    w.EpisodeFileID,
		HasFile:   w.HasFile,
		Monitored: w.Monitored,
	}
}

func (w *sonarrV4EpisodeFile) episodeFile() EpisodeFile {
	var rev *arrapi.Revision
	if w.Quality != nil && w.Quality.Revision != nil {
		rev = &arrapi.Revision{Version: w.Quality.Revision.Version, IsRepack: w.Quality.Revision.IsRepack}
	}
	var mi *arrapi.MediaInfo
	if w.MediaInfo != nil {
		mi = &arrapi.MediaInfo{
			VideoCodec:     w.MediaInfo.VideoCodec,
			AudioLanguages: joinedmedia.Languages(w.MediaInfo.AudioLanguages),
		}
	}
	return EpisodeFile{
		MediaInfo:    mi,
		Revision:     rev,
		RelativePath: w.RelativePath,
		Path:         w.Path,
		SceneName:    w.SceneName,
		ReleaseGroup: w.ReleaseGroup,
		ID:           w.ID,
		Season:       w.SeasonNumber,
		Size:         w.Size,
	}
}

func (w *sonarrV4History) record() HistoryRecord {
	return HistoryRecord{
		Date:        w.Date,
		DownloadID:  w.DownloadID,
		SourceTitle: w.SourceTitle,
		GUID:        w.Data.GUID,
		Indexer:     w.Data.Indexer,
		InfoURL:     w.Data.InfoURL,
		Source:      arrenum.ReleaseSource(w.Data.ReleaseSource),
		Message:     w.Data.Message,
		EventType:   EventType(w.EventType),
		ID:          w.ID,
		SeriesID:    w.SeriesID,
		EpisodeID:   w.EpisodeID,
	}
}

func (w *sonarrV4Command) command() Command {
	return Command{
		Queued:    w.Queued,
		Started:   w.Started,
		Ended:     w.Ended,
		Name:      w.Name,
		Message:   w.Message,
		SeriesIDs: idsOf(w.Body.SeriesIDs, w.Body.SeriesID),
		ID:        w.ID,
		Status:    arrenum.CommandStatus(string(w.Status)),
		NewSeries: w.Body.IsNewSeries,
	}
}

// groupV4Queue folds the rows of one download, those sharing a non-empty
// (downloadClient, downloadId), into one item in first-row order, collecting each
// row's episode of 1 or more; a null episodeId decodes as 0. Held-back rows carry no
// downloadId and stay one item each.
func groupV4Queue(rows []*sonarrV4QueueItem) []QueueItem {
	type key struct{ client, id string }
	out := make([]QueueItem, 0, len(rows))
	at := map[key]int{}
	for _, w := range rows {
		k := key{w.DownloadClient, w.DownloadID}
		if i, ok := at[k]; ok {
			out[i].EpisodeIDs = idsOf(out[i].EpisodeIDs, w.EpisodeID)
			continue
		}
		if w.DownloadID != "" {
			at[k] = len(out)
		}
		out = append(out, w.item())
	}
	return out
}

func (w *sonarrV4QueueItem) item() QueueItem {
	var msgs []arrapi.StatusMessage
	if len(w.StatusMessages) > 0 {
		msgs = make([]arrapi.StatusMessage, len(w.StatusMessages))
		for i, m := range w.StatusMessages {
			msgs[i] = arrapi.StatusMessage{Title: m.Title, Messages: m.Messages}
		}
	}
	return QueueItem{
		Title:          w.Title,
		DownloadID:     w.DownloadID,
		DownloadClient: w.DownloadClient,
		State:          arrenum.DownloadState(string(w.TrackedDownloadState)),
		Messages:       msgs,
		EpisodeIDs:     idsOf(nil, w.EpisodeID),
		SeriesID:       w.SeriesID,
		Size:           queueBytes(w.Size),
		SizeLeft:       queueBytes(w.SizeLeft),
	}
}

func (w *sonarrV4Release) release() Release {
	var rejections []string
	if len(w.Rejections) > 0 {
		rejections = w.Rejections
	}
	return Release{
		GUID:                w.GUID,
		Indexer:             w.Indexer,
		Title:               w.Title,
		Quality:             w.Quality.Quality.Name,
		InfoHash:            w.InfoHash,
		InfoURL:             w.InfoURL,
		CommentURL:          w.CommentURL,
		Rejections:          rejections,
		MappedEpisodes:      releaseEpisodes(w.MappedEpisodeInfo),
		Size:                w.Size,
		IndexerID:           w.IndexerID,
		CustomFormatScore:   w.CustomFormatScore,
		MappedSeriesID:      w.MappedSeriesID,
		Weight:              w.ReleaseWeight,
		Flags:               w.IndexerFlags,
		Approved:            w.Approved,
		TemporarilyRejected: w.TemporarilyRejected,
		Rejected:            w.Rejected,
	}
}

func (w *sonarrV4Tag) tag() arrapi.Tag { return arrapi.Tag{Label: w.Label, ID: w.ID} }

func (w *sonarrV4RootFolder) path() string { return w.Path }
