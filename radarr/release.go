package radarr

import (
	"context"
	"errors"

	"github.com/cplieger/arrapi/v3/internal/grabfail"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

var (
	errReleaseMovieID = errors.New("arrapi: a release search needs a movie id of 1 or more")
	errGrabGUID       = errors.New("arrapi: a grab needs the release's guid")
	errGrabIndexer    = errors.New("arrapi: a grab needs an indexer id of 1 or more")
)

// Release is one result of Radarr's interactive search, as Radarr decided it at search
// time. It holds no download or magnet link: Radarr's answer carries both, and either
// can embed an indexer's or tracker's secret, so neither is decoded. InfoURL, CommentURL
// and GUID are the indexer's own values, and a GUID can be a link carrying the
// indexer's passkey, so log none of them.
type Release struct {
	GUID                string   // the indexer's id for the release; with IndexerID, what Grab names
	Indexer             string   // the indexer's name in Radarr
	Title               string   // the release title
	Quality             string   // the quality's name ("Bluray-1080p"); "" when Radarr parsed none
	InfoHash            string   // "" for a Usenet release or an indexer that sends none
	InfoURL             string   // "" when absent
	CommentURL          string   // "" when absent
	Rejections          []string // Radarr's reasons, in its order; nil when approved
	Size                int64    // bytes
	IndexerID           int
	CustomFormatScore   int
	MappedMovieID       int          // 0 when Radarr mapped the title onto no movie
	Weight              int          // Radarr's rank: 0 is its best; the answer is in this order
	Flags               IndexerFlags // the indexer's flags, as Radarr's bits, which differ from Sonarr's
	Approved            bool         // Radarr would grab it
	TemporarilyRejected bool         // Radarr would hold it (a delay profile, say)
	Rejected            bool         // Radarr would not grab it; Rejections says why
}

// IndexerFlags is Radarr's indexer flag bits (NzbDrone.Core/Parser/Model/IndexerFlags.cs).
// Radarr sends them as a list of names; the same name can hold another bit in Sonarr.
type IndexerFlags uint32

// The flags Radarr defines.
const (
	FlagFreeleech      IndexerFlags = 1    // G_Freeleech: the download does not count
	FlagHalfleech      IndexerFlags = 2    // G_Halfleech: the download counts 50%
	FlagDoubleUpload   IndexerFlags = 4    // G_DoubleUpload: the upload counts double
	FlagPTPGolden      IndexerFlags = 8    // PTP_Golden: a PTP staff high-quality encode
	FlagPTPApproved    IndexerFlags = 16   // PTP_Approved: checked by PTP staff
	FlagInternal       IndexerFlags = 32   // G_Internal: an internal release group
	FlagAHDInternal    IndexerFlags = 64   // AHD_Internal: obsolete in Radarr; an indexer may still send it
	FlagScene          IndexerFlags = 128  // G_Scene: from a scene group
	FlagFreeleech75    IndexerFlags = 256  // G_Freeleech75: the download counts 75%
	FlagFreeleech25    IndexerFlags = 512  // G_Freeleech25: the download counts 25%
	FlagAHDUserRelease IndexerFlags = 1024 // AHD_UserRelease: obsolete in Radarr; an indexer may still send it
	FlagNuked          IndexerFlags = 2048 // Nuked: the release is nuked
)

// MovieReleases runs Radarr's interactive search for the movie and returns every
// result Radarr decided, best first; the rest of its contract is
// sonarr.Client.SeasonReleases'. A movieID below 1 is refused before any request.
func (c *Client) MovieReleases(ctx context.Context, movieID int) ([]Release, error) {
	if movieID < 1 {
		return nil, errReleaseMovieID
	}
	var out []Release
	err := c.s.Mutate(ctx, func(ctx context.Context, d *transport.Detection, e radarrAPIv3) transport.Result {
		var r transport.Result
		out, r = e.releases(ctx, d, movieID)
		return r
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Grab (package radarr) is sonarr.Client.Grab for a release from MovieReleases.
func (c *Client) Grab(ctx context.Context, guid string, indexerID int) error {
	switch {
	case guid == "":
		return errGrabGUID
	case indexerID < 1:
		return errGrabIndexer
	}
	g := releaseGrab{GUID: guid, IndexerID: indexerID}
	return c.s.Mutate(ctx, func(ctx context.Context, d *transport.Detection, e radarrAPIv3) transport.Result {
		r := e.grab(ctx, d, g)
		// Classified here, on the grab's own answer, so a detection probe's refusal never is.
		r.Err = grabfail.Classify(r.Err)
		return r
	})
}
