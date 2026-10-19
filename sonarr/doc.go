// Package sonarr reads a Sonarr server: Sonarr v4 over API v3, or Sonarr v5 over API v5.
// A Client detects which on its first call and returns the same types from both; Info
// reports the API and its Features. A Sonarr 5 reachable only over API v3 is refused,
// never read through API v3. A Client read can follow the server across an upgrade; reads
// through one Pin all come from one server version.
package sonarr
