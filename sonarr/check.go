package sonarr

import (
	"context"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/detect"
)

// Check tests a connection a setup form has not saved: it validates baseURL and key as
// New does, runs the fresh detection Info runs (one attempt per request, at most 5 s
// in all) and reports what it found. A failure is an *arrapi.CheckError naming what
// to fix, unless ctx has ended when Check fails: then it returns ctx.Err(), even for
// a baseURL or key New refuses. It keeps no client. WithHTTPClient, WithLogger and
// WithLoggedErrorText apply as in New; WithMaxAttempts, WithBaseDelay and WithTimeout
// have no effect.
func Check(ctx context.Context, baseURL string, key arrapi.APIKey, opts ...Option) (Info, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	c, err := New(baseURL, key, opts...)
	if err != nil {
		return Info{}, detect.CheckFailure(ctx, err)
	}
	// Close also drops the idle connections of a caller's transport.
	if o.cfg.HTTPClient == nil {
		defer c.Close()
	}
	d, err := c.s.Check(ctx)
	if err != nil {
		return Info{}, err
	}
	return c.info(d), nil
}
