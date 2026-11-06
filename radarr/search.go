package radarr

import (
	"context"
	"errors"
	"slices"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

var errSearchMovies = errors.New("arrapi: a movie search needs at least one movie id, each 1 or more")

// MoviesSearch asks Radarr to search its indexers for the listed movies, monitored or
// not, and grab what each movie's quality profile accepts; the rest of its contract is
// sonarr.Client.SeriesSearch's. It needs at least one id, each 1 or more; any other
// list is refused before any request. Radarr skips an id it does not hold.
func (c *Client) MoviesSearch(ctx context.Context, movieIDs []int) (int, error) {
	if len(movieIDs) == 0 || slices.ContainsFunc(movieIDs, func(id int) bool { return id < 1 }) {
		return 0, errSearchMovies
	}
	var id int
	err := c.s.Mutate(ctx, func(ctx context.Context, d *transport.Detection, e radarrAPIv3) transport.Result {
		var r transport.Result
		id, r = e.moviesSearch(ctx, d, movieIDs)
		return r
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}
