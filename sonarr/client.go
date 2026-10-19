package sonarr

import (
	"context"
	"slices"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/detect"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// Client reads one Sonarr server. It is safe for concurrent use.
type Client struct {
	t  *transport.Client
	s  *detect.Session[engine]
	v4 sonarrV4APIv3
	v5 sonarrV5APIv5
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
	c := &Client{t: t, v4: sonarrV4APIv3{t: t}, v5: sonarrV5APIv5{t: t}}
	c.s = detect.NewSession(t, detect.AppSonarr, c.engine)
	return c, nil
}

// engine names the engine that reads d's API; detection yields only APIv3 at major 4
// and APIv5 for Sonarr.
func (c *Client) engine(d *transport.Detection) engine {
	if d.API == arrapi.APIv5 {
		return c.v5
	}
	return c.v4
}

// Close releases idle connections; it is idempotent.
func (c *Client) Close() { c.t.Close() }

// via runs one read's attempts: Session.Read for a Client, Session.ReadPinned for a Pinned.
type via func(context.Context, func(context.Context, *transport.Detection, engine) transport.Result) error

// read runs one read through v and returns the value of the call's last attempt when
// err is nil (the zero value when that attempt reported the resource absent).
func read[T any](ctx context.Context, v via, f func(context.Context, *transport.Detection, engine) (T, transport.Result)) (T, error) {
	var out T
	err := v(ctx, func(ctx context.Context, d *transport.Detection, e engine) transport.Result {
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

// Series returns every series in the library.
func (c *Client) Series(ctx context.Context) ([]Series, error) { return readSeries(ctx, c.s.Read) }

// SeriesByID returns the series with id; (zero, false, nil) when the server has none.
func (c *Client) SeriesByID(ctx context.Context, id int) (Series, bool, error) {
	return readSeriesByID(ctx, c.s.Read, id)
}

// Episodes returns the series' episodes, Episode.File nil.
func (c *Client) Episodes(ctx context.Context, seriesID int) ([]Episode, error) {
	return readEpisodes(ctx, c.s.Read, seriesID, false)
}

// EpisodesWithFiles returns the series' episodes with Episode.File set where HasFile.
func (c *Client) EpisodesWithFiles(ctx context.Context, seriesID int) ([]Episode, error) {
	return readEpisodes(ctx, c.s.Read, seriesID, true)
}

// EpisodeFiles returns the series' episode files.
func (c *Client) EpisodeFiles(ctx context.Context, seriesID int) ([]EpisodeFile, error) {
	return readEpisodeFiles(ctx, c.s.Read, seriesID)
}

// HistorySince returns, in one request, every history record dated at or after since
// (sent at second precision), oldest first, keeping only the listed types when any are
// given; EventType(0) matches nothing. Sonarr inner-joins each record with its series
// and its episode, so a deleted series' or episode's records are not returned: they
// vanish with it, and a later call never brings them back.
func (c *Client) HistorySince(ctx context.Context, since time.Time, types ...EventType) ([]HistoryRecord, error) {
	return readHistorySince(ctx, c.s.Read, since, types)
}

// Tags returns every tag.
func (c *Client) Tags(ctx context.Context) ([]arrapi.Tag, error) { return readTags(ctx, c.s.Read) }

// RootFolders returns the path of every root folder.
func (c *Client) RootFolders(ctx context.Context) ([]string, error) {
	return readRootFolders(ctx, c.s.Read)
}

func readSeries(ctx context.Context, v via) ([]Series, error) {
	return read(ctx, v, func(ctx context.Context, d *transport.Detection, e engine) ([]Series, transport.Result) {
		return e.series(ctx, d)
	})
}

func readSeriesByID(ctx context.Context, v via, id int) (Series, bool, error) {
	type found struct {
		s  Series
		ok bool
	}
	f, err := read(ctx, v, func(ctx context.Context, d *transport.Detection, e engine) (found, transport.Result) {
		s, r := e.seriesByID(ctx, d, id)
		return found{s, r.Accepted()}, r
	})
	return f.s, f.ok, err
}

func readEpisodes(ctx context.Context, v via, seriesID int, withFiles bool) ([]Episode, error) {
	return read(ctx, v, func(ctx context.Context, d *transport.Detection, e engine) ([]Episode, transport.Result) {
		return e.episodes(ctx, d, seriesID, withFiles)
	})
}

func readEpisodeFiles(ctx context.Context, v via, seriesID int) ([]EpisodeFile, error) {
	return read(ctx, v, func(ctx context.Context, d *transport.Detection, e engine) ([]EpisodeFile, transport.Result) {
		return e.episodeFiles(ctx, d, seriesID)
	})
}

func readHistorySince(ctx context.Context, v via, since time.Time, types []EventType) ([]HistoryRecord, error) {
	recs, err := read(ctx, v, func(ctx context.Context, d *transport.Detection, e engine) ([]HistoryRecord, transport.Result) {
		return e.historySince(ctx, d, since)
	})
	if err != nil || len(types) == 0 {
		return recs, err
	}
	return slices.DeleteFunc(recs, func(r HistoryRecord) bool {
		return r.EventType == 0 || !slices.Contains(types, r.EventType)
	}), nil
}

func readTags(ctx context.Context, v via) ([]arrapi.Tag, error) {
	return read(ctx, v, func(ctx context.Context, d *transport.Detection, e engine) ([]arrapi.Tag, transport.Result) {
		return e.tags(ctx, d)
	})
}

func readRootFolders(ctx context.Context, v via) ([]string, error) {
	return read(ctx, v, func(ctx context.Context, d *transport.Detection, e engine) ([]string, transport.Result) {
		return e.rootFolders(ctx, d)
	})
}
