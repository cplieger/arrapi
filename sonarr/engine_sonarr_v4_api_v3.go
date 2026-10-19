package sonarr

import (
	"context"
	"strconv"
	"time"

	"github.com/cplieger/arrapi/v3"
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

func (e sonarrV4APIv3) rescan(ctx context.Context, d *transport.Detection, seriesID int) transport.Result {
	return postRescan(ctx, e.t, d, sonarrV4Prefix, seriesID)
}

func (sonarrV4APIv3) mediaCoverPath(id int, file string) string {
	return mediaCoverPath(sonarrV4Prefix, id, file)
}

type sonarrV4Series struct {
	OriginalLanguage *sonarrV4Language        `json:"originalLanguage"`
	Title            string                   `json:"title"`
	TitleSlug        string                   `json:"titleSlug"`
	IMDbID           string                   `json:"imdbId"`
	AlternateTitles  []sonarrV4AlternateTitle `json:"alternateTitles"`
	Seasons          []sonarrV4Season         `json:"seasons"`
	Tags             []int                    `json:"tags"`
	Statistics       sonarrV4Statistics       `json:"statistics"`
	ID               int                      `json:"id"`
	Year             int                      `json:"year"`
	TVDBID           int                      `json:"tvdbId"`
	TMDBID           int                      `json:"tmdbId"`
}

type sonarrV4Language struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

type sonarrV4AlternateTitle struct {
	Title string `json:"title"`
}

type sonarrV4Season struct {
	SeasonNumber int `json:"seasonNumber"`
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
	Date      time.Time  `json:"date"`
	EventType eventToken `json:"eventType"`
	ID        int        `json:"id"`
	SeriesID  int        `json:"seriesId"`
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
			seasons[i] = Season{Number: s.SeasonNumber}
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
		ID:              w.ID,
		Year:            w.Year,
		FileCount:       w.Statistics.EpisodeFileCount,
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
		File:     file,
		Title:    w.Title,
		AirDate:  w.AirDate,
		ID:       w.ID,
		SeriesID: w.SeriesID,
		Season:   w.SeasonNumber,
		Number:   w.EpisodeNumber,
		Absolute: w.AbsoluteEpisodeNumber,
		TVDBID:   w.TVDBID,
		FileID:   w.EpisodeFileID,
		HasFile:  w.HasFile,
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
	return HistoryRecord{Date: w.Date, EventType: EventType(w.EventType), ID: w.ID, SeriesID: w.SeriesID}
}

func (w *sonarrV4Tag) tag() arrapi.Tag { return arrapi.Tag{Label: w.Label, ID: w.ID} }

func (w *sonarrV4RootFolder) path() string { return w.Path }
