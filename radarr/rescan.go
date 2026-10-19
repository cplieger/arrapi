package radarr

import (
	"context"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

// RescanMovie asks the server to rescan the movie's files. It runs a fresh detection,
// then sends the command at most once and never through a redirect; a 2xx not
// attributable to the server wraps arrapi.ErrCommandOutcomeUnknown, since the server
// may have run it.
func (c *Client) RescanMovie(ctx context.Context, movieID int) error {
	return c.s.Mutate(ctx, func(ctx context.Context, d *transport.Detection, e radarrAPIv3) transport.Result {
		return e.rescan(ctx, d, movieID)
	})
}
