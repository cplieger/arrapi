package radarr

import (
	"context"
	"slices"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/detect"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// Client reads one Radarr server. It is safe for concurrent use.
type Client struct {
	t *transport.Client
	s *detect.Session[radarrAPIv3]
}

// New validates baseURL (absolute http(s), a host, no query or fragment; a path is
// allowed) and key (not empty or whitespace; only letters, digits, '.', '_' and '-',
// as every key the server generates is). It makes no request and starts no
// goroutine. A validation error names the rule, never the URL or the key.
func New(baseURL string, key arrapi.APIKey, opts ...Option) (*Client, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	t, err := transport.New(baseURL, key, o.cfg)
	if err != nil {
		return nil, err
	}
	e := radarrAPIv3{t: t}
	return &Client{t: t, s: detect.NewSession(t, detect.AppRadarr, func(*transport.Detection) radarrAPIv3 { return e })}, nil
}

// Close releases idle connections; it is idempotent.
func (c *Client) Close() { c.t.Close() }

// read runs one read through the session and returns the value of the call's last
// attempt when err is nil (the zero value when that attempt reported the resource absent).
func read[T any](ctx context.Context, c *Client, f func(context.Context, *transport.Detection, radarrAPIv3) (T, transport.Result)) (T, error) {
	var out T
	err := c.s.Read(ctx, func(ctx context.Context, d *transport.Detection, e radarrAPIv3) transport.Result {
		var r transport.Result
		out, r = f(ctx, d, e)
		return r
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

// Movies returns every movie in the library.
func (c *Client) Movies(ctx context.Context) ([]Movie, error) {
	return read(ctx, c, func(ctx context.Context, d *transport.Detection, e radarrAPIv3) ([]Movie, transport.Result) {
		return e.movies(ctx, d)
	})
}

// MovieByID returns the movie with id; (zero, false, nil) when the server has none.
func (c *Client) MovieByID(ctx context.Context, id int) (Movie, bool, error) {
	type found struct {
		m  Movie
		ok bool
	}
	f, err := read(ctx, c, func(ctx context.Context, d *transport.Detection, e radarrAPIv3) (found, transport.Result) {
		m, r := e.movieByID(ctx, d, id)
		return found{m, r.Accepted()}, r
	})
	return f.m, f.ok, err
}

// HistorySince returns, in one request, every history record dated at or after since
// (sent at second precision), oldest first, keeping only the listed types when any are
// given; EventType(0) matches nothing. Radarr inner-joins each record with its movie,
// so a deleted movie's records are not returned: they vanish with it, and a later call
// never brings them back.
func (c *Client) HistorySince(ctx context.Context, since time.Time, types ...EventType) ([]HistoryRecord, error) {
	recs, err := read(ctx, c, func(ctx context.Context, d *transport.Detection, e radarrAPIv3) ([]HistoryRecord, transport.Result) {
		return e.historySince(ctx, d, since)
	})
	if err != nil || len(types) == 0 {
		return recs, err
	}
	return slices.DeleteFunc(recs, func(r HistoryRecord) bool {
		return r.EventType == 0 || !slices.Contains(types, r.EventType)
	}), nil
}

// Tags returns every tag.
func (c *Client) Tags(ctx context.Context) ([]arrapi.Tag, error) {
	return read(ctx, c, func(ctx context.Context, d *transport.Detection, e radarrAPIv3) ([]arrapi.Tag, transport.Result) {
		return e.tags(ctx, d)
	})
}

// RootFolders returns the path of every root folder.
func (c *Client) RootFolders(ctx context.Context) ([]string, error) {
	return read(ctx, c, func(ctx context.Context, d *transport.Detection, e radarrAPIv3) ([]string, transport.Result) {
		return e.rootFolders(ctx, d)
	})
}
