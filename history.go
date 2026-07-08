package arrapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// EventType identifies a Sonarr/Radarr history event. Sonarr encodes it as an
// integer and Radarr as a string; UnmarshalJSON accepts either form.
type EventType int

const (
	// EventGrabbed is a release grabbed from an indexer.
	EventGrabbed EventType = 1
	// EventDownloadImported is a downloaded file imported into the library
	// (the "downloadFolderImported" event).
	EventDownloadImported EventType = 3
	// EventDownloadFailed is a failed download.
	EventDownloadFailed EventType = 4
	// EventFileDeleted is an episode-file or movie-file deletion.
	EventFileDeleted EventType = 5
	// EventFileRenamed is an episode-file or movie-file rename.
	EventFileRenamed EventType = 6
)

// radarrEventNames maps Radarr's string event types to their integer form.
// Both the movie* and episode* spellings are included so one map serves both
// services.
var radarrEventNames = map[string]EventType{
	"grabbed":                EventGrabbed,
	"downloadFolderImported": EventDownloadImported,
	"downloadFailed":         EventDownloadFailed,
	"movieFileDeleted":       EventFileDeleted,
	"episodeFileDeleted":     EventFileDeleted,
	"movieFileRenamed":       EventFileRenamed,
	"episodeFileRenamed":     EventFileRenamed,
}

// UnmarshalJSON decodes the integer form (Sonarr) or the string form (Radarr).
// Unknown or negative values decode to 0 (unknown), which callers filter out;
// a value that is neither an int nor a string is an error.
func (e *EventType) UnmarshalJSON(data []byte) error {
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		if n < 0 {
			*e = 0
			return nil
		}
		*e = EventType(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("arrapi: eventType: expected int or string, got %s", data)
	}
	*e = radarrEventNames[s] // absent key yields 0 (unknown)
	return nil
}

// HistoryRecord is a single Sonarr or Radarr history event. Sonarr populates
// SeriesID/EpisodeID; Radarr populates MovieID. Data carries event-specific
// key/value detail (e.g. "importedPath").
type HistoryRecord struct {
	Date        time.Time         `json:"date"`
	Data        map[string]string `json:"data,omitempty"`
	SourceTitle string            `json:"sourceTitle,omitempty"`
	EventType   EventType         `json:"eventType"`
	ID          int               `json:"id"`
	SeriesID    int               `json:"seriesId,omitempty"`
	EpisodeID   int               `json:"episodeId,omitempty"`
	MovieID     int               `json:"movieId,omitempty"`
}

// ImportedPath returns the imported file path from a download-import event's
// data dictionary, or "" when absent.
func (h *HistoryRecord) ImportedPath() string { return h.Data["importedPath"] }

// GetHistorySince returns history records on or after since (the arr endpoint
// orders newest first). Pass an eventType to filter to a single kind of event,
// or 0 for all types. Available on both Sonarr and Radarr. It is not coalesced:
// each poll carries a distinct timestamp and must see fresh results.
func (c *client) GetHistorySince(ctx context.Context, since time.Time, eventType EventType) ([]HistoryRecord, error) {
	params := url.Values{}
	params.Set("date", since.UTC().Format(time.RFC3339))
	if eventType > 0 {
		params.Set("eventType", strconv.Itoa(int(eventType)))
	}
	params.Set("includeSeries", "false")
	params.Set("includeEpisode", "false")
	params.Set("includeMovie", "false")
	return fetchAll[HistoryRecord](ctx, c, apiPrefix+"/history/since?"+params.Encode())
}
