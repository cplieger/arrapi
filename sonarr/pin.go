package sonarr

import (
	"context"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// Pin runs a fresh detection, each request under WithMaxAttempts and WithBaseDelay and
// bounded only by ctx, and returns a reader bound to the server version it found.
// Cost: after an answer without X-Application-Version to any call of c (on Sonarr 4 and
// Sonarr 5 before 5.0.0.2701, every error answer Sonarr's exception handler writes, such
// as a list read naming a missing series), each later Pinned read first re-detects or
// reuses a detection, within its own budget, and continues only on the pinned version.
// Any version change, a minor upgrade included, fails every Pinned read until an answer
// from the pinned version reaches c again.
func (c *Client) Pin(ctx context.Context) (*Pinned, error) {
	d, err := c.s.Pin(ctx)
	if err != nil {
		return nil, err
	}
	return &Pinned{c: c, pin: d}, nil
}

// Pinned reads one Sonarr server version: every value it returns was decoded from an
// answer carrying the version Pin detected, so values joined from several reads
// describe one server. A read that meets another version returns
// *arrapi.ServerChangedError and never switches engines; it retries, and re-detects the
// same version, as a Client read does. It is safe for concurrent use. Obtain one from
// Client.Pin.
type Pinned struct {
	c   *Client
	pin *transport.Detection
}

func (p *Pinned) read(ctx context.Context, call func(context.Context, *transport.Detection, engine) transport.Result) error {
	return p.c.s.ReadPinned(ctx, p.pin, call)
}

// Info reports what Pin's detection found, without a request. Its Features hold for
// every read of p.
func (p *Pinned) Info() Info { return p.c.info(p.pin) }

// Series is Client.Series on the pinned version.
func (p *Pinned) Series(ctx context.Context) ([]Series, error) { return readSeries(ctx, p.read) }

// SeriesByID is Client.SeriesByID on the pinned version.
func (p *Pinned) SeriesByID(ctx context.Context, id int) (Series, bool, error) {
	return readSeriesByID(ctx, p.read, id)
}

// Episodes is Client.Episodes on the pinned version.
func (p *Pinned) Episodes(ctx context.Context, seriesID int) ([]Episode, error) {
	return readEpisodes(ctx, p.read, seriesID, false)
}

// EpisodesWithFiles is Client.EpisodesWithFiles on the pinned version.
func (p *Pinned) EpisodesWithFiles(ctx context.Context, seriesID int) ([]Episode, error) {
	return readEpisodes(ctx, p.read, seriesID, true)
}

// EpisodeFiles is Client.EpisodeFiles on the pinned version.
func (p *Pinned) EpisodeFiles(ctx context.Context, seriesID int) ([]EpisodeFile, error) {
	return readEpisodeFiles(ctx, p.read, seriesID)
}

// HistorySince is Client.HistorySince on the pinned version.
func (p *Pinned) HistorySince(ctx context.Context, since time.Time, types ...EventType) ([]HistoryRecord, error) {
	return readHistorySince(ctx, p.read, since, types)
}

// Tags is Client.Tags on the pinned version.
func (p *Pinned) Tags(ctx context.Context) ([]arrapi.Tag, error) { return readTags(ctx, p.read) }

// RootFolders is Client.RootFolders on the pinned version.
func (p *Pinned) RootFolders(ctx context.Context) ([]string, error) {
	return readRootFolders(ctx, p.read)
}
