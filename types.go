package arrapi

// APIKey is a server's API key, sent as X-Api-Key.
type APIKey string

// API is the wire generation a client reads.
type API string

// The generations arrapi reads.
const (
	APIv3 API = "v3" // Sonarr v4 (API v3) and Radarr (API v3)
	APIv5 API = "v5" // Sonarr v5 (API v5)
)

// Language is a language as the server names it.
type Language struct {
	Name string // "English", "Japanese": the server's name, not an ISO code
	ID   int
}

// Revision is the release revision the server recorded at import: Version 1 is an
// original release; a v2 token, PROPER or REPACK raises it.
type Revision struct {
	Version  int
	IsRepack bool
}

// MediaInfo is the server's media analysis of a file.
type MediaInfo struct {
	VideoCodec string
	// AudioLanguages lists the language of every tagged audio track in track order,
	// trimmed and lowercased, duplicates kept; a track tagged "" or "und" is omitted
	// on every server, so an index is not a track index.
	AudioLanguages []string
}

// Cover is a cover image file as Sonarr or Radarr sent it.
type Cover struct {
	ContentType string // the media type the file's extension names, which the answer carried: "image/jpeg", "image/png" or "image/gif"
	Data        []byte // the whole file, never empty; the caller's to keep or change
}

// ReleaseSource is what made Sonarr or Radarr grab a release, as its grabbed history
// record names it. Its values are the server's own words for its ReleaseSourceType.
type ReleaseSource string

// The release sources both servers write.
const (
	SourceRSS               ReleaseSource = "Rss"               // the server's RSS sync
	SourceSearch            ReleaseSource = "Search"            // a search the server started on its own
	SourceUserInvokedSearch ReleaseSource = "UserInvokedSearch" // an automatic search a user or an API caller started
	SourceInteractiveSearch ReleaseSource = "InteractiveSearch" // a release a user or an API caller picked from a release search
	SourceReleasePush       ReleaseSource = "ReleasePush"       // a release pushed to the server's release/push endpoint
)

// CommandStatus is where a Sonarr or Radarr command is in its run, in the server's own
// words.
type CommandStatus string

// The command statuses both servers write.
const (
	CommandQueued    CommandStatus = "queued"
	CommandStarted   CommandStatus = "started"
	CommandCompleted CommandStatus = "completed"
	CommandFailed    CommandStatus = "failed"
	CommandAborted   CommandStatus = "aborted"
	CommandCancelled CommandStatus = "cancelled"
	CommandOrphaned  CommandStatus = "orphaned" // it was running when the server stopped
)

// DownloadState is where Sonarr or Radarr is with a download it tracks, in the server's
// own words for its TrackedDownloadState.
type DownloadState string

// The download states both servers write.
const (
	DownloadDownloading   DownloadState = "downloading"
	DownloadImportBlocked DownloadState = "importBlocked" // finished, and the server will not import it without the user
	DownloadImportPending DownloadState = "importPending"
	DownloadImporting     DownloadState = "importing"
	DownloadImported      DownloadState = "imported"
	DownloadFailedPending DownloadState = "failedPending" // failed at the client; the server has not yet handled it
	DownloadFailed        DownloadState = "failed"
	DownloadIgnored       DownloadState = "ignored"
)

// StatusMessage is one group of the server's notes on a queue item, such as why it
// will not import: a title, often a file or release name, and its lines. Both are
// server text.
type StatusMessage struct {
	Title    string
	Messages []string
}
