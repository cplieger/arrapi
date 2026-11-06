package sonarr

import (
	"math"
	"reflect"
	"slices"
	"time"

	"github.com/cplieger/arrapi/v3"
)

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
	Type             SeriesType // SeriesStandard, SeriesDaily or SeriesAnime; "" when the server sent none or a type this version of arrapi does not know
	Translations     []Translation
	AlternateTitles  []string
	Seasons          []Season
	Tags             []int
	Order            Order
	IDs              ExternalIDs
	ID               int
	Year             int
	FileCount        int  // episodes that have a file on disk, a multi-episode file counting once per episode; 0 when none or unknown
	Monitored        bool // Sonarr searches for and grabs this series' wanted episodes
}

// SeriesType is how Sonarr numbers and searches a series' episodes.
type SeriesType string

// The series types Sonarr knows (NzbDrone.Core/Tv/SeriesTypes.cs).
const (
	SeriesStandard SeriesType = "standard" // season and episode numbers
	SeriesDaily    SeriesType = "daily"    // air dates
	SeriesAnime    SeriesType = "anime"    // absolute episode numbers
)

// seriesType maps a wire code to its SeriesType, and any other text to "".
func seriesType(code string) SeriesType {
	switch t := SeriesType(code); t {
	case SeriesStandard, SeriesDaily, SeriesAnime:
		return t
	}
	return ""
}

// seriesTypeToken is a wire seriesType: its string as sent, else "".
type seriesTypeToken string

func (s *seriesTypeToken) UnmarshalJSON(b []byte) error {
	v, err := stringToken(b, "seriesType", reflect.TypeFor[SeriesType]())
	*s = seriesTypeToken(v)
	return err
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
	Title     string // "" unless Features.SeasonTitles
	Number    int    // in Series.Order
	Monitored bool   // Sonarr searches for and grabs this season's wanted episodes; the series' own flag still applies
}

// SeasonRef names one season of a series, numbered in the series' Order; Number 0 is
// the specials.
type SeasonRef struct {
	SeriesID int
	Number   int
}

// Episode is one episode of a series.
type Episode struct {
	File      *EpisodeFile // set only by EpisodesWithFiles, for an episode with a file
	Title     string
	AirDate   string // the series' local air date, "2006-10-05"; "" when unaired
	ID        int
	SeriesID  int
	Season    int // in Series.Order
	Number    int // in Series.Order
	Absolute  int // 0 when none; measured 0 under every non-official order
	TVDBID    int // 0 when unknown; the server fills it when it refreshes the series
	FileID    int // 0 when no file; shared by the episodes of a multi-episode file
	HasFile   bool
	Monitored bool // Sonarr searches for and grabs this episode; the series' and season's flags still apply
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

// Command is one command in Sonarr's in-memory command list. The string fields hold
// the server's text; arrapi checks none, so a caller renders them through textsafe or
// an escaping template.
type Command struct {
	Queued    time.Time            // when Sonarr queued it
	Started   time.Time            // zero until it starts
	Ended     time.Time            // zero until it ends
	Name      string               // the command class without its Command suffix: "RescanSeries", "RefreshSeries", "SeriesSearch"
	Message   string               // Sonarr's latest progress or completion text; "" when none
	Status    arrapi.CommandStatus // "" for a status this version of arrapi does not know
	SeriesIDs []int                // the series its body names, from seriesIds or seriesId, in that order and without repeats; nil when it names none
	ID        int
	NewSeries bool // a RefreshSeries Sonarr queued for a series just added or imported
}

// commandStatusToken is a wire command status: its string as sent, else "".
type commandStatusToken string

func (s *commandStatusToken) UnmarshalJSON(b []byte) error {
	v, err := stringToken(b, "status", reflect.TypeFor[arrapi.CommandStatus]())
	*s = commandStatusToken(v)
	return err
}

// idsOf merges a command body's id list and single id: the list's ids in order without
// repeats, then one when it is 1 or more and not listed; nil when that leaves none.
func idsOf(list []int, one int) []int {
	var out []int
	for _, id := range list {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if one >= 1 && !slices.Contains(out, one) {
		out = append(out, one)
	}
	return out
}

// QueueItem is one download in Sonarr's queue, or one release Sonarr holds back before
// sending it to a download client. On Sonarr 4 a held-back release is one item per
// episode; Sonarr 5 lists it once. The string fields hold the server's text.
type QueueItem struct {
	Title          string                 // the release title, file extension removed
	DownloadID     string                 // the download client's id for it, the DownloadID its history records carry; "" for a held-back release
	DownloadClient string                 // the download client's name in Sonarr; "" when none is set
	State          arrapi.DownloadState   // "" for a held-back release and for a state this version of arrapi does not know
	Messages       []arrapi.StatusMessage // nil when Sonarr has none
	EpisodeIDs     []int                  // the episodes Sonarr matched it to; nil when none
	SeriesID       int                    // 0 when Sonarr matched it to no series
	Size           int64                  // bytes; 0 when the client has not reported one
	SizeLeft       int64                  // bytes still to download
}

// downloadStateToken is a wire tracked download state: its string as sent, else "".
type downloadStateToken string

func (s *downloadStateToken) UnmarshalJSON(b []byte) error {
	v, err := stringToken(b, "trackedDownloadState", reflect.TypeFor[arrapi.DownloadState]())
	*s = downloadStateToken(v)
	return err
}

// queueBytes converts a queue size, a C# decimal decoded as float64, to bytes: 0 for
// NaN and anything not above 0, math.MaxInt64 from 2^63 up, where int64(v) is
// implementation-defined, else v rounded.
func queueBytes(v float64) int64 {
	switch {
	case math.IsNaN(v) || v <= 0:
		return 0
	case v >= float64(math.MaxInt64):
		return math.MaxInt64
	default:
		return int64(math.Round(v))
	}
}
