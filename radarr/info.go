package radarr

import (
	"context"

	"github.com/cplieger/arrapi/v3"
)

// Info is what a detection found.
type Info struct {
	Version    string     // system/status "version", e.g. "6.4.4.10685", single-line, at most 64 bytes
	API        arrapi.API // APIv3: Radarr (API v3)
	Deprecated bool       // the server lists API v3 in /api "deprecated"
}

// Info runs a fresh detection, one attempt per probe and at most 5 s in total, and
// reports what it found. Later calls use that detection, or an equal one already
// installed, while it remains current.
func (c *Client) Info(ctx context.Context) (Info, error) {
	d, err := c.s.Info(ctx)
	if err != nil {
		return Info{}, err
	}
	return Info{Version: d.Version, API: d.API, Deprecated: d.Deprecated}, nil
}
