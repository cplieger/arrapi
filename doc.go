// Package arrapi provides typed, resilient read clients for the Sonarr and
// Radarr v3 HTTP APIs.
//
// Two constructors return two concrete client types, so an operation can only
// be called against the instance that supports it: NewSonarr returns a *Sonarr
// (GetSeries, GetEpisodes) and NewRadarr returns a *Radarr (GetMovies). Both
// embed a shared core that exposes the endpoints common to either service
// (GetTags, GetSystemStatus, Ping, Close).
//
// Every request is authenticated with the instance's X-Api-Key, bounded by a
// per-request timeout, and retried on transient failures (HTTP 429, any 5xx,
// and transient transport errors) with jittered exponential backoff via
// github.com/cplieger/httpx. Non-2xx responses surface as a *StatusError, which
// reports whether it was transient and lets callers detect a 404 with
// IsNotFound.
//
// Response bodies are size-bounded before decoding to guard against oversized
// or malicious payloads. The clients own no goroutines and hold no locks; a
// single client is safe for concurrent use.
package arrapi
