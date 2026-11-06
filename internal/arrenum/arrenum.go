// Package arrenum maps the enum text Sonarr and Radarr both write to arrapi's closed
// sets; any text outside a set maps to "".
package arrenum

import "github.com/cplieger/arrapi/v3"

// ReleaseSource returns the ReleaseSource whose value is exactly s, the
// ReleaseSourceType.ToString() a grabbed history record carries (Sonarr and Radarr
// src/NzbDrone.Core/History/HistoryService.cs); "" for "Unknown", "" and any other text.
func ReleaseSource(s string) arrapi.ReleaseSource {
	switch src := arrapi.ReleaseSource(s); src {
	case arrapi.SourceRSS, arrapi.SourceSearch, arrapi.SourceUserInvokedSearch,
		arrapi.SourceInteractiveSearch, arrapi.SourceReleasePush:
		return src
	default:
		return ""
	}
}

// CommandStatus returns the CommandStatus whose value is exactly s, a command's status
// as the server writes it (NzbDrone.Core/Messaging/Commands/CommandStatus.cs under
// camelCase naming, so no case variant exists); "" for any other text.
func CommandStatus(s string) arrapi.CommandStatus {
	switch st := arrapi.CommandStatus(s); st {
	case arrapi.CommandQueued, arrapi.CommandStarted, arrapi.CommandCompleted, arrapi.CommandFailed,
		arrapi.CommandAborted, arrapi.CommandCancelled, arrapi.CommandOrphaned:
		return st
	default:
		return ""
	}
}

// DownloadState returns the DownloadState whose value is exactly s, a queue item's
// trackedDownloadState as the server writes it (NzbDrone.Core/Download/TrackedDownloads/
// TrackedDownload.cs under camelCase naming); "" for any other text.
func DownloadState(s string) arrapi.DownloadState {
	switch st := arrapi.DownloadState(s); st {
	case arrapi.DownloadDownloading, arrapi.DownloadImportBlocked, arrapi.DownloadImportPending,
		arrapi.DownloadImporting, arrapi.DownloadImported, arrapi.DownloadFailedPending,
		arrapi.DownloadFailed, arrapi.DownloadIgnored:
		return st
	default:
		return ""
	}
}
