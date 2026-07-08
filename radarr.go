package arrapi

import "context"

// Radarr is a client for a single Radarr v3 instance. The zero value is not
// usable; construct one with NewRadarr. A Radarr is safe for concurrent use.
type Radarr struct {
	*client
}

// NewRadarr returns a Radarr client for the given base URL (e.g.
// "http://radarr:7878") and API key. It returns an error if the URL is not an
// absolute http(s) URL or the key is empty.
func NewRadarr(baseURL, apiKey string, opts ...Option) (*Radarr, error) {
	c, err := newClient(baseURL, apiKey, opts...)
	if err != nil {
		return nil, err
	}
	return &Radarr{client: c}, nil
}

// GetMovies returns every movie in the Radarr library.
func (r *Radarr) GetMovies(ctx context.Context) ([]Movie, error) {
	return doSingleflight(ctx, r.client, "movies", func(fctx context.Context) ([]Movie, error) {
		return fetchAll[Movie](fctx, r.client, apiPrefix+"/movie")
	})
}
