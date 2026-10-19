package sonarr

import "github.com/cplieger/arrapi/v3"

// Features describes the engine the detection behind this Info selected. A later call can
// switch engines after an in-place upgrade without a new Info; the per-item fields
// (Order.Official, a nil IDs.AniList) stay correct for every answer.
type Features struct {
	SeriesOrders bool // Series.Order can be non-official, so Season and Number can be in another order than aired
	SeasonTitles bool // Season.Title can be set
	Translations bool // Series.Language, OriginalTitle and Translations are filled
	AnimeIDs     bool // Series.IDs.AniList is filled where the server knows it
}

// Series is a series in the server's library.
type Series struct {
	OriginalLanguage *arrapi.Language
	Language         *arrapi.Language // metadata language; nil unless Features.Translations
	Title            string           // in the metadata language on Sonarr v5 (API v5)
	OriginalTitle    string           // "" unless Features.Translations
	TitleSlug        string
	Translations     []Translation
	AlternateTitles  []string
	Seasons          []Season
	Tags             []int
	Order            Order
	IDs              ExternalIDs
	ID               int
	Year             int
	FileCount        int // episodes that have a file on disk, a multi-episode file counting once per episode; 0 when none or unknown
}

// ExternalIDs are a series' ids at the metadata providers.
type ExternalIDs struct {
	IMDb    string // "tt" + digits; "" when none
	AniList []int  // ascending; nil unless Features.AnimeIDs
	TVDB    int    // 0 when unknown
	TMDB    int
}

// Order is the series' episode order. Type is the server's code ("official", "dvd",
// "alternate", "alttwo", ...); codes are reused across series with different meanings,
// so Name identifies an order.
type Order struct {
	Type     string
	Name     string // "Aired Order", "DVD Order", "Superfan Order"; "" when the server lists no order of this type
	Official bool   // Type == "official": seasons and numbers are TheTVDB's aired order
}

// officialOrder is the Order.Type of TheTVDB's aired order.
const officialOrder = "official"

// Translation is a series title in another language.
type Translation struct {
	Title    string
	Language arrapi.Language
}

// Season is one season of a series.
type Season struct {
	Title  string // "" unless Features.SeasonTitles
	Number int    // in Series.Order
}

// Episode is one episode of a series.
type Episode struct {
	File     *EpisodeFile // set only by EpisodesWithFiles, for an episode with a file
	Title    string
	AirDate  string // the series' local air date, "2006-10-05"; "" when unaired
	ID       int
	SeriesID int
	Season   int // in Series.Order
	Number   int // in Series.Order
	Absolute int // 0 when none; measured 0 under every non-official order
	TVDBID   int // 0 when unknown; the server fills it when it refreshes the series
	FileID   int // 0 when no file; shared by the episodes of a multi-episode file
	HasFile  bool
}

// EpisodeFile is a file on disk holding one or more episodes.
type EpisodeFile struct {
	MediaInfo    *arrapi.MediaInfo
	Revision     *arrapi.Revision
	RelativePath string
	Path         string
	SceneName    string
	ReleaseGroup string
	ID           int
	Season       int // in Series.Order
	Size         int64
}
