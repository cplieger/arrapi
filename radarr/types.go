package radarr

import (
	"math"
	"reflect"
	"slices"
	"time"

	"github.com/cplieger/arrapi/v3"
)

// Movie is a movie in the server's library.
type Movie struct {
	File             *MovieFile // nil when the movie has no file or the server sent none
	OriginalLanguage *arrapi.Language
	Title            string
	IDs              ExternalIDs
	AlternateTitles  []string
	Tags             []int
	ID               int
	Year             int
	HasFile          bool // wire hasFile is nullable; null reads false
	Monitored        bool // Radarr searches for and grabs this movie
}

// ExternalIDs are a movie's ids at the metadata providers.
type ExternalIDs struct {
	IMDb string // "tt" + digits; "" when none
	TMDB int    // 0 when unknown
}

// MovieFile is a movie's file on disk.
type MovieFile struct {
	MediaInfo    *arrapi.MediaInfo
	Revision     *arrapi.Revision
	RelativePath string
	Path         string
	SceneName    string
	ReleaseGroup string
	ID           int
	Size         int64
}

// Command is one command in Radarr's in-memory command list. The string fields hold
// the server's text; arrapi checks none, so a caller renders them through textsafe or
// an escaping template.
type Command struct {
	Queued   time.Time            // when Radarr queued it
	Started  time.Time            // zero until it starts
	Ended    time.Time            // zero until it ends
	Name     string               // the command class without its Command suffix: "RescanMovie", "RefreshMovie", "MoviesSearch"
	Message  string               // Radarr's latest progress or completion text; "" when none
	Status   arrapi.CommandStatus // "" for a status this version of arrapi does not know
	MovieIDs []int                // the movies its body names, from movieIds or movieId, in that order and without repeats; nil when it names none
	ID       int
	NewMovie bool // a RefreshMovie Radarr queued for a movie just added or imported
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

// QueueItem is one download in Radarr's queue, or one release Radarr holds back before
// sending it to a download client. The string fields hold the server's text.
type QueueItem struct {
	Title          string                 // the release title, file extension removed
	DownloadID     string                 // the download client's id for it, the DownloadID its history records carry; "" for a held-back release
	DownloadClient string                 // the download client's name in Radarr; "" when none is set
	State          arrapi.DownloadState   // "" for a held-back release and for a state this version of arrapi does not know
	Messages       []arrapi.StatusMessage // nil when Radarr has none
	MovieID        int                    // 0 when Radarr matched it to no movie
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
