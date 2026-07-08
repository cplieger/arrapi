package arrapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cplieger/httpx/v2"
)

// Command names understood by the Sonarr/Radarr /command endpoint. Rescan looks
// for new or changed files on disk; Refresh re-fetches metadata (and rescans).
const (
	cmdRescanSeries  = "RescanSeries"
	cmdRefreshSeries = "RefreshSeries"
	cmdRescanMovie   = "RescanMovie"
	cmdRefreshMovie  = "RefreshMovie"
)

// commandBody is the JSON request body for the /command endpoint. Only the
// fields the modeled commands use are present.
type commandBody struct {
	Name     string `json:"name"`
	SeriesID int    `json:"seriesId,omitempty"`
	MovieID  int    `json:"movieId,omitempty"`
}

// RescanSeries asks Sonarr to rescan the series' folder for new or changed
// files (for example after an external tool wrote a subtitle). It does not
// re-fetch metadata; use RefreshSeries for that.
func (s *Sonarr) RescanSeries(ctx context.Context, seriesID int) error {
	return s.postCommand(ctx, commandBody{Name: cmdRescanSeries, SeriesID: seriesID})
}

// RefreshSeries asks Sonarr to refresh the series' metadata and rescan its
// files.
func (s *Sonarr) RefreshSeries(ctx context.Context, seriesID int) error {
	return s.postCommand(ctx, commandBody{Name: cmdRefreshSeries, SeriesID: seriesID})
}

// RescanMovie asks Radarr to rescan the movie's folder for new or changed files.
func (r *Radarr) RescanMovie(ctx context.Context, movieID int) error {
	return r.postCommand(ctx, commandBody{Name: cmdRescanMovie, MovieID: movieID})
}

// RefreshMovie asks Radarr to refresh the movie's metadata and rescan its files.
func (r *Radarr) RefreshMovie(ctx context.Context, movieID int) error {
	return r.postCommand(ctx, commandBody{Name: cmdRefreshMovie, MovieID: movieID})
}

// postCommand sends a command to the instance's /command endpoint. Commands are
// mutations, so it does not retry. A non-2xx response is returned as a
// *StatusError; the endpoint replies 201 Created on success.
func (c *client) postCommand(ctx context.Context, body commandBody) error {
	if _, ok := ctx.Deadline(); !ok && c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("arrapi: marshal command %s: %w", body.Name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+apiPrefix+"/command", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("arrapi: build command %s: %w", body.Name, err)
	}
	req.Header.Set(headerAPIKey, c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("arrapi: post command %s: %w", body.Name, err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return statusError(resp, apiPrefix+"/command")
	}
	httpx.DrainClose(resp.Body)
	return nil
}
