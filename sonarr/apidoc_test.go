package sonarr

import (
	"os/exec"
	"strings"
	"testing"
)

// goDoc returns `go doc <symbol>` run in this package, every run of whitespace one
// space, since go doc re-wraps a doc comment's paragraphs.
func goDoc(t *testing.T, symbol string) string {
	t.Helper()
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go command is needed to read the API text: %v", err)
	}
	out, err := exec.CommandContext(t.Context(), gobin, "doc", symbol).CombinedOutput()
	if err != nil {
		t.Fatalf("go doc %s: %v\n%s", symbol, err, out)
	}
	return strings.Join(strings.Fields(string(out)), " ")
}

// TestHistorySinceDocNamesTheInnerJoin pins the public text callers rely on: Sonarr's
// /history/since inner-joins each record with its series and episode, oldest first
// (NzbDrone.Core/History/HistoryRepository.cs Since).
func TestHistorySinceDocNamesTheInnerJoin(t *testing.T) {
	doc := goDoc(t, "Client.HistorySince")
	for _, want := range []string{"inner-joins each record with its series and its episode", "oldest first"} {
		if !strings.Contains(doc, want) {
			t.Errorf("go doc Client.HistorySince lacks %q:\n%s", want, doc)
		}
	}
}
