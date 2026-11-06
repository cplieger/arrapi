package arrenum

import (
	"testing"

	"github.com/cplieger/arrapi/v3"
)

func TestReleaseSource(t *testing.T) {
	tests := []struct {
		in   string
		want arrapi.ReleaseSource
	}{
		{"Rss", arrapi.SourceRSS},
		{"Search", arrapi.SourceSearch},
		{"UserInvokedSearch", arrapi.SourceUserInvokedSearch},
		{"InteractiveSearch", arrapi.SourceInteractiveSearch},
		{"ReleasePush", arrapi.SourceReleasePush},
		{"Unknown", ""},
		{"", ""},
		{"rss", ""},
		{"InteractiveSearch ", ""},
		{"Interactive", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := ReleaseSource(tc.in); got != tc.want {
				t.Errorf("ReleaseSource(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The words are NzbDrone.Core/Messaging/Commands/CommandStatus.cs's members under
// STJson's camelCase naming, the same in Sonarr 4, Sonarr 5 and Radarr.
func TestCommandStatus(t *testing.T) {
	tests := []struct {
		in   string
		want arrapi.CommandStatus
	}{
		{"queued", arrapi.CommandQueued},
		{"started", arrapi.CommandStarted},
		{"completed", arrapi.CommandCompleted},
		{"failed", arrapi.CommandFailed},
		{"aborted", arrapi.CommandAborted},
		{"cancelled", arrapi.CommandCancelled},
		{"orphaned", arrapi.CommandOrphaned},
		{"Queued", ""},
		{"done", ""},
		{"", ""},
		{"completed ", ""},
		{"canceled", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := CommandStatus(tc.in); got != tc.want {
				t.Errorf("CommandStatus(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The words are NzbDrone.Core/Download/TrackedDownloads/TrackedDownload.cs's
// TrackedDownloadState members under STJson's camelCase naming, the same in Sonarr 4,
// Sonarr 5 and Radarr.
func TestDownloadState(t *testing.T) {
	tests := []struct {
		in   string
		want arrapi.DownloadState
	}{
		{"downloading", arrapi.DownloadDownloading},
		{"importBlocked", arrapi.DownloadImportBlocked},
		{"importPending", arrapi.DownloadImportPending},
		{"importing", arrapi.DownloadImporting},
		{"imported", arrapi.DownloadImported},
		{"failedPending", arrapi.DownloadFailedPending},
		{"failed", arrapi.DownloadFailed},
		{"ignored", arrapi.DownloadIgnored},
		{"Downloading", ""},
		{"queued", ""},
		{"importblocked", ""},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := DownloadState(tc.in); got != tc.want {
				t.Errorf("DownloadState(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
