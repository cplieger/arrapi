package sonarr

import (
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cplieger/arrapi/v3/internal/fixturegen"
)

const reRecord = "re-record with ARRAPI_RECORD=1 go test -tags integration -run TestRecord ./sonarr/"

// TestFixturesCarryNoDeploymentData repeats, over the committed fixtures, the checks
// the recorder ran before writing them that need no live run, and requires exactly
// the files the recorder writes.
func TestFixturesCarryNoDeploymentData(t *testing.T) {
	root := filepath.Join("..", "testdata")
	if err := fixturegen.CheckTree(filepath.Join(root, "sonarr")); err != nil {
		t.Fatalf("%v; %s", err, reRecord)
	}
	got := slices.Sorted(maps.Keys(readFixtures(t, filepath.Join(root, "sonarr"))))
	for i, name := range got {
		got[i] = "sonarr/" + name
	}
	if !slices.Equal(got, sonarrFixtureFiles) {
		t.Fatalf("testdata/sonarr holds\n  %q\nwant\n  %q\n%s", got, sonarrFixtureFiles, reRecord)
	}
}
