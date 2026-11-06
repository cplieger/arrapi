package sonarr

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/cplieger/arrapi/v3"
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

// HistoryRecord is one history event. The string fields hold the server's text as it
// recorded it; arrapi checks none, so a caller renders them through textsafe or an
// escaping template. GUID and InfoURL are the indexer's own values, and a GUID can be a
// link carrying the indexer's passkey, so log neither.
type HistoryRecord struct {
	Date        time.Time
	DownloadID  string               // the download client's id for the release, shared by its queue item and its later import, failed and ignored records; "" when the record has none
	SourceTitle string               // the release title; on episodeFileDeleted and episodeFileRenamed, the file path Sonarr recorded
	GUID        string               // grabbed: the release's guid at its indexer; "" on other events
	Indexer     string               // the indexer's name in Sonarr: on grabbed, and on downloadFailed and downloadIgnored from Sonarr 5; "" otherwise
	InfoURL     string               // grabbed: the release's information page at its indexer; "" on other events and when the indexer gives none
	Source      arrapi.ReleaseSource // grabbed: what made Sonarr grab it; "" on other events, for "Unknown", and for a value arrapi does not know
	Message     string               // downloadFailed and downloadIgnored: Sonarr's reason; "" otherwise
	EventType   EventType            // 0 for a token arrapi does not know
	ID          int
	SeriesID    int
	EpisodeID   int // the episode the record is about; Sonarr writes one record per episode, so a season pack's grab is several records sharing one DownloadID
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
	for v, tok := range eventTokens {
		if v > 0 && tok == s {
			*e = eventToken(v)
			break
		}
	}
	return nil
}

// stringToken reads a closed server enum's wire value b: a string as sent, "" for a
// number or null. Sonarr registers JsonStringEnumConverter with allowIntegerValues
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
