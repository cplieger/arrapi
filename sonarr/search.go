package sonarr

import (
	"context"
	"errors"
	"slices"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

var (
	errSearchSeriesID = errors.New("arrapi: a search needs a series id of 1 or more")
	errSearchSeason   = errors.New("arrapi: a season search needs a season number of 0 or more")
	errSearchEpisodes = errors.New("arrapi: an episode search needs at least one episode id, each 1 or more")
)

// SeriesSearch asks Sonarr to search its indexers for the series' wanted episodes, in
// its monitored seasons, and grab what the series' quality profile accepts. It is sent
// as RescanSeries is, at most once, so an unattributable 2xx wraps
// arrapi.ErrCommandOutcomeUnknown. It returns the id of the command that runs the
// search (an equal one's when already queued or running), or 0 when the accepted answer
// held no command id of 1 or more, which the client's logger records. A seriesID below
// 1 is refused before any request.
func (c *Client) SeriesSearch(ctx context.Context, seriesID int) (int, error) {
	if seriesID < 1 {
		return 0, errSearchSeriesID
	}
	return c.search(ctx, searchCommand{Name: "SeriesSearch", SeriesID: seriesID})
}

// SeasonSearch is SeriesSearch for one season. A season.SeriesID below 1 or a
// season.Number below 0 is refused before any request.
func (c *Client) SeasonSearch(ctx context.Context, season SeasonRef) (int, error) {
	switch {
	case season.SeriesID < 1:
		return 0, errSearchSeriesID
	case season.Number < 0:
		return 0, errSearchSeason
	}
	return c.search(ctx, searchCommand{Name: "SeasonSearch", SeriesID: season.SeriesID, SeasonNumber: &season.Number})
}

// EpisodeSearch is SeriesSearch for the listed episodes, which Sonarr searches one by
// one. It needs at least one id, each 1 or more; any other list is refused before any
// request. The ids are sent as given.
func (c *Client) EpisodeSearch(ctx context.Context, episodeIDs []int) (int, error) {
	if len(episodeIDs) == 0 || slices.ContainsFunc(episodeIDs, func(id int) bool { return id < 1 }) {
		return 0, errSearchEpisodes
	}
	return c.search(ctx, searchCommand{Name: "EpisodeSearch", EpisodeIDs: episodeIDs})
}

func (c *Client) search(ctx context.Context, cmd searchCommand) (int, error) {
	var id int
	err := c.s.Mutate(ctx, func(ctx context.Context, d *transport.Detection, e engine) transport.Result {
		var r transport.Result
		id, r = e.search(ctx, d, cmd)
		return r
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}
