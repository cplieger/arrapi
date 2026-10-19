package radarr

import (
	"context"
	"strconv"
	"time"

	"github.com/cplieger/arrapi/v3"
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

type rescanCommand struct {
	Name    string `json:"name"`
	MovieID int    `json:"movieId"`
}

func (e radarrAPIv3) rescan(ctx context.Context, d *transport.Detection, movieID int) transport.Result {
	return e.t.Post(ctx, d, radarrPrefix+"/command", rescanCommand{Name: "RescanMovie", MovieID: movieID})
}

func (radarrAPIv3) mediaCoverPath(id int, file string) string {
	return radarrPrefix + "/mediacover/" + strconv.Itoa(id) + "/" + file
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
	Date      time.Time  `json:"date"`
	EventType eventToken `json:"eventType"`
	ID        int        `json:"id"`
	MovieID   int        `json:"movieId"`
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
	return HistoryRecord{Date: w.Date, EventType: EventType(w.EventType), ID: w.ID, MovieID: w.MovieID}
}

func (w *radarrTag) tag() arrapi.Tag { return arrapi.Tag{Label: w.Label, ID: w.ID} }

func (w *radarrRootFolder) path() string { return w.Path }
