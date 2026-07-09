package arrapi_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestGetSeries_concurrentCallsAreSafe fires many simultaneous reads at one
// client under the race detector; each caller must get its own correct result.
// arrapi does not coalesce identical reads, so every call reaches upstream, but
// a single client is documented as safe for concurrent use.
func TestGetSeries_concurrentCallsAreSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
}
