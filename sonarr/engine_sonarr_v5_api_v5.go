package sonarr

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/arrenum"
	"github.com/cplieger/arrapi/v3/internal/joinedmedia"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// sonarrV5APIv5 reads Sonarr 5.x over /api/v5. Its wire contract is Sonarr's develop
// branch, src/Sonarr.Api.V5.
type sonarrV5APIv5 struct{ t *transport.Client }

var _ engine = sonarrV5APIv5{}

const sonarrV5Prefix = "/api/v5"

func (sonarrV5APIv5) features() Features {
	return Features{
		SeriesOrders: true,
		SeasonTitles: true,
		Translations: true,
		AnimeIDs:     true,
	}
}

func (e sonarrV5APIv5) series(ctx context.Context, d *transport.Detection) ([]Series, transport.Result) {
	return transport.GetList(ctx, e.t, d, sonarrV5Prefix+"/series", (*sonarrV5Series).series)
}

func (e sonarrV5APIv5) seriesByID(ctx context.Context, d *transport.Detection, id int) (Series, transport.Result) {
	return transport.GetOne(ctx, e.t, d, sonarrV5Prefix+"/series/"+strconv.Itoa(id), (*sonarrV5Series).series)
}

// episodes embeds files through includeSubresources (Sonarr.Api.V5/Episodes/
// EpisodeController.cs): /api/v5 ignores the includeEpisodeFile key Sonarr v4 (API v3)
// reads, so sending it would embed none.
func (e sonarrV5APIv5) episodes(ctx context.Context, d *transport.Detection, seriesID int, withFiles bool) ([]Episode, transport.Result) {
	path := seriesQuery(sonarrV5Prefix, "episode", seriesID)
	if withFiles {
		// Before 5.0.0.2701 this answers 500 while an episode names a file whose row is gone
		// (Sonarr#9162); the read retries within its budget, then returns the 500.
		path += "&includeSubresources=episodeFile"
	}
	return transport.GetList(ctx, e.t, d, path, func(w *sonarrV5Episode) Episode { return w.episode(withFiles) })
}

func (e sonarrV5APIv5) episodeFiles(ctx context.Context, d *transport.Detection, seriesID int) ([]EpisodeFile, transport.Result) {
	return transport.GetList(ctx, e.t, d, seriesQuery(sonarrV5Prefix, "episodefile", seriesID), (*sonarrV5EpisodeFile).episodeFile)
}

func (e sonarrV5APIv5) historySince(ctx context.Context, d *transport.Detection, since time.Time) ([]HistoryRecord, transport.Result) {
	return transport.GetList(ctx, e.t, d, sinceQuery(sonarrV5Prefix, since), (*sonarrV5History).record)
}

func (e sonarrV5APIv5) tags(ctx context.Context, d *transport.Detection) ([]arrapi.Tag, transport.Result) {
	return transport.GetList(ctx, e.t, d, sonarrV5Prefix+"/tag", (*sonarrV5Tag).tag)
}

func (e sonarrV5APIv5) rootFolders(ctx context.Context, d *transport.Detection) ([]string, transport.Result) {
	return transport.GetList(ctx, e.t, d, sonarrV5Prefix+"/rootfolder", (*sonarrV5RootFolder).path)
}

func (e sonarrV5APIv5) commands(ctx context.Context, d *transport.Detection) ([]Command, transport.Result) {
	return transport.GetList(ctx, e.t, d, sonarrV5Prefix+"/command", (*sonarrV5Command).command)
}

func (e sonarrV5APIv5) queue(ctx context.Context, d *transport.Detection) ([]QueueItem, transport.Result) {
	return transport.GetList(ctx, e.t, d, sonarrV5Prefix+"/queue/details", (*sonarrV5QueueItem).item)
}

func (e sonarrV5APIv5) releases(ctx context.Context, d *transport.Detection, q releaseQuery) ([]Release, transport.Result) {
	return transport.GetListOnce(ctx, e.t, d, q.path(sonarrV5Prefix), (*sonarrV5Release).release)
}

func (e sonarrV5APIv5) grab(ctx context.Context, d *transport.Detection, g releaseGrab) transport.Result {
	return e.t.Post(ctx, d, sonarrV5Prefix+"/release", g)
}

func (e sonarrV5APIv5) rescan(ctx context.Context, d *transport.Detection, seriesID int) transport.Result {
	return postRescan(ctx, e.t, d, sonarrV5Prefix, seriesID)
}

func (e sonarrV5APIv5) search(ctx context.Context, d *transport.Detection, cmd searchCommand) (int, transport.Result) {
	return e.t.PostCommand(ctx, d, sonarrV5Prefix+"/command", cmd)
}

func (sonarrV5APIv5) mediaCoverPath(id int, file string) string {
	return mediaCoverPath(sonarrV5Prefix, id, file)
}

func (e sonarrV5APIv5) mediaCover(ctx context.Context, d *transport.Detection, id int, file string, limit int64) (arrapi.Cover, transport.Result) {
	return getCover(ctx, e.t, d, sonarrV5Prefix, id, file, limit)
}

type sonarrV5Series struct {
	OriginalLanguage *sonarrV5Language        `json:"originalLanguage"`
	Language         *sonarrV5Language        `json:"language"`
	Title            string                   `json:"title"`
	OriginalTitle    string                   `json:"originalTitle"`
	TitleSlug        string                   `json:"titleSlug"`
	IMDbID           string                   `json:"imdbId"`
	SeasonType       string                   `json:"seasonType"`
	SeriesType       seriesTypeToken          `json:"seriesType"`
	Translations     []sonarrV5Translation    `json:"translations"`
	AlternateTitles  []sonarrV5AlternateTitle `json:"alternateTitles"`
	Seasons          []sonarrV5Season         `json:"seasons"`
	SeasonTypes      []sonarrV5SeasonType     `json:"seasonTypes"`
	Tags             []int                    `json:"tags"`
	AniListIDs       []int                    `json:"aniListIds"`
	Statistics       sonarrV5Statistics       `json:"statistics"`
	ID               int                      `json:"id"`
	Year             int                      `json:"year"`
	TVDBID           int                      `json:"tvdbId"`
	TMDBID           int                      `json:"tmdbId"`
	Monitored        bool                     `json:"monitored"`
}

type sonarrV5Language struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

type sonarrV5Translation struct {
	Language *sonarrV5Language `json:"language"`
	Title    string            `json:"title"`
}

type sonarrV5AlternateTitle struct {
	Title string `json:"title"`
}

type sonarrV5Season struct {
	Title        string `json:"title"`
	SeasonNumber int    `json:"seasonNumber"`
	Monitored    bool   `json:"monitored"`
}

type sonarrV5SeasonType struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type sonarrV5Statistics struct {
	EpisodeFileCount int `json:"episodeFileCount"`
}

type sonarrV5Episode struct {
	EpisodeFile           *sonarrV5EpisodeFile `json:"episodeFile"`
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

type sonarrV5EpisodeFile struct {
	Quality      *sonarrV5Quality   `json:"quality"`
	MediaInfo    *sonarrV5MediaInfo `json:"mediaInfo"`
	RelativePath string             `json:"relativePath"`
	Path         string             `json:"path"`
	SceneName    string             `json:"sceneName"`
	ReleaseGroup string             `json:"releaseGroup"`
	ID           int                `json:"id"`
	SeasonNumber int                `json:"seasonNumber"`
	Size         int64              `json:"size"`
}

type sonarrV5Quality struct {
	Revision *sonarrV5Revision `json:"revision"`
}

type sonarrV5Revision struct {
	Version  int  `json:"version"`
	IsRepack bool `json:"isRepack"`
}

type sonarrV5MediaInfo struct {
	VideoCodec   string                `json:"videoCodec"`
	AudioStreams []sonarrV5AudioStream `json:"audioStreams"`
}

type sonarrV5AudioStream struct {
	Language string `json:"language"`
}

type sonarrV5History struct {
	Date        time.Time           `json:"date"`
	DownloadID  string              `json:"downloadId"`
	SourceTitle string              `json:"sourceTitle"`
	Data        sonarrV5HistoryData `json:"data"`
	EventType   eventToken          `json:"eventType"`
	ID          int                 `json:"id"`
	SeriesID    int                 `json:"seriesId"`
	EpisodeID   int                 `json:"episodeId"`
}

// sonarrV5HistoryData is the record's data dictionary, of which only the grab fields are read.
type sonarrV5HistoryData struct {
	GUID          string `json:"guid"`
	Indexer       string `json:"indexer"`
	InfoURL       string `json:"nzbInfoUrl"`
	ReleaseSource string `json:"releaseSource"`
	Message       string `json:"message"`
}

// sonarrV5Command is a CommandResource; its body is the command class's own members.
type sonarrV5Command struct {
	Queued  time.Time           `json:"queued"`
	Started time.Time           `json:"started"`
	Ended   time.Time           `json:"ended"`
	Name    string              `json:"name"`
	Message string              `json:"message"`
	Status  commandStatusToken  `json:"status"`
	Body    sonarrV5CommandBody `json:"body"`
	ID      int                 `json:"id"`
}

type sonarrV5CommandBody struct {
	SeriesIDs   []int `json:"seriesIds"`
	SeriesID    int   `json:"seriesId"`
	IsNewSeries bool  `json:"isNewSeries"`
}

// sonarrV5QueueItem is a QueueResource: one row per download, listing its episodes.
type sonarrV5QueueItem struct {
	Title                string                  `json:"title"`
	DownloadID           string                  `json:"downloadId"`
	DownloadClient       string                  `json:"downloadClient"`
	TrackedDownloadState downloadStateToken      `json:"trackedDownloadState"`
	StatusMessages       []sonarrV5StatusMessage `json:"statusMessages"`
	EpisodeIDs           []int                   `json:"episodeIds"`
	SeriesID             int                     `json:"seriesId"`
	Size                 float64                 `json:"size"`
	SizeLeft             float64                 `json:"sizeLeft"`
}

type sonarrV5StatusMessage struct {
	Title    string   `json:"title"`
	Messages []string `json:"messages"`
}

// sonarrV5Release is a ReleaseResource, which nests the indexer's release, Sonarr's
// decision and the parsed title; any of the three can be null.
type sonarrV5Release struct {
	ParsedInfo        sonarrV5ParsedInfo      `json:"parsedInfo"`
	MappedEpisodeInfo []sonarrReleaseEpisode  `json:"mappedEpisodeInfo"`
	Decision          sonarrV5ReleaseDecision `json:"decision"`
	Release           sonarrV5ReleaseInfo     `json:"release"`
	MappedSeriesID    int                     `json:"mappedSeriesId"`
	ReleaseWeight     int                     `json:"releaseWeight"`
	CustomFormatScore int                     `json:"customFormatScore"`
}

type sonarrV5ParsedInfo struct {
	Quality sonarrReleaseQuality `json:"quality"`
}

// sonarrV5ReleaseInfo is a ReleaseInfoResource. Its downloadUrl and magnetUrl are not
// declared, so neither link is ever decoded.
type sonarrV5ReleaseInfo struct {
	GUID         string       `json:"guid"`
	Indexer      string       `json:"indexer"`
	Title        string       `json:"title"`
	InfoHash     string       `json:"infoHash"`
	InfoURL      string       `json:"infoUrl"`
	CommentURL   string       `json:"commentUrl"`
	Size         int64        `json:"size"`
	IndexerID    int          `json:"indexerId"`
	IndexerFlags IndexerFlags `json:"indexerFlags"`
}

type sonarrV5ReleaseDecision struct {
	Rejections          []sonarrV5Rejection `json:"rejections"`
	Approved            bool                `json:"approved"`
	TemporarilyRejected bool                `json:"temporarilyRejected"`
	Rejected            bool                `json:"rejected"`
}

type sonarrV5Rejection struct {
	Message string `json:"message"`
}

type sonarrV5Tag struct {
	Label string `json:"label"`
	ID    int    `json:"id"`
}

type sonarrV5RootFolder struct {
	Path string `json:"path"`
}

func (w *sonarrV5Series) series() Series {
	var translations []Translation
	if w.Translations != nil {
		translations = make([]Translation, len(w.Translations))
		for i, tr := range w.Translations {
			translations[i] = Translation{Title: tr.Title}
			if tr.Language != nil {
				translations[i].Language = *tr.Language.language()
			}
		}
	}
	var seasons []Season
	if w.Seasons != nil {
		seasons = make([]Season, len(w.Seasons))
		for i, s := range w.Seasons {
			seasons[i] = Season{Title: s.Title, Number: s.SeasonNumber, Monitored: s.Monitored}
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
	aniList := w.AniListIDs
	slices.Sort(aniList)
	return Series{
		OriginalLanguage: w.OriginalLanguage.language(),
		Language:         w.Language.language(),
		Title:            w.Title,
		OriginalTitle:    w.OriginalTitle,
		TitleSlug:        w.TitleSlug,
		Translations:     translations,
		AlternateTitles:  titles,
		Seasons:          seasons,
		Tags:             w.Tags,
		Order:            w.order(),
		IDs:              ExternalIDs{IMDb: w.IMDbID, AniList: aniList, TVDB: w.TVDBID, TMDB: w.TMDBID},
		Type:             seriesType(string(w.SeriesType)),
		ID:               w.ID,
		Year:             w.Year,
		FileCount:        w.Statistics.EpisodeFileCount,
		Monitored:        w.Monitored,
	}
}

// order names w's seasonType from seasonTypes. An empty seasonType is an unknown order,
// never aired: Sonarr validates it NotEmpty (src/Sonarr.Api.V5/Series/SeriesController.cs).
func (w *sonarrV5Series) order() Order {
	if w.SeasonType == "" {
		return Order{}
	}
	o := Order{Type: w.SeasonType, Official: w.SeasonType == officialOrder}
	for _, st := range w.SeasonTypes {
		if st.Type == w.SeasonType {
			o.Name = st.Name
			break
		}
	}
	return o
}

func (l *sonarrV5Language) language() *arrapi.Language {
	if l == nil {
		return nil
	}
	return &arrapi.Language{Name: l.Name, ID: l.ID}
}

// episode maps w; File is set only when withFile is true and the server embedded one.
func (w *sonarrV5Episode) episode(withFile bool) Episode {
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

func (w *sonarrV5EpisodeFile) episodeFile() EpisodeFile {
	var rev *arrapi.Revision
	if w.Quality != nil && w.Quality.Revision != nil {
		rev = &arrapi.Revision{Version: w.Quality.Revision.Version, IsRepack: w.Quality.Revision.IsRepack}
	}
	var mi *arrapi.MediaInfo
	if w.MediaInfo != nil {
		mi = &arrapi.MediaInfo{VideoCodec: w.MediaInfo.VideoCodec, AudioLanguages: w.MediaInfo.audioLanguages()}
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

// audioLanguages takes each stream's one language tag, unsplit, in stream order.
func (m *sonarrV5MediaInfo) audioLanguages() []string {
	var langs []string
	for _, s := range m.AudioStreams {
		if lang, ok := joinedmedia.Language(s.Language); ok {
			langs = append(langs, lang)
		}
	}
	return langs
}

func (w *sonarrV5History) record() HistoryRecord {
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

func (w *sonarrV5Command) command() Command {
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

func (w *sonarrV5QueueItem) item() QueueItem {
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
		EpisodeIDs:     idsOf(w.EpisodeIDs, 0),
		SeriesID:       w.SeriesID,
		Size:           queueBytes(w.Size),
		SizeLeft:       queueBytes(w.SizeLeft),
	}
}

func (w *sonarrV5Release) release() Release {
	var rejections []string
	if len(w.Decision.Rejections) > 0 {
		rejections = make([]string, len(w.Decision.Rejections))
		for i, r := range w.Decision.Rejections {
			rejections[i] = r.Message
		}
	}
	info := &w.Release
	return Release{
		GUID:                info.GUID,
		Indexer:             info.Indexer,
		Title:               info.Title,
		Quality:             w.ParsedInfo.Quality.Quality.Name,
		InfoHash:            info.InfoHash,
		InfoURL:             info.InfoURL,
		CommentURL:          info.CommentURL,
		Rejections:          rejections,
		MappedEpisodes:      releaseEpisodes(w.MappedEpisodeInfo),
		Size:                info.Size,
		IndexerID:           info.IndexerID,
		CustomFormatScore:   w.CustomFormatScore,
		MappedSeriesID:      w.MappedSeriesID,
		Weight:              w.ReleaseWeight,
		Flags:               info.IndexerFlags,
		Approved:            w.Decision.Approved,
		TemporarilyRejected: w.Decision.TemporarilyRejected,
		Rejected:            w.Decision.Rejected,
	}
}

func (w *sonarrV5Tag) tag() arrapi.Tag { return arrapi.Tag{Label: w.Label, ID: w.ID} }

func (w *sonarrV5RootFolder) path() string { return w.Path }
