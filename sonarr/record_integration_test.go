//go:build integration

package sonarr

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/fixturegen"
)

// TestRecord records testdata/sonarr from ARRAPI_SONARR_V4_URL (the Sonarr 4.0) and
// ARRAPI_SONARR_V5_URL (the Sonarr 5.0), GET only, when ARRAPI_RECORD=1.
func TestRecord(t *testing.T) {
	if os.Getenv("ARRAPI_RECORD") != "1" {
		t.Skip("set ARRAPI_RECORD=1 to record testdata/sonarr from the live servers")
	}
	v4 := liveTarget(t, "ARRAPI_SONARR_V4")
	v5 := liveTarget(t, "ARRAPI_SONARR_V5")
	root := filepath.Join("..", "testdata")
	got, err := recordSonarr(t.Context(), root, v4, v5, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	t.Logf("wrote %d files; real values replaced per map: %v", got.files, got.counts)
	leaks, err := fixturegen.Gitleaks(t.Context(), filepath.Join(root, "sonarr"), t.TempDir())
	if err != nil || leaks != 0 {
		t.Fatalf("gitleaks: %d findings, %v", leaks, err)
	}
	t.Log("gitleaks: 0 findings")
}

func liveTarget(t *testing.T, prefix string) recordTarget {
	t.Helper()
	u, key := os.Getenv(prefix+"_URL"), os.Getenv(prefix+"_KEY")
	if u == "" || key == "" {
		t.Fatalf("%s_URL and %s_KEY must both be set", prefix, prefix)
	}
	return recordTarget{url: u, key: arrapi.APIKey(key)}
}
