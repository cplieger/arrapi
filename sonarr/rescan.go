package sonarr

import (
	"context"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

// RescanSeries asks the server to rescan the series' files. It runs a fresh detection,
// then sends the command at most once and never through a redirect; a 2xx not
// attributable to the server wraps arrapi.ErrCommandOutcomeUnknown, since the server
// may have run it.
func (c *Client) RescanSeries(ctx context.Context, seriesID int) error {
	return c.s.Mutate(ctx, func(ctx context.Context, d *transport.Detection, e engine) transport.Result {
		return e.rescan(ctx, d, seriesID)
	})
}
