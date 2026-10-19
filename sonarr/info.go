package sonarr

import (
	"context"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// Info is what a detection found.
type Info struct {
	Version    string     // system/status "version", e.g. "5.0.0.2635", single-line, at most 64 bytes
	API        arrapi.API // APIv3: Sonarr v4 (API v3); APIv5: Sonarr v5 (API v5)
	Deprecated bool       // the server lists API in /api "deprecated"
	Features   Features
}

// Info runs a fresh detection, one attempt per probe and at most 5 s in total, and
// reports what it found. Later calls use that detection, or an equal one already
// installed, while it remains current.
func (c *Client) Info(ctx context.Context) (Info, error) {
	d, err := c.s.Info(ctx)
	if err != nil {
		return Info{}, err
	}
	return c.info(d), nil
}

func (c *Client) info(d *transport.Detection) Info {
	return Info{Version: d.Version, API: d.API, Deprecated: d.Deprecated, Features: c.engine(d).features()}
}
