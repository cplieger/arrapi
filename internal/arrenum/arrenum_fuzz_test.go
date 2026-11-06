package arrenum

import (
	"slices"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

// FuzzReleaseSource: any text maps to "" or one of the five sources, and a source is
// the input byte for byte.
func FuzzReleaseSource(f *testing.F) {
	for _, seed := range []string{
		"Rss", "Search", "UserInvokedSearch", "InteractiveSearch", "ReleasePush",
		"Unknown", "", "rss", "RSS", "InteractiveSearch ", "\x00Rss", "Rss\u200b",
	} {
		f.Add(seed)
	}
	known := []arrapi.ReleaseSource{
		arrapi.SourceRSS, arrapi.SourceSearch, arrapi.SourceUserInvokedSearch,
		arrapi.SourceInteractiveSearch, arrapi.SourceReleasePush,
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := ReleaseSource(s)
		if got == "" {
			return
		}
		if !slices.Contains(known, got) || string(got) != s {
			t.Errorf("ReleaseSource(%q) = %q, want \"\" or the input as one of the five sources", s, got)
		}
	})
}

// FuzzCommandStatus: any text maps to "" or one of the seven statuses, and a status is
// the input byte for byte.
func FuzzCommandStatus(f *testing.F) {
	for _, seed := range []string{
		"queued", "started", "completed", "failed", "aborted", "cancelled", "orphaned",
		"", "Queued", "COMPLETED", "done", "canceled", "completed ", "\x00queued", "queued\u200b",
	} {
		f.Add(seed)
	}
	known := []arrapi.CommandStatus{
		arrapi.CommandQueued, arrapi.CommandStarted, arrapi.CommandCompleted, arrapi.CommandFailed,
		arrapi.CommandAborted, arrapi.CommandCancelled, arrapi.CommandOrphaned,
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := CommandStatus(s)
		if got == "" {
			if slices.Contains(known, arrapi.CommandStatus(s)) {
				t.Errorf("CommandStatus(%q) = \"\", want that status", s)
			}
			return
		}
		if !slices.Contains(known, got) || string(got) != s {
			t.Errorf("CommandStatus(%q) = %q, want \"\" or the input as one of the seven statuses", s, got)
		}
	})
}

// FuzzDownloadState: any text maps to "" or one of the eight states, and a state is the
// input byte for byte.
func FuzzDownloadState(f *testing.F) {
	for _, seed := range []string{
		"downloading", "importBlocked", "importPending", "importing", "imported",
		"failedPending", "failed", "ignored", "", "Downloading", "queued", "importblocked",
		"failed ", "\x00failed", "imported\u200b",
	} {
		f.Add(seed)
	}
	known := []arrapi.DownloadState{
		arrapi.DownloadDownloading, arrapi.DownloadImportBlocked, arrapi.DownloadImportPending,
		arrapi.DownloadImporting, arrapi.DownloadImported, arrapi.DownloadFailedPending,
		arrapi.DownloadFailed, arrapi.DownloadIgnored,
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := DownloadState(s)
		if got == "" {
			if slices.Contains(known, arrapi.DownloadState(s)) {
				t.Errorf("DownloadState(%q) = \"\", want that state", s)
			}
			return
		}
		if !slices.Contains(known, got) || string(got) != s {
			t.Errorf("DownloadState(%q) = %q, want \"\" or the input as one of the eight states", s, got)
		}
	})
}
