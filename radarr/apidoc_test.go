package radarr

import (
	"os/exec"
	"strings"
	"testing"
)

// TestHistorySinceDocNamesTheInnerJoin pins the public text callers rely on: Radarr's
// /history/since inner-joins each record with its movie, oldest first.
// go doc re-wraps a doc comment, so every run of whitespace is compared as one space.
func TestHistorySinceDocNamesTheInnerJoin(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go command is needed to read the API text: %v", err)
	}
	out, err := exec.CommandContext(t.Context(), gobin, "doc", "Client.HistorySince").CombinedOutput()
	if err != nil {
		t.Fatalf("go doc Client.HistorySince: %v\n%s", err, out)
	}
	doc := strings.Join(strings.Fields(string(out)), " ")
	for _, want := range []string{"inner-joins each record with its movie", "oldest first"} {
		if !strings.Contains(doc, want) {
			t.Errorf("go doc Client.HistorySince lacks %q:\n%s", want, doc)
		}
	}
}
