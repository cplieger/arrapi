package radarr

import (
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cplieger/arrapi/v3/internal/fixturegen"
)

const reRecord = "re-record with ARRAPI_RECORD=1 go test -tags integration -run TestRecord ./radarr/"

// TestFixturesCarryNoDeploymentData repeats, over the committed fixtures, the checks
// the recorder ran before writing them that need no live run, and requires exactly
// the files the recorder writes.
func TestFixturesCarryNoDeploymentData(t *testing.T) {
	root := filepath.Join("..", "testdata")
	if err := fixturegen.CheckTree(filepath.Join(root, "radarr")); err != nil {
		t.Fatalf("%v; %s", err, reRecord)
	}
	got := slices.Sorted(maps.Keys(readFixtures(t, filepath.Join(root, "radarr"))))
	for i, name := range got {
		got[i] = "radarr/" + name
	}
	if !slices.Equal(got, radarrFixtureFiles) {
		t.Fatalf("testdata/radarr holds\n  %q\nwant\n  %q\n%s", got, radarrFixtureFiles, reRecord)
	}
}

// radarrBuiltFixtureFiles is every hand-built file under testdata/built/radarr: the
// release search answer, built from the document's ReleaseResource because recording
// one runs a live search against every indexer the server has enabled.
var radarrBuiltFixtureFiles = []string{"radarr/api-v3/releases.json"}

// TestBuiltFixturesCarryNoDeploymentData holds the hand-built files to the checks the
// recorder runs on a recorded one, and requires exactly the listed files.
func TestBuiltFixturesCarryNoDeploymentData(t *testing.T) {
	root := filepath.Join("..", "testdata", "built")
	if err := fixturegen.CheckTree(filepath.Join(root, "radarr")); err != nil {
		t.Fatal(err)
	}
	got := slices.Sorted(maps.Keys(readFixtures(t, filepath.Join(root, "radarr"))))
	for i, name := range got {
		got[i] = "radarr/" + name
	}
	if !slices.Equal(got, radarrBuiltFixtureFiles) {
		t.Fatalf("testdata/built/radarr holds\n  %q\nwant\n  %q", got, radarrBuiltFixtureFiles)
	}
}
