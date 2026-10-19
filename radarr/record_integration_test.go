//go:build integration

package radarr

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/fixturegen"
)

// TestRecord records testdata/radarr from ARRAPI_RADARR_URL, GET only, when
// ARRAPI_RECORD=1.
func TestRecord(t *testing.T) {
	if os.Getenv("ARRAPI_RECORD") != "1" {
		t.Skip("set ARRAPI_RECORD=1 to record testdata/radarr from the live server")
	}
	u, key := os.Getenv("ARRAPI_RADARR_URL"), os.Getenv("ARRAPI_RADARR_KEY")
	if u == "" || key == "" {
		t.Fatal("ARRAPI_RADARR_URL and ARRAPI_RADARR_KEY must both be set")
	}
	root := filepath.Join("..", "testdata")
	got, err := recordRadarr(t.Context(), root, recordTarget{url: u, key: arrapi.APIKey(key)}, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	t.Logf("wrote %d files; real values replaced per map: %v", got.files, got.counts)
	leaks, err := fixturegen.Gitleaks(t.Context(), filepath.Join(root, "radarr"), t.TempDir())
	if err != nil || leaks != 0 {
		t.Fatalf("gitleaks: %d findings, %v", leaks, err)
	}
	t.Log("gitleaks: 0 findings")
}
