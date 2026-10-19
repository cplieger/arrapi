package sonarr

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/cplieger/arrapi/v3"
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

func (e sonarrV5APIv5) rescan(ctx context.Context, d *transport.Detection, seriesID int) transport.Result {
	return postRescan(ctx, e.t, d, sonarrV5Prefix, seriesID)
}

func (sonarrV5APIv5) mediaCoverPath(id int, file string) string {
	return mediaCoverPath(sonarrV5Prefix, id, file)
}

type sonarrV5Series struct {
	OriginalLanguage *sonarrV5Language        `json:"originalLanguage"`
	Language         *sonarrV5Language        `json:"language"`
	Title            string                   `json:"title"`
	OriginalTitle    string                   `json:"originalTitle"`
	TitleSlug        string                   `json:"titleSlug"`
	IMDbID           string                   `json:"imdbId"`
	SeasonType       string                   `json:"seasonType"`
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
	Date      time.Time  `json:"date"`
	EventType eventToken `json:"eventType"`
	ID        int        `json:"id"`
	SeriesID  int        `json:"seriesId"`
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
			seasons[i] = Season{Title: s.Title, Number: s.SeasonNumber}
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
		ID:               w.ID,
		Year:             w.Year,
		FileCount:        w.Statistics.EpisodeFileCount,
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
	return HistoryRecord{Date: w.Date, EventType: EventType(w.EventType), ID: w.ID, SeriesID: w.SeriesID}
}

func (w *sonarrV5Tag) tag() arrapi.Tag { return arrapi.Tag{Label: w.Label, ID: w.ID} }

func (w *sonarrV5RootFolder) path() string { return w.Path }
