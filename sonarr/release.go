package sonarr

import (
	"context"
	"errors"

	"github.com/cplieger/arrapi/v3/internal/grabfail"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

var (
	errReleaseSeriesID  = errors.New("arrapi: a release search needs a series id of 1 or more")
	errReleaseSeason    = errors.New("arrapi: a release search needs a season number of 0 or more")
	errReleaseEpisodeID = errors.New("arrapi: a release search needs an episode id of 1 or more")
	errGrabGUID         = errors.New("arrapi: a grab needs the release's guid")
	errGrabIndexer      = errors.New("arrapi: a grab needs an indexer id of 1 or more")
)

// Release is one result of Sonarr's interactive search, as Sonarr decided it at search
// time. It holds no download or magnet link: Sonarr's answer carries both, and either
// can embed an indexer's or tracker's secret, so neither is decoded. InfoURL, CommentURL
// and GUID are the indexer's own values, and a GUID can be a link carrying the
// indexer's passkey, so log none of them.
type Release struct {
	GUID                string           // the indexer's id for the release; with IndexerID, what Grab names
	Indexer             string           // the indexer's name in Sonarr
	Title               string           // the release title
	Quality             string           // the quality's name ("Bluray-1080p"); "" when Sonarr parsed none
	InfoHash            string           // "" for a Usenet release or an indexer that sends none
	InfoURL             string           // "" when absent
	CommentURL          string           // "" when absent
	Rejections          []string         // Sonarr's reasons, in its order; nil when approved
	MappedEpisodes      []ReleaseEpisode // the episodes Sonarr mapped the title onto, in its order; nil when none
	Size                int64            // bytes
	IndexerID           int
	CustomFormatScore   int
	MappedSeriesID      int          // 0 when Sonarr mapped the title onto no series
	Weight              int          // Sonarr's rank: 0 is its best; the answer is in this order
	Flags               IndexerFlags // the indexer's flags, as Sonarr's bits
	Approved            bool         // Sonarr would grab it
	TemporarilyRejected bool         // Sonarr would hold it (a delay profile, say)
	Rejected            bool         // Sonarr would not grab it; Rejections says why
}

// ReleaseEpisode is one episode a release was mapped onto.
type ReleaseEpisode struct {
	ID, SeasonNumber, EpisodeNumber int
	Absolute                        int // 0 when the episode has no absolute number
}

// IndexerFlags is Sonarr's indexer flag bits (NzbDrone.Core/Parser/Model/IndexerFlags.cs).
type IndexerFlags uint32

// The flags Sonarr defines. Subtitles is Sonarr 5's; Sonarr 4 never sets it.
const (
	FlagFreeleech    IndexerFlags = 1
	FlagHalfleech    IndexerFlags = 2
	FlagDoubleUpload IndexerFlags = 4
	FlagInternal     IndexerFlags = 8
	FlagScene        IndexerFlags = 16
	FlagFreeleech75  IndexerFlags = 32
	FlagFreeleech25  IndexerFlags = 64
	FlagNuked        IndexerFlags = 128
	FlagSubtitles    IndexerFlags = 256
)

// SeasonReleases runs Sonarr's interactive search for one season and returns every
// result Sonarr decided, best first. The search queries each of Sonarr's enabled
// indexers, so it is sent at most once: no failure is retried, and an answer from
// another server version is a *arrapi.ServerChangedError, not a search re-sent there.
// It can take minutes; each attempt ends at ctx's deadline, else at the client's
// attempt timeout (WithTimeout). Sonarr keeps each result 30 minutes for Grab. A
// season.SeriesID below 1 or a season.Number below 0 is refused before any request.
func (c *Client) SeasonReleases(ctx context.Context, season SeasonRef) ([]Release, error) {
	switch {
	case season.SeriesID < 1:
		return nil, errReleaseSeriesID
	case season.Number < 0:
		return nil, errReleaseSeason
	}
	return c.releases(ctx, releaseQuery{SeriesID: season.SeriesID, SeasonNumber: season.Number})
}

// EpisodeReleases is SeasonReleases for one episode. An episodeID below 1 is refused
// before any request.
func (c *Client) EpisodeReleases(ctx context.Context, episodeID int) ([]Release, error) {
	if episodeID < 1 {
		return nil, errReleaseEpisodeID
	}
	return c.releases(ctx, releaseQuery{EpisodeID: episodeID, Episode: true})
}

func (c *Client) releases(ctx context.Context, q releaseQuery) ([]Release, error) {
	var out []Release
	err := c.s.Mutate(ctx, func(ctx context.Context, d *transport.Detection, e engine) transport.Result {
		var r transport.Result
		out, r = e.releases(ctx, d, q)
		return r
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Grab asks Sonarr to grab one release from an interactive search it ran in the last
// 30 minutes, named by the release's GUID and IndexerID, through Sonarr's own download
// client and history. It runs a fresh detection, then sends the request at most once and
// never through a redirect; a 2xx not attributable to the server wraps
// arrapi.ErrCommandOutcomeUnknown. A refusal Sonarr explains is a *arrapi.GrabError
// whose Reason says why; Sonarr 5's GrabUnavailable message quotes Sonarr's download
// error. A refusal's text is masked from the first magnet link it quotes to its end.
// An empty guid or an indexerID below 1 is refused before any request.
func (c *Client) Grab(ctx context.Context, guid string, indexerID int) error {
	switch {
	case guid == "":
		return errGrabGUID
	case indexerID < 1:
		return errGrabIndexer
	}
	g := releaseGrab{GUID: guid, IndexerID: indexerID}
	return c.s.Mutate(ctx, func(ctx context.Context, d *transport.Detection, e engine) transport.Result {
		r := e.grab(ctx, d, g)
		// Classified here, on the grab's own answer, so a detection probe's refusal never is.
		r.Err = grabfail.Classify(r.Err)
		return r
	})
}

// releaseEpisodes maps a release's mappedEpisodeInfo; nil when it lists none.
func releaseEpisodes(w []sonarrReleaseEpisode) []ReleaseEpisode {
	if len(w) == 0 {
		return nil
	}
	out := make([]ReleaseEpisode, len(w))
	for i, e := range w {
		out[i] = ReleaseEpisode{ID: e.ID, SeasonNumber: e.SeasonNumber, EpisodeNumber: e.EpisodeNumber, Absolute: e.AbsoluteEpisodeNumber}
	}
	return out
}
