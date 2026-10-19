package radarr

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// EventType is a Radarr history event, valued as Radarr's MovieHistoryEventType.
type EventType int

// Values equal Radarr develop's NzbDrone.Core/History/History.cs, which retired 2 and 5.
const (
	EventGrabbed          EventType = 1
	EventDownloadImported EventType = 3 // "downloadFolderImported"
	EventDownloadFailed   EventType = 4
	EventFileDeleted      EventType = 6 // "movieFileDeleted"
	EventFolderImported   EventType = 7 // "movieFolderImported"
	EventFileRenamed      EventType = 8 // "movieFileRenamed"
	EventDownloadIgnored  EventType = 9
)

// eventTokens is MovieHistoryEventType's wire token per value; a retired value's
// entry is "".
var eventTokens = [...]string{
	EventGrabbed:          "grabbed",
	EventDownloadImported: "downloadFolderImported",
	EventDownloadFailed:   "downloadFailed",
	EventFileDeleted:      "movieFileDeleted",
	EventFolderImported:   "movieFolderImported",
	EventFileRenamed:      "movieFileRenamed",
	EventDownloadIgnored:  "downloadIgnored",
}

// tokenEvents inverts eventTokens without the retired values' "", so an empty token
// decodes to 0 rather than a retired value.
var tokenEvents = func() map[string]EventType {
	m := make(map[string]EventType, len(eventTokens))
	for v, tok := range eventTokens {
		if tok != "" {
			m[tok] = EventType(v)
		}
	}
	return m
}()

// String returns the wire token, or "EventType(<n>)" for a value Radarr does not define.
func (e EventType) String() string {
	if e > 0 && int(e) < len(eventTokens) && eventTokens[e] != "" {
		return eventTokens[e]
	}
	return fmt.Sprintf("EventType(%d)", int(e))
}

// HistoryRecord is one history event.
type HistoryRecord struct {
	Date      time.Time
	EventType EventType // 0 for a token arrapi does not know
	ID        int
	MovieID   int
}

// eventToken is a wire eventType: its string token, else 0.
type eventToken EventType

// UnmarshalJSON maps an unknown token, a number and null to 0. Radarr registers
// JsonStringEnumConverter with allowIntegerValues (NzbDrone.Common/Serializer/
// System.Text.Json/STJson.cs), so a value outside the gapped enum is written as a
// number, and one such record must not fail the whole read. No server writes another kind.
func (e *eventToken) UnmarshalJSON(b []byte) error {
	*e = 0
	switch c := b[0]; {
	case c == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*e = eventToken(tokenEvents[s])
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
