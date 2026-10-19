package sonarr

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// EventType is a Sonarr history event, valued as Sonarr's EpisodeHistoryEventType.
type EventType int

// Values equal NzbDrone.Core/History/EpisodeHistory.cs's, the same on v4-develop and develop.
const (
	EventGrabbed          EventType = 1
	EventFolderImported   EventType = 2 // "seriesFolderImported"
	EventDownloadImported EventType = 3 // "downloadFolderImported"
	EventDownloadFailed   EventType = 4
	EventFileDeleted      EventType = 5 // "episodeFileDeleted"
	EventFileRenamed      EventType = 6 // "episodeFileRenamed"
	EventDownloadIgnored  EventType = 7
)

// eventTokens is EpisodeHistoryEventType's wire token per value, identical on Sonarr v4
// (API v3) and Sonarr v5 (API v5).
var eventTokens = [...]string{
	EventGrabbed:          "grabbed",
	EventFolderImported:   "seriesFolderImported",
	EventDownloadImported: "downloadFolderImported",
	EventDownloadFailed:   "downloadFailed",
	EventFileDeleted:      "episodeFileDeleted",
	EventFileRenamed:      "episodeFileRenamed",
	EventDownloadIgnored:  "downloadIgnored",
}

// String returns the wire token, or "EventType(<n>)" for a value Sonarr does not define.
func (e EventType) String() string {
	if e > 0 && int(e) < len(eventTokens) {
		return eventTokens[e]
	}
	return fmt.Sprintf("EventType(%d)", int(e))
}

// HistoryRecord is one history event.
type HistoryRecord struct {
	Date      time.Time
	EventType EventType // 0 for a token arrapi does not know
	ID        int
	SeriesID  int
}

// eventToken is a wire eventType: its string token, else 0.
type eventToken EventType

// UnmarshalJSON maps an unknown token, a number and null to 0. Sonarr registers
// JsonStringEnumConverter with allowIntegerValues (NzbDrone.Common/Serializer/
// System.Text.Json/STJson.cs), so a value outside the enum is written as a number, and
// one such record must not fail the whole read. No server writes another kind.
func (e *eventToken) UnmarshalJSON(b []byte) error {
	*e = 0
	switch c := b[0]; {
	case c == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		for v, tok := range eventTokens {
			if v > 0 && tok == s {
				*e = eventToken(v)
				break
			}
		}
		return nil
	case c == 'n', c == '-', c >= '0' && c <= '9':
		return nil
	default:
		return &json.UnmarshalTypeError{Value: tokenKind(c), Type: reflect.TypeFor[EventType](), Field: "eventType"}
	}
}

// tokenKind names the JSON kind a value opening with c has, among those eventToken refuses.
func tokenKind(c byte) string {
	switch c {
	case '{':
		return "object"
	case '[':
		return "array"
	}
	return "bool"
}
