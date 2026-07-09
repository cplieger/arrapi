package arrapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Command names understood by the Sonarr/Radarr /command endpoint. Rescan looks
// for new or changed files on disk; Refresh re-fetches metadata (and rescans).
const (
	cmdRescanSeries  = "RescanSeries"
	cmdRefreshSeries = "RefreshSeries"
	cmdRescanMovie   = "RescanMovie"
	cmdRefreshMovie  = "RefreshMovie"
)

// Command is a Sonarr/Radarr command resource. The endpoint queues the command
// and returns it with an ID and a Status ("queued", "started", "completed", or
// "failed"); poll GetCommandByID to follow it to completion.
type Command struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	ID     int    `json:"id"`
}

// commandBody is the JSON request body for the /command endpoint. Only the
// fields the modeled commands use are present.
type commandBody struct {
	Name     string `json:"name"`
	SeriesID int    `json:"seriesId,omitempty"`
	MovieID  int    `json:"movieId,omitempty"`
}

// RescanSeries asks Sonarr to rescan the series' folder for new or changed
// files (for example after an external tool wrote a subtitle). It does not
// re-fetch metadata; use RefreshSeries for that. It returns the queued command.
func (s *Sonarr) RescanSeries(ctx context.Context, seriesID int) (Command, error) {
	return s.postCommand(ctx, commandBody{Name: cmdRescanSeries, SeriesID: seriesID})
}

// RefreshSeries asks Sonarr to refresh the series' metadata and rescan its
// files. It returns the queued command.
func (s *Sonarr) RefreshSeries(ctx context.Context, seriesID int) (Command, error) {
	return s.postCommand(ctx, commandBody{Name: cmdRefreshSeries, SeriesID: seriesID})
}

// RescanMovie asks Radarr to rescan the movie's folder for new or changed
// files. It returns the queued command.
func (r *Radarr) RescanMovie(ctx context.Context, movieID int) (Command, error) {
	return r.postCommand(ctx, commandBody{Name: cmdRescanMovie, MovieID: movieID})
}

// RefreshMovie asks Radarr to refresh the movie's metadata and rescan its
// files. It returns the queued command.
func (r *Radarr) RefreshMovie(ctx context.Context, movieID int) (Command, error) {
	return r.postCommand(ctx, commandBody{Name: cmdRefreshMovie, MovieID: movieID})
}

// GetCommandByID returns the current state of a previously issued command, so a
// caller can poll a rescan or refresh to completion. It returns a *StatusError
// for which IsNotFound reports true when no command has that ID. Available on
// both Sonarr and Radarr.
func (c *client) GetCommandByID(ctx context.Context, id int) (Command, error) {
	return fetchOne[Command](ctx, c, fmt.Sprintf("%s/command/%d", apiPrefix, id))
}

// postCommand sends a command to the instance's /command endpoint and decodes
// the returned command resource. Commands are mutations, so it does not retry.
// A non-2xx response is returned as a *StatusError; the endpoint replies 201
// Created on success.
func (c *client) postCommand(ctx context.Context, body commandBody) (Command, error) {
	var zero Command
	ctx, cancel := c.requestContext(ctx)
	defer cancel()

	data, err := json.Marshal(body)
	if err != nil {
		return zero, fmt.Errorf("arrapi: marshal command %s: %w", body.Name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+apiPrefix+"/command", bytes.NewReader(data))
	if err != nil {
		return zero, fmt.Errorf("arrapi: build command %s: %w", body.Name, err)
	}
	req.Header.Set(headerAPIKey, c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return zero, fmt.Errorf("arrapi: post command %s: %w", body.Name, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return zero, statusError(resp, apiPrefix+"/command", c.apiKey)
	}
	return decodeObject[Command](resp, apiPrefix+"/command")
}
