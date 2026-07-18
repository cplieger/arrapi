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
		{"sonarr int downloadIgnored", "7", arrapi.EventDownloadIgnored, false},
		{"sonarr int folderImported", "2", arrapi.EventFolderImported, false},
		{"sonarr int downloadFailed", "4", arrapi.EventDownloadFailed, false},
		{"sonarr int fileRenamed", "6", arrapi.EventFileRenamed, false},
		{"radarr string imported", `"downloadFolderImported"`, arrapi.EventDownloadImported, false},
		{"radarr movieFileDeleted", `"movieFileDeleted"`, arrapi.EventFileDeleted, false},
		{"radarr movieFileRenamed", `"movieFileRenamed"`, arrapi.EventFileRenamed, false},
		{"radarr movieFolderImported", `"movieFolderImported"`, arrapi.EventFolderImported, false},
		{"sonarr seriesFolderImported", `"seriesFolderImported"`, arrapi.EventFolderImported, false},
		{"sonarr episodeFileRenamed", `"episodeFileRenamed"`, arrapi.EventFileRenamed, false},
		{"string downloadIgnored", `"downloadIgnored"`, arrapi.EventDownloadIgnored, false},
		{"unknown string is zero", `"someFutureEvent"`, 0, false},
		{"unknown positive int is zero", "99", 0, false},
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

func TestGetHistorySince_sonarrSingleTypeFilteredClientSide(t *testing.T) {
	// Two events; a single-type filter must keep only the imported one, and the
	// eventType must NOT be pushed to the server (it is filtered client-side).
	body := `[{"id":9,"eventType":3,"seriesId":7,"episodeId":42,"sourceTitle":"Show.S01E01.1080p",
	   "date":"2026-07-01T10:00:00Z","data":{"importedPath":"/media/anime/Show/S01E01.mkv"}},
	   {"id":10,"eventType":1,"seriesId":7,"episodeId":43}]`
	rs := newServer(t, http.StatusOK, body)
	s := fastSonarr(t, rs.srv.URL)

	recs, err := s.GetHistorySince(t.Context(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), arrapi.EventDownloadImported)
	if err != nil {
		t.Fatalf("GetHistorySince: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1 (grabbed filtered out)", len(recs))
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
	for _, want := range []string{"includeSeries=false", "date=2026-07-01T00"} {
		if !strings.Contains(path, want) {
			t.Errorf("path %q missing %q", path, want)
		}
	}
	if strings.Contains(path, "eventType=") {
		t.Errorf("path %q must not push eventType server-side (filtered client-side)", path)
	}
}

// TestGetHistorySince_radarrSingleTypeFilter is the regression for the Radarr
// event-number mismatch: Radarr numbers movieFileRenamed=8 and movieFileDeleted=6,
// so a server-side integer filter using Sonarr's numbering would return the
// wrong events. Client-side filtering on the decoded type must return the
// renamed event and not the deleted one.
func TestGetHistorySince_radarrSingleTypeFilter(t *testing.T) {
	body := `[{"id":1,"eventType":"movieFileDeleted","movieId":100},
	   {"id":2,"eventType":"movieFileRenamed","movieId":100},
	   {"id":3,"eventType":"grabbed","movieId":100}]`
	rs := newServer(t, http.StatusOK, body)
	r, err := arrapi.NewRadarr(rs.srv.URL, testKey, arrapi.WithBaseDelay(time.Millisecond))
	if err != nil {
		t.Fatalf("NewRadarr: %v", err)
	}
	t.Cleanup(r.Close)

	recs, err := r.GetHistorySince(t.Context(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), arrapi.EventFileRenamed)
	if err != nil {
		t.Fatalf("GetHistorySince: %v", err)
	}
	if len(recs) != 1 || recs[0].ID != 2 || recs[0].EventType != arrapi.EventFileRenamed {
		t.Fatalf("records = %+v, want only the renamed event (id 2)", recs)
	}
}

func TestGetHistorySince_noFilterReturnsAll(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":5,"eventType":"downloadFolderImported","movieId":100,"date":"2026-07-01T10:00:00Z"}]`)
	r, err := arrapi.NewRadarr(rs.srv.URL, testKey, arrapi.WithBaseDelay(time.Millisecond))
	if err != nil {
		t.Fatalf("NewRadarr: %v", err)
	}
	t.Cleanup(r.Close)

	recs, err := r.GetHistorySince(t.Context(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GetHistorySince: %v", err)
	}
	if len(recs) != 1 || recs[0].EventType != arrapi.EventDownloadImported || recs[0].MovieID != 100 {
		t.Fatalf("records = %+v, want one imported movie 100", recs)
	}
	if strings.Contains(deref(rs.lastPath.Load()), "eventType=") {
		t.Errorf("path %q should not carry eventType when no filter is given", deref(rs.lastPath.Load()))
	}
}

func TestGetHistorySince_zeroValueFilterReturnsAll(t *testing.T) {
	body := `[{"id":1,"eventType":1,"seriesId":7},{"id":2,"eventType":3,"seriesId":7}]`
	rs := newServer(t, http.StatusOK, body)
	s := fastSonarr(t, rs.srv.URL)

	recs, err := s.GetHistorySince(t.Context(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), arrapi.EventType(0))
	if err != nil {
		t.Fatalf("GetHistorySince: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2 (a zero-value-only filter is ignored, returns all)", len(recs))
	}
}

func TestGetHistorySince_multipleEventTypesFilteredClientSide(t *testing.T) {
	body := `[{"id":1,"eventType":1,"seriesId":7},{"id":2,"eventType":3,"seriesId":7},
	   {"id":3,"eventType":5,"seriesId":7},{"id":4,"eventType":3,"seriesId":8}]`
	rs := newServer(t, http.StatusOK, body)
	s := fastSonarr(t, rs.srv.URL)

	recs, err := s.GetHistorySince(t.Context(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		arrapi.EventGrabbed, arrapi.EventDownloadImported)
	if err != nil {
		t.Fatalf("GetHistorySince: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3 (grabbed + two imported)", len(recs))
	}
	for _, r := range recs {
		if r.EventType != arrapi.EventGrabbed && r.EventType != arrapi.EventDownloadImported {
			t.Errorf("record %d has unwanted event %v", r.ID, r.EventType)
		}
	}
	if strings.Contains(deref(rs.lastPath.Load()), "eventType=") {
		t.Errorf("path %q should not carry eventType for a multi-type request", deref(rs.lastPath.Load()))
	}
}

func TestHistoryRecord_rawEventTypePreserved(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":1,"eventType":"someFutureEvent","seriesId":7}]`)
	s := fastSonarr(t, rs.srv.URL)

	recs, err := s.GetHistorySince(t.Context(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GetHistorySince: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].EventType != 0 {
		t.Errorf("unknown event should decode to 0, got %d", recs[0].EventType)
	}
	if recs[0].RawEventType != "someFutureEvent" {
		t.Errorf("RawEventType = %q, want someFutureEvent", recs[0].RawEventType)
	}
}

func TestHistoryRecord_rawEventTypeDecodesEscapes(t *testing.T) {
	// A garbled or hostile token with JSON escapes must be preserved as the
	// DECODED string: the escaped interior quote resolves, and the trailing
	// escaped quote must not be stripped as if it were the closing delimiter
	// (the old strings.Trim extraction mangled this to `odd\` + a lost tail).
	rs := newServer(t, http.StatusOK, `[{"id":1,"eventType":"odd\"name\"","seriesId":7}]`)
	s := fastSonarr(t, rs.srv.URL)

	recs, err := s.GetHistorySince(t.Context(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GetHistorySince: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].EventType != 0 {
		t.Errorf("unknown event should decode to 0, got %d", recs[0].EventType)
	}
	if recs[0].RawEventType != `odd"name"` {
		t.Errorf("RawEventType = %q, want odd\"name\" decoded", recs[0].RawEventType)
	}
}

func TestHistoryRecord_rawEventTypePreservedForUnknownInt(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":1,"eventType":99,"seriesId":7}]`)
	s := fastSonarr(t, rs.srv.URL)

	recs, err := s.GetHistorySince(t.Context(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GetHistorySince: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].EventType != 0 {
		t.Errorf("unmodeled positive int event should decode to 0, got %d", recs[0].EventType)
	}
	if recs[0].RawEventType != "99" {
		t.Errorf("RawEventType = %q, want 99", recs[0].RawEventType)
	}
}

func TestGetHistory_paged(t *testing.T) {
	body := `{"page":2,"pageSize":10,"totalRecords":25,"records":[
	   {"id":11,"eventType":3,"seriesId":7},{"id":12,"eventType":1,"seriesId":8}]}`
	rs := newServer(t, http.StatusOK, body)
	s := fastSonarr(t, rs.srv.URL)

	page, err := s.GetHistory(t.Context(), arrapi.HistoryOptions{Page: 2, PageSize: 10})
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if page.Page != 2 || page.PageSize != 10 || page.TotalRecords != 25 || len(page.Records) != 2 {
		t.Errorf("page = %+v, want page 2 size 10 total 25 with 2 records", page)
	}
	path := deref(rs.lastPath.Load())
	if !strings.HasPrefix(path, "/api/v3/history?") {
		t.Errorf("path = %q, want /api/v3/history prefix", path)
	}
	for _, want := range []string{"page=2", "pageSize=10", "sortKey=date", "sortDirection=descending"} {
		if !strings.Contains(path, want) {
			t.Errorf("path %q missing %q", path, want)
		}
	}
}

func TestEventType_String(t *testing.T) {
	tests := []struct {
		et   arrapi.EventType
		want string
	}{
		{arrapi.EventGrabbed, "grabbed"},
		{arrapi.EventFolderImported, "folderImported"},
		{arrapi.EventDownloadImported, "downloadFolderImported"},
		{arrapi.EventDownloadFailed, "downloadFailed"},
		{arrapi.EventFileDeleted, "fileDeleted"},
		{arrapi.EventFileRenamed, "fileRenamed"},
		{arrapi.EventDownloadIgnored, "downloadIgnored"},
		{0, "EventType(0)"},
		{99, "EventType(99)"},
	}
	for _, tc := range tests {
		if got := tc.et.String(); got != tc.want {
			t.Errorf("EventType(%d).String() = %q, want %q", int(tc.et), got, tc.want)
		}
	}
}

func TestGetHistory_largePageUsesListLimit(t *testing.T) {
	largeTitle := strings.Repeat("x", (1<<20)+1024)
	rs := newServer(t, http.StatusOK, `{"page":1,"pageSize":1,"totalRecords":1,"records":[{"id":1,"eventType":3,"sourceTitle":"`+largeTitle+`"}]}`)
	s := fastSonarr(t, rs.srv.URL)

	page, err := s.GetHistory(t.Context(), arrapi.HistoryOptions{Page: 1, PageSize: 1})
	if err != nil {
		t.Fatalf("GetHistory on page larger than maxObjectBytes but below maxListBytes: %v", err)
	}
	if len(page.Records) != 1 || page.Records[0].SourceTitle != largeTitle {
		t.Errorf("records = %+v, want one large source title of length %d", page.Records, len(largeTitle))
	}
}

func TestGetHistory_defaultOptionsOmitPageParams(t *testing.T) {
	rs := newServer(t, http.StatusOK, `{"page":1,"pageSize":20,"totalRecords":0,"records":[]}`)
	s := fastSonarr(t, rs.srv.URL)

	page, err := s.GetHistory(t.Context(), arrapi.HistoryOptions{})
	if err != nil {
		t.Fatalf("GetHistory with default options: %v", err)
	}
	if page.Page != 1 || page.PageSize != 20 || page.TotalRecords != 0 || len(page.Records) != 0 {
		t.Errorf("page = %+v, want server defaults page 1 size 20 with no records", page)
	}
	path := deref(rs.lastPath.Load())
	if !strings.HasPrefix(path, "/api/v3/history?") {
		t.Fatalf("path = %q, want /api/v3/history prefix", path)
	}
	for _, forbidden := range []string{"page=", "pageSize="} {
		if strings.Contains(path, forbidden) {
			t.Errorf("default HistoryOptions path %q should omit %q", path, forbidden)
		}
	}
	for _, want := range []string{"sortKey=date", "sortDirection=descending"} {
		if !strings.Contains(path, want) {
			t.Errorf("path %q missing %q", path, want)
		}
	}
}

func TestGetHistorySince_zeroValueMixedWithFilterIsIgnored(t *testing.T) {
	body := `[{"id":1,"eventType":"someFutureEvent","seriesId":7},{"id":2,"eventType":3,"seriesId":7},{"id":3,"eventType":1,"seriesId":7}]`
	rs := newServer(t, http.StatusOK, body)
	s := fastSonarr(t, rs.srv.URL)

	recs, err := s.GetHistorySince(t.Context(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), arrapi.EventType(0), arrapi.EventDownloadImported)
	if err != nil {
		t.Fatalf("GetHistorySince: %v", err)
	}
	if len(recs) != 1 || recs[0].ID != 2 || recs[0].EventType != arrapi.EventDownloadImported {
		t.Fatalf("records = %+v, want only the imported event (id 2); zero-value filter entries are ignored", recs)
	}
}
