package arrapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// EventType identifies a Sonarr/Radarr history event. Its integer values follow
// Sonarr's HistoryEventType enum; Radarr's string encoding is mapped to the
// same semantic values (see eventTypeByName). Decode with UnmarshalJSON, which
// accepts either the integer (Sonarr) or the string (Radarr) form.
type EventType int

const (
	// EventGrabbed is a release grabbed from an indexer.
	EventGrabbed EventType = 1
	// EventFolderImported is a download folder imported at the series or movie
	// level (Sonarr "seriesFolderImported", Radarr "movieFolderImported").
	EventFolderImported EventType = 2
	// EventDownloadImported is a downloaded file imported into the library
	// (the "downloadFolderImported" event).
	EventDownloadImported EventType = 3
	// EventDownloadFailed is a failed download.
	EventDownloadFailed EventType = 4
	// EventFileDeleted is an episode-file or movie-file deletion.
	EventFileDeleted EventType = 5
	// EventFileRenamed is an episode-file or movie-file rename.
	EventFileRenamed EventType = 6
	// EventDownloadIgnored is a grabbed download that was ignored.
	EventDownloadIgnored EventType = 7
)

// eventTypeByName maps both services' string event names to the semantic
// EventType. Sonarr (episode*/series*) and Radarr (movie*) spellings are both
// included so one map serves either service.
var eventTypeByName = map[string]EventType{
	"grabbed":                EventGrabbed,
	"seriesFolderImported":   EventFolderImported,
	"movieFolderImported":    EventFolderImported,
	"downloadFolderImported": EventDownloadImported,
	"downloadFailed":         EventDownloadFailed,
	"episodeFileDeleted":     EventFileDeleted,
	"movieFileDeleted":       EventFileDeleted,
	"episodeFileRenamed":     EventFileRenamed,
	"movieFileRenamed":       EventFileRenamed,
	"downloadIgnored":        EventDownloadIgnored,
}

// UnmarshalJSON decodes the integer form (Sonarr) or the string form (Radarr).
// The integer path uses Sonarr's numbering, which is safe because Radarr
// encodes the event as a string. An unknown string or negative integer decodes
// to 0 (unknown); HistoryRecord preserves the raw token in RawEventType.
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
	*e = eventTypeByName[s] // absent key yields 0 (unknown)
	return nil
}

// String returns a stable, human-readable name for the event type, suitable
// for structured logs. Known types return their canonical arr name; any other
// value (including the zero/unknown type) returns the Go-style EventType(<n>).
func (e EventType) String() string {
	switch e {
	case EventGrabbed:
		return "grabbed"
	case EventFolderImported:
		return "folderImported"
	case EventDownloadImported:
		return "downloadFolderImported"
	case EventDownloadFailed:
		return "downloadFailed"
	case EventFileDeleted:
		return "fileDeleted"
	case EventFileRenamed:
		return "fileRenamed"
	case EventDownloadIgnored:
		return "downloadIgnored"
	default:
		return fmt.Sprintf("EventType(%d)", int(e))
	}
}

// HistoryRecord is a single Sonarr or Radarr history event. Sonarr populates
// SeriesID/EpisodeID; Radarr populates MovieID. Data carries event-specific
// key/value detail (e.g. "importedPath"). RawEventType holds the original wire
// token when the event is one arrapi does not model (EventType is then 0).
type HistoryRecord struct {
	Date         time.Time         `json:"date"`
	Data         map[string]string `json:"data"`
	SourceTitle  string            `json:"sourceTitle"`
	RawEventType string            `json:"-"`
	EventType    EventType         `json:"eventType"`
	ID           int               `json:"id"`
	SeriesID     int               `json:"seriesId"`
	EpisodeID    int               `json:"episodeId"`
	MovieID      int               `json:"movieId"`
}

// UnmarshalJSON decodes a history record and, when the event is not one arrapi
// models, preserves the raw eventType token in RawEventType so a new upstream
// event stays identifiable in logs rather than collapsing silently to 0.
func (h *HistoryRecord) UnmarshalJSON(data []byte) error {
	type alias HistoryRecord
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*h = HistoryRecord(a)
	if h.EventType == 0 {
		var raw struct {
			EventType json.RawMessage `json:"eventType"`
		}
		if json.Unmarshal(data, &raw) == nil {
			h.RawEventType = strings.Trim(string(raw.EventType), `"`)
		}
	}
	return nil
}

// ImportedPath returns the imported file path from a download-import event's
// data dictionary, or "" when absent.
func (h *HistoryRecord) ImportedPath() string { return h.Data["importedPath"] }

// GetHistorySince returns history records on or after since (the arr endpoint
// orders newest first). Pass one or more event types to filter the result;
// pass none to return every type. Available on both Sonarr and Radarr.
//
// Filtering is client-side: the arr eventType query parameter is numbered per
// service (Sonarr and Radarr disagree on the integers), so a server-side filter
// is not portable. Note that /history/since is unbounded, so a wide since
// window can return a large payload (subject to the response size cap); use
// GetHistory for bounded, paged scans.
func (c *client) GetHistorySince(ctx context.Context, since time.Time, eventTypes ...EventType) ([]HistoryRecord, error) {
	params := url.Values{}
	params.Set("date", since.UTC().Format(time.RFC3339))
	params.Set("includeSeries", "false")
	params.Set("includeEpisode", "false")
	params.Set("includeMovie", "false")

	recs, err := fetchAll[HistoryRecord](ctx, c, apiPrefix+"/history/since?"+params.Encode())
	if err != nil {
		return nil, err
	}
	return filterByEventType(recs, eventTypes), nil
}

// HistoryPage is one page from the paged /history endpoint (newest first).
type HistoryPage struct {
	Records      []HistoryRecord `json:"records"`
	Page         int             `json:"page"`
	PageSize     int             `json:"pageSize"`
	TotalRecords int             `json:"totalRecords"`
}

// HistoryOptions parameterizes a paged GetHistory call. A zero Page or PageSize
// uses the arr default (page 1; the server's default page size).
type HistoryOptions struct {
	Page     int
	PageSize int
}

// GetHistory returns one page of history from the paged /history endpoint,
// newest first. Unlike GetHistorySince it is bounded by page size, so it suits
// backfills and large scans. Filter the returned records by EventType
// client-side; the arr eventType query parameter is numbered per service and is
// not portable. Available on both Sonarr and Radarr.
func (c *client) GetHistory(ctx context.Context, opts HistoryOptions) (HistoryPage, error) {
	params := url.Values{}
	if opts.Page > 0 {
		params.Set("page", strconv.Itoa(opts.Page))
	}
	if opts.PageSize > 0 {
		params.Set("pageSize", strconv.Itoa(opts.PageSize))
	}
	params.Set("sortKey", "date")
	params.Set("sortDirection", "descending")
	return fetchOne[HistoryPage](ctx, c, apiPrefix+"/history?"+params.Encode())
}

// filterByEventType returns the records whose EventType is among want, matching
// on the decoded, service-agnostic type. A nil or empty want returns recs
// unchanged. It filters in place, which is safe because recs is freshly decoded
// and owned by the caller (arrapi does not share decoded slices across calls).
func filterByEventType(recs []HistoryRecord, want []EventType) []HistoryRecord {
	allow := make(map[EventType]struct{}, len(want))
	for _, et := range want {
		if et > 0 {
			allow[et] = struct{}{}
		}
	}
	if len(allow) == 0 {
		return recs
	}
	filtered := recs[:0]
	for _, r := range recs {
		if _, ok := allow[r.EventType]; ok {
			filtered = append(filtered, r)
		}
	}
	return filtered
}
