package radarr

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/arrenum"
	"github.com/cplieger/arrapi/v3/internal/coverfile"
	"github.com/cplieger/arrapi/v3/internal/joinedmedia"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// radarrAPIv3 reads Radarr over /api/v3. Its wire contract is Radarr's develop branch,
// src/Radarr.Api.V3. Each request method sends one attempt on the detection it is
// handed and returns that attempt's Result; its value is set only when Decide accepted
// the answer and it decoded.
type radarrAPIv3 struct{ t *transport.Client }

const radarrPrefix = "/api/v3"

func (e radarrAPIv3) movies(ctx context.Context, d *transport.Detection) ([]Movie, transport.Result) {
	return transport.GetList(ctx, e.t, d, radarrPrefix+"/movie", (*radarrMovie).movie)
}

func (e radarrAPIv3) movieByID(ctx context.Context, d *transport.Detection, id int) (Movie, transport.Result) {
	return transport.GetOne(ctx, e.t, d, radarrPrefix+"/movie/"+strconv.Itoa(id), (*radarrMovie).movie)
}

func (e radarrAPIv3) historySince(ctx context.Context, d *transport.Detection, since time.Time) ([]HistoryRecord, transport.Result) {
	return transport.GetList(ctx, e.t, d, historySincePath(since), (*radarrHistory).record)
}

// historySincePath sends includeMovie=false, Radarr's default, so a changed default
// cannot embed a movie in every record.
func historySincePath(since time.Time) string {
	return radarrPrefix + "/history/since?date=" + since.UTC().Format(time.RFC3339) + "&includeMovie=false"
}

func (e radarrAPIv3) tags(ctx context.Context, d *transport.Detection) ([]arrapi.Tag, transport.Result) {
	return transport.GetList(ctx, e.t, d, radarrPrefix+"/tag", (*radarrTag).tag)
}

func (e radarrAPIv3) rootFolders(ctx context.Context, d *transport.Detection) ([]string, transport.Result) {
	return transport.GetList(ctx, e.t, d, radarrPrefix+"/rootfolder", (*radarrRootFolder).path)
}

func (e radarrAPIv3) commands(ctx context.Context, d *transport.Detection) ([]Command, transport.Result) {
	return transport.GetList(ctx, e.t, d, radarrPrefix+"/command", (*radarrCommand).command)
}

func (e radarrAPIv3) queue(ctx context.Context, d *transport.Detection) ([]QueueItem, transport.Result) {
	return transport.GetList(ctx, e.t, d, radarrPrefix+"/queue/details", (*radarrQueueItem).item)
}

func (e radarrAPIv3) releases(ctx context.Context, d *transport.Detection, movieID int) ([]Release, transport.Result) {
	return transport.GetListOnce(ctx, e.t, d, radarrPrefix+"/release?movieId="+strconv.Itoa(movieID), (*radarrRelease).release)
}

func (e radarrAPIv3) grab(ctx context.Context, d *transport.Detection, g releaseGrab) transport.Result {
	return e.t.Post(ctx, d, radarrPrefix+"/release", g)
}

type rescanCommand struct {
	Name    string `json:"name"`
	MovieID int    `json:"movieId"`
}

func (e radarrAPIv3) rescan(ctx context.Context, d *transport.Detection, movieID int) transport.Result {
	return e.t.Post(ctx, d, radarrPrefix+"/command", rescanCommand{Name: "RescanMovie", MovieID: movieID})
}

type moviesSearchCommand struct {
	Name     string `json:"name"`
	MovieIDs []int  `json:"movieIds"`
}

func (e radarrAPIv3) moviesSearch(ctx context.Context, d *transport.Detection, movieIDs []int) (int, transport.Result) {
	return e.t.PostCommand(ctx, d, radarrPrefix+"/command", moviesSearchCommand{Name: "MoviesSearch", MovieIDs: movieIDs})
}

func (radarrAPIv3) mediaCoverPath(id int, file string) string {
	return radarrPrefix + "/mediacover/" + strconv.Itoa(id) + "/" + file
}

func (e radarrAPIv3) mediaCover(ctx context.Context, d *transport.Detection, id int, file string, limit int64) (arrapi.Cover, transport.Result) {
	mediaType := coverfile.MediaType(file)
	var data []byte
	r := e.t.GetFile(ctx, d, e.mediaCoverPath(id, file), limit, mediaType, &data)
	if !r.Accepted() {
		return arrapi.Cover{}, r
	}
	return arrapi.Cover{ContentType: mediaType, Data: data}, r
}

type radarrMovie struct {
	MovieFile        *radarrMovieFile       `json:"movieFile"`
	OriginalLanguage *radarrLanguage        `json:"originalLanguage"`
	Title            string                 `json:"title"`
	IMDbID           string                 `json:"imdbId"`
	AlternateTitles  []radarrAlternateTitle `json:"alternateTitles"`
	Tags             []int                  `json:"tags"`
	ID               int                    `json:"id"`
	Year             int                    `json:"year"`
	TMDBID           int                    `json:"tmdbId"`
	HasFile          bool                   `json:"hasFile"`
	Monitored        bool                   `json:"monitored"`
}

type radarrLanguage struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

type radarrAlternateTitle struct {
	Title string `json:"title"`
}

type radarrMovieFile struct {
	Quality      *radarrQuality   `json:"quality"`
	MediaInfo    *radarrMediaInfo `json:"mediaInfo"`
	RelativePath string           `json:"relativePath"`
	Path         string           `json:"path"`
	SceneName    string           `json:"sceneName"`
	ReleaseGroup string           `json:"releaseGroup"`
	ID           int              `json:"id"`
	Size         int64            `json:"size"`
}

type radarrQuality struct {
	Revision *radarrRevision `json:"revision"`
}

type radarrRevision struct {
	Version  int  `json:"version"`
	IsRepack bool `json:"isRepack"`
}

type radarrMediaInfo struct {
	VideoCodec     string `json:"videoCodec"`
	AudioLanguages string `json:"audioLanguages"`
}

type radarrHistory struct {
	Date        time.Time         `json:"date"`
	DownloadID  string            `json:"downloadId"`
	SourceTitle string            `json:"sourceTitle"`
	Data        radarrHistoryData `json:"data"`
	EventType   eventToken        `json:"eventType"`
	ID          int               `json:"id"`
	MovieID     int               `json:"movieId"`
}

// radarrHistoryData is the record's data dictionary, of which only the grab fields are read.
type radarrHistoryData struct {
	GUID          string `json:"guid"`
	Indexer       string `json:"indexer"`
	InfoURL       string `json:"nzbInfoUrl"`
	ReleaseSource string `json:"releaseSource"`
	Message       string `json:"message"`
}

// radarrQueueItem is a QueueResource.
type radarrQueueItem struct {
	Title                string                `json:"title"`
	DownloadID           string                `json:"downloadId"`
	DownloadClient       string                `json:"downloadClient"`
	TrackedDownloadState downloadStateToken    `json:"trackedDownloadState"`
	StatusMessages       []radarrStatusMessage `json:"statusMessages"`
	MovieID              int                   `json:"movieId"`
	Size                 float64               `json:"size"`
	SizeLeft             float64               `json:"sizeleft"`
}

type radarrStatusMessage struct {
	Title    string   `json:"title"`
	Messages []string `json:"messages"`
}

// radarrCommand is a CommandResource; its body is the command class's own members.
type radarrCommand struct {
	Queued  time.Time          `json:"queued"`
	Started time.Time          `json:"started"`
	Ended   time.Time          `json:"ended"`
	Name    string             `json:"name"`
	Message string             `json:"message"`
	Status  commandStatusToken `json:"status"`
	Body    radarrCommandBody  `json:"body"`
	ID      int                `json:"id"`
}

type radarrCommandBody struct {
	MovieIDs   []int `json:"movieIds"`
	MovieID    int   `json:"movieId"`
	IsNewMovie bool  `json:"isNewMovie"`
}

// radarrRelease is a ReleaseResource. Its downloadUrl and magnetUrl are not declared,
// so neither is ever decoded.
type radarrRelease struct {
	GUID                string               `json:"guid"`
	Indexer             string               `json:"indexer"`
	Title               string               `json:"title"`
	InfoHash            string               `json:"infoHash"`
	InfoURL             string               `json:"infoUrl"`
	CommentURL          string               `json:"commentUrl"`
	Quality             radarrReleaseQuality `json:"quality"`
	Rejections          []string             `json:"rejections"`
	Size                int64                `json:"size"`
	IndexerID           int                  `json:"indexerId"`
	CustomFormatScore   int                  `json:"customFormatScore"`
	MappedMovieID       int                  `json:"mappedMovieId"`
	ReleaseWeight       int                  `json:"releaseWeight"`
	IndexerFlags        radarrIndexerFlags   `json:"indexerFlags"`
	Approved            bool                 `json:"approved"`
	TemporarilyRejected bool                 `json:"temporarilyRejected"`
	Rejected            bool                 `json:"rejected"`
}

// radarrReleaseQuality is a release's QualityModel, of which only the name is read.
type radarrReleaseQuality struct {
	Quality radarrQualityName `json:"quality"`
}

type radarrQualityName struct {
	Name string `json:"name"`
}

// releaseGrab is the grab body: the two members that key Radarr's release cache.
type releaseGrab struct {
	GUID      string `json:"guid"`
	IndexerID int    `json:"indexerId"`
}

// radarrIndexerFlags is a release's indexerFlags, which ReleaseResource declares
// dynamic: Radarr writes the names of its IndexerFlags enum (ToResource) and reads an
// integer back (ToModel). A name or entry Radarr does not define sets no bit, and an
// integer keeps only Radarr's bits; any other JSON kind is a decode error.
type radarrIndexerFlags IndexerFlags

var radarrFlagNames = map[string]IndexerFlags{
	"G_Freeleech": FlagFreeleech, "G_Halfleech": FlagHalfleech, "G_DoubleUpload": FlagDoubleUpload,
	"PTP_Golden": FlagPTPGolden, "PTP_Approved": FlagPTPApproved, "G_Internal": FlagInternal,
	"AHD_Internal": FlagAHDInternal, "G_Scene": FlagScene, "G_Freeleech75": FlagFreeleech75,
	"G_Freeleech25": FlagFreeleech25, "AHD_UserRelease": FlagAHDUserRelease, "Nuked": FlagNuked,
}

const radarrFlagsAll = FlagFreeleech | FlagHalfleech | FlagDoubleUpload | FlagPTPGolden | FlagPTPApproved |
	FlagInternal | FlagAHDInternal | FlagScene | FlagFreeleech75 | FlagFreeleech25 | FlagAHDUserRelease | FlagNuked

func (f *radarrIndexerFlags) UnmarshalJSON(b []byte) error {
	var kind string
	switch c := b[0]; {
	case c == 'n':
		return nil
	case c == '[':
		var entries []json.RawMessage
		if err := json.Unmarshal(b, &entries); err != nil {
			return err
		}
		var v IndexerFlags
		for _, e := range entries {
			var name string
			if e[0] == '"' && json.Unmarshal(e, &name) == nil {
				v |= radarrFlagNames[name]
			}
		}
		*f = radarrIndexerFlags(v)
		return nil
	case c == '-', c >= '0' && c <= '9':
		var n uint32
		if json.Unmarshal(b, &n) == nil {
			*f = radarrIndexerFlags(IndexerFlags(n) & radarrFlagsAll)
			return nil
		}
		kind = "number"
	case c == '"':
		kind = "string"
	default:
		kind = tokenKind(c)
	}
	return &json.UnmarshalTypeError{Value: kind, Type: reflect.TypeFor[IndexerFlags](), Field: "indexerFlags"}
}

func (w *radarrRelease) release() Release {
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
		Size:                w.Size,
		IndexerID:           w.IndexerID,
		CustomFormatScore:   w.CustomFormatScore,
		MappedMovieID:       w.MappedMovieID,
		Weight:              w.ReleaseWeight,
		Flags:               IndexerFlags(w.IndexerFlags),
		Approved:            w.Approved,
		TemporarilyRejected: w.TemporarilyRejected,
		Rejected:            w.Rejected,
	}
}

type radarrTag struct {
	Label string `json:"label"`
	ID    int    `json:"id"`
}

type radarrRootFolder struct {
	Path string `json:"path"`
}

func (w *radarrMovie) movie() Movie {
	var titles []string
	if w.AlternateTitles != nil {
		titles = make([]string, 0, len(w.AlternateTitles))
		for _, a := range w.AlternateTitles {
			if a.Title != "" {
				titles = append(titles, a.Title)
			}
		}
	}
	var file *MovieFile
	if w.MovieFile != nil {
		f := w.MovieFile.movieFile()
		file = &f
	}
	var lang *arrapi.Language
	if w.OriginalLanguage != nil {
		lang = &arrapi.Language{Name: w.OriginalLanguage.Name, ID: w.OriginalLanguage.ID}
	}
	return Movie{
		File:             file,
		OriginalLanguage: lang,
		Title:            w.Title,
		IDs:              ExternalIDs{IMDb: w.IMDbID, TMDB: w.TMDBID},
		AlternateTitles:  titles,
		Tags:             w.Tags,
		ID:               w.ID,
		Year:             w.Year,
		HasFile:          w.HasFile,
		Monitored:        w.Monitored,
	}
}

func (w *radarrMovieFile) movieFile() MovieFile {
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
	return MovieFile{
		MediaInfo:    mi,
		Revision:     rev,
		RelativePath: w.RelativePath,
		Path:         w.Path,
		SceneName:    w.SceneName,
		ReleaseGroup: w.ReleaseGroup,
		ID:           w.ID,
		Size:         w.Size,
	}
}

func (w *radarrHistory) record() HistoryRecord {
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
		MovieID:     w.MovieID,
	}
}

func (w *radarrQueueItem) item() QueueItem {
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
		MovieID:        w.MovieID,
		Size:           queueBytes(w.Size),
		SizeLeft:       queueBytes(w.SizeLeft),
	}
}

func (w *radarrCommand) command() Command {
	return Command{
		Queued:   w.Queued,
		Started:  w.Started,
		Ended:    w.Ended,
		Name:     w.Name,
		Message:  w.Message,
		MovieIDs: idsOf(w.Body.MovieIDs, w.Body.MovieID),
		ID:       w.ID,
		Status:   arrenum.CommandStatus(string(w.Status)),
		NewMovie: w.Body.IsNewMovie,
	}
}

func (w *radarrTag) tag() arrapi.Tag { return arrapi.Tag{Label: w.Label, ID: w.ID} }

func (w *radarrRootFolder) path() string { return w.Path }
