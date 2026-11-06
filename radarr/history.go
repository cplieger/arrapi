package radarr

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/cplieger/arrapi/v3"
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

// HistoryRecord is one history event. The string fields hold the server's text as it
// recorded it; arrapi checks none, so a caller renders them through textsafe or an
// escaping template. GUID and InfoURL are the indexer's own values, and a GUID can be a
// link carrying the indexer's passkey, so log neither.
type HistoryRecord struct {
	Date        time.Time
	DownloadID  string               // the download client's id for the release, shared by its queue item and its later import, failed and ignored records; "" when the record has none
	SourceTitle string               // the release title; on movieFileDeleted and movieFileRenamed, the file path Radarr recorded
	GUID        string               // grabbed: the release's guid at its indexer; "" on other events
	Indexer     string               // the indexer's name in Radarr: on grabbed, downloadFailed and downloadIgnored; "" otherwise
	InfoURL     string               // grabbed: the release's information page at its indexer; "" on other events and when the indexer gives none
	Source      arrapi.ReleaseSource // grabbed: what made Radarr grab it; "" on other events, for "Unknown", and for a value arrapi does not know
	Message     string               // downloadFailed and downloadIgnored: Radarr's reason; "" otherwise
	EventType   EventType            // 0 for a token arrapi does not know
	ID          int
	MovieID     int
}

// eventToken is a wire eventType: its string token, else 0.
type eventToken EventType

// UnmarshalJSON maps an unknown token, a number and null to 0.
func (e *eventToken) UnmarshalJSON(b []byte) error {
	*e = 0
	s, err := stringToken(b, "eventType", reflect.TypeFor[EventType]())
	if s == "" {
		return err
	}
	*e = eventToken(tokenEvents[s])
	return nil
}

// stringToken reads a closed server enum's wire value b: a string as sent, "" for a
// number or null. Radarr registers JsonStringEnumConverter with allowIntegerValues
// (NzbDrone.Common/Serializer/System.Text.Json/STJson.cs), so a value outside the enum
// is written as a number, and one such item must not fail the whole read. No server
// writes another kind, which is refused naming field and typ: encoding/json returns an
// UnmarshalJSON method's error with no field path of its own.
func stringToken(b []byte, field string, typ reflect.Type) (string, error) {
	switch c := b[0]; {
	case c == '"':
		var s string
		err := json.Unmarshal(b, &s)
		return s, err
	case c == 'n', c == '-', c >= '0' && c <= '9':
		return "", nil
	default:
		return "", &json.UnmarshalTypeError{Value: tokenKind(c), Type: typ, Field: field}
	}
}

// tokenKind names the JSON kind a value opening with c has, among those stringToken refuses.
func tokenKind(c byte) string {
	switch c {
	case '{':
		return "object"
	case '[':
		return "array"
	}
	return "bool"
}
