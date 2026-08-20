package arrapi

import (
	"context"
	"fmt"
)

// Sonarr is a client for a single Sonarr v3 instance. The zero value is not
// usable; construct one with NewSonarr. A Sonarr is safe for concurrent use.
type Sonarr struct {
	*client
}

// NewSonarr returns a Sonarr client for the given base URL (e.g.
// "http://sonarr:8989") and API key. It returns an error if the URL is not an
// absolute http(s) URL or the key is empty.
//
// The key is an [APIKey] rather than a string so it cannot be transposed with
// the base URL; see that type for why arrapi declares its own credential type
// instead of exposing httpx's.
func NewSonarr(baseURL string, apiKey APIKey, opts ...Option) (*Sonarr, error) {
	c, err := newClient(baseURL, apiKey, opts...)
	if err != nil {
		return nil, err
	}
	return &Sonarr{client: c}, nil
}

// Series returns every series in the Sonarr library.
func (s *Sonarr) Series(ctx context.Context) ([]Series, error) {
	return s.fetchAll[Series](ctx, apiPrefix+"/series")
}

// Episodes returns all episodes for the given series, including
// episode-file details (release group, size, media info) where present.
func (s *Sonarr) Episodes(ctx context.Context, seriesID int) ([]Episode, error) {
	path := fmt.Sprintf("%s/episode?seriesId=%d&includeEpisodeFile=true", apiPrefix, seriesID)
	return s.fetchAll[Episode](ctx, path)
}

// EpisodeFiles returns the episode files for the given series, from the
// dedicated episodefile endpoint. It yields exactly the episodes that have a
// file on disk — the same file details Episodes embeds, without the
// fileless episode rows — so a consumer that reads only episodes with files
// (such as a library walker aggregating release groups per season) gets a
// smaller payload on a long airing series at the same request count. Each
// file carries its SeriesID and SeasonNumber, so no episode list is needed to
// attribute it.
func (s *Sonarr) EpisodeFiles(ctx context.Context, seriesID int) ([]EpisodeFile, error) {
	path := fmt.Sprintf("%s/episodefile?seriesId=%d", apiPrefix, seriesID)
	return s.fetchAll[EpisodeFile](ctx, path)
}

// SeriesByID returns the single series with the given Sonarr ID. It returns
// a *StatusError for which IsNotFound reports true when no series has that ID.
func (s *Sonarr) SeriesByID(ctx context.Context, seriesID int) (Series, error) {
	return s.fetchOne[Series](ctx, fmt.Sprintf("%s/series/%d", apiPrefix, seriesID))
}

// EpisodeByID returns the single episode with the given Sonarr ID. It
// returns a *StatusError for which IsNotFound reports true when no episode has
// that ID.
func (s *Sonarr) EpisodeByID(ctx context.Context, episodeID int) (Episode, error) {
	return s.fetchOne[Episode](ctx, fmt.Sprintf("%s/episode/%d", apiPrefix, episodeID))
}
