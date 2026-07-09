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
func NewSonarr(baseURL, apiKey string, opts ...Option) (*Sonarr, error) {
	c, err := newClient(baseURL, apiKey, opts...)
	if err != nil {
		return nil, err
	}
	return &Sonarr{client: c}, nil
}

// GetSeries returns every series in the Sonarr library.
func (s *Sonarr) GetSeries(ctx context.Context) ([]Series, error) {
	return fetchAll[Series](ctx, s.client, apiPrefix+"/series")
}

// GetEpisodes returns all episodes for the given series, including
// episode-file details (release group, size, media info) where present.
func (s *Sonarr) GetEpisodes(ctx context.Context, seriesID int) ([]Episode, error) {
	path := fmt.Sprintf("%s/episode?seriesId=%d&includeEpisodeFile=true", apiPrefix, seriesID)
	return fetchAll[Episode](ctx, s.client, path)
}

// GetSeriesByID returns the single series with the given Sonarr ID. It returns
// a *StatusError for which IsNotFound reports true when no series has that ID.
func (s *Sonarr) GetSeriesByID(ctx context.Context, seriesID int) (Series, error) {
	return fetchOne[Series](ctx, s.client, fmt.Sprintf("%s/series/%d", apiPrefix, seriesID))
}

// GetEpisodeByID returns the single episode with the given Sonarr ID. It
// returns a *StatusError for which IsNotFound reports true when no episode has
// that ID.
func (s *Sonarr) GetEpisodeByID(ctx context.Context, episodeID int) (Episode, error) {
	return fetchOne[Episode](ctx, s.client, fmt.Sprintf("%s/episode/%d", apiPrefix, episodeID))
}
