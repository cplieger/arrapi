// Package arrapi holds the vocabulary the sonarr and radarr packages share: the API
// key and generation types, the error types every call can return, tags, languages,
// revisions and media info. The clients live in those packages and read three server
// shapes: Sonarr v4 (API v3), Sonarr v5 (API v5) and Radarr (API v3). A Sonarr 5
// reachable only over /api/v3 is refused with an *UnsupportedAPIError, never read.
package arrapi
