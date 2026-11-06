package radarr

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
)

func TestEventTypeValues(t *testing.T) {
	for want, e := range map[int]EventType{
		1: EventGrabbed, 3: EventDownloadImported, 4: EventDownloadFailed, 6: EventFileDeleted,
		7: EventFolderImported, 8: EventFileRenamed, 9: EventDownloadIgnored,
	} {
		if int(e) != want {
			t.Errorf("%v = %d, want Radarr's MovieHistoryEventType value %d", e, int(e), want)
		}
	}
}

func TestEventTypeString(t *testing.T) {
	tests := []struct {
		want string
		e    EventType
	}{
		{"grabbed", EventGrabbed},
		{"downloadFolderImported", EventDownloadImported},
		{"downloadFailed", EventDownloadFailed},
		{"movieFileDeleted", EventFileDeleted},
		{"movieFolderImported", EventFolderImported},
		{"movieFileRenamed", EventFileRenamed},
		{"downloadIgnored", EventDownloadIgnored},
		{"EventType(0)", 0},
		{"EventType(2)", 2},
		{"EventType(5)", 5},
		{"EventType(10)", 10},
		{"EventType(-1)", -1},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.e.String(); got != tc.want {
				t.Errorf("EventType(%d).String() = %q, want %q", int(tc.e), got, tc.want)
			}
		})
	}
}

func TestHistoryRecordEventToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  EventType
	}{
		{"grabbed", `"grabbed"`, EventGrabbed},
		{"downloadFolderImported", `"downloadFolderImported"`, EventDownloadImported},
		{"downloadFailed", `"downloadFailed"`, EventDownloadFailed},
		{"movieFileDeleted", `"movieFileDeleted"`, EventFileDeleted},
		{"movieFolderImported", `"movieFolderImported"`, EventFolderImported},
		{"movieFileRenamed", `"movieFileRenamed"`, EventFileRenamed},
		{"downloadIgnored", `"downloadIgnored"`, EventDownloadIgnored},
		{"an escaped token", `"gr\u0061bbed"`, EventGrabbed},
		{"an unknown token is 0", `"someFutureEvent"`, 0},
		{"the enum's unknown member is 0", `"unknown"`, 0},
		{"a Sonarr token is 0", `"episodeFileDeleted"`, 0},
		{"the retired seriesFolderImported is 0", `"seriesFolderImported"`, 0},
		{"tokens match exactly", `"Grabbed"`, 0},
		{"an empty token is 0", `""`, 0},
		{"a number is 0", `3`, 0},
		{"a gap value is 0", `5`, 0},
		{"a negative number is 0", `-1`, 0},
		{"an out-of-range number is 0", `1e999`, 0},
		{"null is 0", `null`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var w radarrHistory
			if err := json.Unmarshal([]byte(`{"id":4,"movieId":9,"eventType":`+tc.token+`}`), &w); err != nil {
				t.Fatalf("decode eventType %s: %v, want no error", tc.token, err)
			}
			if got := w.record().EventType; got != tc.want {
				t.Errorf("eventType %s decoded to %v, want %v", tc.token, got, tc.want)
			}
		})
	}
}

func TestHistoryRecordEventTokenOtherKindIsError(t *testing.T) {
	for _, token := range []string{`{"name":"grabbed"}`, `["grabbed"]`, `true`, `false`} {
		t.Run(token, func(t *testing.T) {
			var w radarrHistory
			err := json.Unmarshal([]byte(`{"id":4,"eventType":`+token+`}`), &w)
			te, ok := errors.AsType[*json.UnmarshalTypeError](err)
			if !ok {
				t.Fatalf("decode eventType %s: %v, want a *json.UnmarshalTypeError (no server writes that kind)", token, err)
			}
			if te.Type != reflect.TypeFor[EventType]() || te.Field != "eventType" {
				t.Errorf("decode eventType %s: Type %v, Field %q; want EventType at eventType", token, te.Type, te.Field)
			}
		})
	}
}

func TestHistoryRecordMapping(t *testing.T) {
	body := `{"movieId":41,"sourceTitle":"Example.Movie.2011.1080p",
		"languages":[{"id":8,"name":"Japanese"}],"quality":{"quality":{"id":7,"name":"Bluray-1080p"},"revision":{"version":1,"real":0,"isRepack":false}},
		"customFormats":[],"customFormatScore":0,"qualityCutoffNotMet":false,
		"date":"2031-07-09T13:57:11Z","downloadId":"SYNTHETICDOWNLOAD","eventType":"movieFileRenamed",
		"data":{"path":"/movies/x.mkv"},"movie":null,"arrapiUnknownField":1,"id":9001}`
	var w radarrHistory
	if err := json.Unmarshal([]byte(body), &w); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := w.record()
	if want := time.Date(2031, 7, 9, 13, 57, 11, 0, time.UTC); !got.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", got.Date, want)
	}
	if got.EventType != EventFileRenamed || got.ID != 9001 || got.MovieID != 41 {
		t.Errorf("record() = %+v, want EventFileRenamed, ID 9001, MovieID 41", got)
	}
}

func TestHistoryGrabFieldsOverTheWire(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	cached(t, c, f)
	f.on("GET /api/v3/history/since", ans(200, vRadarr, `[
		{"id":1,"movieId":4,"eventType":"grabbed","downloadId":"DL-1","sourceTitle":"Example.Movie.2005.1080p",
		 "data":{"guid":"guid-at-indexer","indexer":"Indexer A","nzbInfoUrl":"info-page","releaseSource":"InteractiveSearch",
		         "downloadUrl":"ignored","torrentInfoHash":"ignored","arrapiUnknownField":"x"}},
		{"id":2,"movieId":4,"eventType":"downloadFailed","downloadId":"DL-1","sourceTitle":"Example.Movie.2005.1080p",
		 "data":{"message":"the client removed it","indexer":"Indexer A"}},
		{"id":3,"movieId":4,"eventType":"grabbed","data":null},
		{"id":4,"movieId":4,"eventType":"grabbed"},
		{"id":5,"movieId":4,"eventType":"grabbed","data":{"releaseSource":"Unknown","message":null,"guid":null}},
		{"id":6,"movieId":4,"eventType":"grabbed","data":{"releaseSource":"Search"}}]`))
	got, err := c.HistorySince(t.Context(), time.Unix(0, 0))
	if err != nil {
		t.Fatalf("HistorySince: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("HistorySince returned %d records, want 6", len(got))
	}
	want := []HistoryRecord{
		{
			ID: 1, MovieID: 4, EventType: EventGrabbed, DownloadID: "DL-1", SourceTitle: "Example.Movie.2005.1080p",
			GUID: "guid-at-indexer", Indexer: "Indexer A", InfoURL: "info-page", Source: arrapi.SourceInteractiveSearch,
		},
		{
			ID: 2, MovieID: 4, EventType: EventDownloadFailed, DownloadID: "DL-1", SourceTitle: "Example.Movie.2005.1080p",
			Indexer: "Indexer A", Message: "the client removed it",
		},
		{ID: 3, MovieID: 4, EventType: EventGrabbed},
		{ID: 4, MovieID: 4, EventType: EventGrabbed},
		{ID: 5, MovieID: 4, EventType: EventGrabbed},
		{ID: 6, MovieID: 4, EventType: EventGrabbed, Source: arrapi.SourceSearch},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d =\n  %+v\nwant\n  %+v", want[i].ID, got[i], want[i])
		}
	}

	f.on("GET /api/v3/history/since", ans(200, vRadarr, `[{"id":1,"eventType":"grabbed","data":{"guid":7}}]`))
	_, err = c.HistorySince(t.Context(), time.Unix(0, 0))
	if _, ok := errors.AsType[*arrapi.DecodeError](err); !ok || !strings.Contains(err.Error(), "field data.guid") {
		t.Errorf("HistorySince over a numeric data.guid = %v, want a *DecodeError naming field data.guid", err)
	}
}
