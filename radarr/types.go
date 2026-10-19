package radarr

import "github.com/cplieger/arrapi/v3"

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
