package arrapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi"
)

func TestEventType_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    arrapi.EventType
		wantErr bool
	}{
		{"sonarr int grabbed", "1", arrapi.EventGrabbed, false},
		{"sonarr int imported", "3", arrapi.EventDownloadImported, false},
		{"sonarr int deleted", "5", arrapi.EventFileDeleted, false},
		{"radarr string imported", `"downloadFolderImported"`, arrapi.EventDownloadImported, false},
		{"radarr movieFileDeleted", `"movieFileDeleted"`, arrapi.EventFileDeleted, false},
		{"sonarr episodeFileRenamed", `"episodeFileRenamed"`, arrapi.EventFileRenamed, false},
		{"unknown string is zero", `"someFutureEvent"`, 0, false},
		{"negative is zero", "-1", 0, false},
		{"null is zero", "null", 0, false},
		{"bool is error", "true", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var e arrapi.EventType
			err := json.Unmarshal([]byte(tc.json), &e)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Unmarshal(%s) err = %v, wantErr %v", tc.json, err, tc.wantErr)
			}
			if !tc.wantErr && e != tc.want {
				t.Errorf("Unmarshal(%s) = %d, want %d", tc.json, e, tc.want)
			}
		})
	}
}

func TestGetHistorySince_sonarr(t *testing.T) {
	body := `[{"id":9,"eventType":3,"seriesId":7,"episodeId":42,"sourceTitle":"Show.S01E01.1080p",
	   "date":"2026-07-01T10:00:00Z","data":{"importedPath":"/media/anime/Show/S01E01.mkv"}}]`
	rs := newServer(t, http.StatusOK, body)
	s := fastSonarr(t, rs.srv.URL)

	recs, err := s.GetHistorySince(t.Context(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), arrapi.EventDownloadImported)
	if err != nil {
		t.Fatalf("GetHistorySince: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	r := recs[0]
	if r.EventType != arrapi.EventDownloadImported || r.SeriesID != 7 || r.EpisodeID != 42 {
		t.Errorf("record = %+v, want event 3 series 7 episode 42", r)
	}
	if r.ImportedPath() != "/media/anime/Show/S01E01.mkv" {
		t.Errorf("ImportedPath() = %q", r.ImportedPath())
	}
	path := deref(rs.lastPath.Load())
	if !strings.HasPrefix(path, "/api/v3/history/since?") {
		t.Errorf("path = %q, want /api/v3/history/since prefix", path)
	}
	for _, want := range []string{"eventType=3", "includeSeries=false", "date=2026-07-01T00"} {
		if !strings.Contains(path, want) {
			t.Errorf("path %q missing %q", path, want)
		}
	}
}

func TestGetHistorySince_radarrStringEventAndNoFilter(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":5,"eventType":"downloadFolderImported","movieId":100,"date":"2026-07-01T10:00:00Z"}]`)
	r, err := arrapi.NewRadarr(rs.srv.URL, testKey, arrapi.WithBaseDelay(time.Millisecond))
	if err != nil {
		t.Fatalf("NewRadarr: %v", err)
	}
	t.Cleanup(r.Close)

	recs, err := r.GetHistorySince(t.Context(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), 0)
	if err != nil {
		t.Fatalf("GetHistorySince: %v", err)
	}
	if len(recs) != 1 || recs[0].EventType != arrapi.EventDownloadImported || recs[0].MovieID != 100 {
		t.Fatalf("records = %+v, want one imported movie 100", recs)
	}
	// eventType filter omitted when 0.
	if strings.Contains(deref(rs.lastPath.Load()), "eventType=") {
		t.Errorf("path %q should not carry eventType when filter is 0", deref(rs.lastPath.Load()))
	}
}
