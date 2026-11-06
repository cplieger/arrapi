package sonarr

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
)

func TestEventTypeString(t *testing.T) {
	tests := []struct {
		want string
		e    EventType
	}{
		{"grabbed", EventGrabbed},
		{"seriesFolderImported", EventFolderImported},
		{"downloadFolderImported", EventDownloadImported},
		{"downloadFailed", EventDownloadFailed},
		{"episodeFileDeleted", EventFileDeleted},
		{"episodeFileRenamed", EventFileRenamed},
		{"downloadIgnored", EventDownloadIgnored},
		{"EventType(0)", 0},
		{"EventType(8)", 8},
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
		{"seriesFolderImported", `"seriesFolderImported"`, EventFolderImported},
		{"downloadFolderImported", `"downloadFolderImported"`, EventDownloadImported},
		{"downloadFailed", `"downloadFailed"`, EventDownloadFailed},
		{"episodeFileDeleted", `"episodeFileDeleted"`, EventFileDeleted},
		{"episodeFileRenamed", `"episodeFileRenamed"`, EventFileRenamed},
		{"downloadIgnored", `"downloadIgnored"`, EventDownloadIgnored},
		{"an escaped token", `"gr\u0061bbed"`, EventGrabbed},
		{"an unknown token is 0", `"someFutureEvent"`, 0},
		{"the enum's unknown member is 0", `"unknown"`, 0},
		{"a Radarr token is 0", `"movieFileDeleted"`, 0},
		{"tokens match exactly", `"Grabbed"`, 0},
		{"an empty token is 0", `""`, 0},
		{"a number is 0", `3`, 0},
		{"a negative number is 0", `-1`, 0},
		{"an out-of-range number is 0", `1e999`, 0},
		{"null is 0", `null`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var w sonarrV4History
			if err := json.Unmarshal([]byte(`{"id":4,"seriesId":9,"eventType":`+tc.token+`}`), &w); err != nil {
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
			var w sonarrV4History
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
	body := `{"episodeId":301,"seriesId":41,"sourceTitle":"Example.Show.S01E02.1080p",
		"languages":[{"id":8,"name":"Japanese"}],"quality":{"quality":{"id":7,"name":"Bluray-1080p"},"revision":{"version":1,"real":0,"isRepack":false}},
		"customFormats":[],"customFormatScore":0,"qualityCutoffNotMet":false,
		"date":"2031-07-09T13:57:11Z","downloadId":"SYNTHETICDOWNLOAD","eventType":"downloadFolderImported",
		"data":{"droppedPath":"/downloads/x.mkv","importedPath":"/tv/x.mkv"},"arrapiUnknownField":1,"id":9001}`
	var w sonarrV4History
	if err := json.Unmarshal([]byte(body), &w); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := HistoryRecord{
		Date:      time.Date(2031, 7, 9, 13, 57, 11, 0, time.UTC),
		EventType: EventDownloadImported,
		ID:        9001,
		SeriesID:  41,
	}
	got := w.record()
	if !got.Date.Equal(want.Date) {
		t.Errorf("Date = %v, want %v", got.Date, want.Date)
	}
	if got.EventType != want.EventType || got.ID != want.ID || got.SeriesID != want.SeriesID {
		t.Errorf("record() = %+v, want %+v", got, want)
	}
}

func TestHistoryGrabFieldsOverTheWire(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			f.on("GET "+ec.prefix+"/history/since", ans(200, ec.version, `[
				{"id":1,"seriesId":4,"episodeId":301,"eventType":"grabbed","downloadId":"DL-1","sourceTitle":"Example.Show.S01E02.1080p",
				 "data":{"guid":"guid-at-indexer","indexer":"Indexer A","nzbInfoUrl":"info-page","releaseSource":"InteractiveSearch",
				         "downloadUrl":"ignored","torrentInfoHash":"ignored","arrapiUnknownField":"x"}},
				{"id":2,"seriesId":4,"episodeId":301,"eventType":"downloadFailed","downloadId":"DL-1","sourceTitle":"Example.Show.S01E02.1080p",
				 "data":{"message":"the client removed it","indexer":"Indexer A"}},
				{"id":3,"seriesId":4,"eventType":"grabbed","data":null},
				{"id":4,"seriesId":4,"eventType":"grabbed"},
				{"id":5,"seriesId":4,"eventType":"grabbed","data":{"releaseSource":"Unknown","message":null,"guid":null}},
				{"id":6,"seriesId":4,"eventType":"grabbed","data":{"releaseSource":"Rss"}}]`))
			got, err := c.HistorySince(t.Context(), time.Unix(0, 0))
			if err != nil {
				t.Fatalf("HistorySince: %v", err)
			}
			if len(got) != 6 {
				t.Fatalf("HistorySince returned %d records, want 6", len(got))
			}
			want := []HistoryRecord{
				{
					ID: 1, SeriesID: 4, EventType: EventGrabbed, EpisodeID: 301, DownloadID: "DL-1", SourceTitle: "Example.Show.S01E02.1080p",
					GUID: "guid-at-indexer", Indexer: "Indexer A", InfoURL: "info-page", Source: arrapi.SourceInteractiveSearch,
				},
				{
					ID: 2, SeriesID: 4, EventType: EventDownloadFailed, EpisodeID: 301, DownloadID: "DL-1", SourceTitle: "Example.Show.S01E02.1080p",
					Indexer: "Indexer A", Message: "the client removed it",
				},
				{ID: 3, SeriesID: 4, EventType: EventGrabbed},
				{ID: 4, SeriesID: 4, EventType: EventGrabbed},
				{ID: 5, SeriesID: 4, EventType: EventGrabbed},
				{ID: 6, SeriesID: 4, EventType: EventGrabbed, Source: arrapi.SourceRSS},
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("record %d =\n  %+v\nwant\n  %+v", want[i].ID, got[i], want[i])
				}
			}

			f.on("GET "+ec.prefix+"/history/since", ans(200, ec.version, `[{"id":1,"eventType":"grabbed","data":{"guid":7}}]`))
			_, err = c.HistorySince(t.Context(), time.Unix(0, 0))
			if _, ok := errors.AsType[*arrapi.DecodeError](err); !ok || !strings.Contains(err.Error(), "field data.guid") {
				t.Errorf("HistorySince over a numeric data.guid = %v, want a *DecodeError naming field data.guid", err)
			}
		})
	}
}
