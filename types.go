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
