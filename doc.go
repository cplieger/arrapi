// Package arrapi provides typed, resilient clients for the Sonarr and Radarr
// v3 HTTP APIs. NewSonarr and NewRadarr return distinct client types, so an
// operation can only be called on the service that supports it. Every request
// carries the instance's API key, is bounded by a per-request timeout and a
// response-size cap, and is retried with jittered backoff on transient
// failures; a non-2xx response is a *StatusError whose Body is log-safe. A
// client owns no goroutines and is safe for concurrent use.
package arrapi
