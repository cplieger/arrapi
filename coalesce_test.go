package arrapi_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestGetSeries_coalescesConcurrentCalls verifies singleflight collapses
// concurrent identical reads into a single upstream request.
func TestGetSeries_coalescesConcurrentCalls(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		time.Sleep(40 * time.Millisecond) // hold the flight open so callers pile up
		_, _ = w.Write([]byte(`[{"id":1,"title":"x"}]`))
	}))
	t.Cleanup(srv.Close)
	s := fastSonarr(t, srv.URL)

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	lens := make([]int, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			series, err := s.GetSeries(t.Context())
			errs[i], lens[i] = err, len(series)
		}(i)
	}
	wg.Wait()

	for i := range n {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if lens[i] != 1 {
			t.Errorf("caller %d got %d series, want 1", i, lens[i])
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("upstream saw %d requests, want 1 (coalesced)", got)
	}
}
