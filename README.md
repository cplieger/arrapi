# arrapi

[![Go Reference](https://pkg.go.dev/badge/github.com/cplieger/arrapi.svg)](https://pkg.go.dev/github.com/cplieger/arrapi)
[![Go version](https://img.shields.io/github/go-mod/go-version/cplieger/arrapi)](https://github.com/cplieger/arrapi/blob/main/go.mod)
[![Test coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/arrapi/badges/coverage.json)](https://github.com/cplieger/arrapi/actions/workflows/coverage.yml)
[![Mutation](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/arrapi/badges/mutation.json)](https://github.com/cplieger/arrapi/issues?q=label%3Agremlins-tracker)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/13541/badge)](https://www.bestpractices.dev/projects/13541)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/cplieger/arrapi/badge)](https://scorecard.dev/viewer/?uri=github.com/cplieger/arrapi)

> Typed, resilient Go clients for the Sonarr and Radarr v3 APIs

A standalone Go library that wraps the [Sonarr](https://sonarr.tv) and [Radarr](https://radarr.video) v3 HTTP APIs behind two small, type-safe clients. Requests are authenticated, size-bounded, and retried on transient failures with jittered exponential backoff (via [`cplieger/httpx`](https://github.com/cplieger/httpx)). The only runtime dependencies are `httpx` and [`cplieger/runesafe`](https://github.com/cplieger/runesafe) (log-safe error bodies). The DTOs are curated field subsets of the arr resources, and a test-only schema-drift guard pins every carried field, its wire type, and every request path against the [devopsarr](https://github.com/devopsarr) OpenAPI-generated clients: when an upstream release renames, removes, or re-types a field, or moves an endpoint, the dependency bump fails CI instead of the change silently corrupting decodes.

## Design

Two constructors return two concrete types, so an operation can only be called against the service that supports it:

- `NewSonarr(...)` returns a `*Sonarr` with `GetSeries`, `GetEpisodes`, and `GetEpisodeFiles`.
- `NewRadarr(...)` returns a `*Radarr` with `GetMovies`.

Both embed a shared core exposing the endpoints common to either service (`GetTags`, `GetSystemStatus`, `Ping`, `Close`). This replaces the single-client-does-both shape where a wrong call (`GetMovies` on a Sonarr instance) is only caught at runtime.

## Install

`go get github.com/cplieger/arrapi@latest`

## Usage

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/cplieger/arrapi"
)

func main() {
	ctx := context.Background()

	sonarr, err := arrapi.NewSonarr("http://sonarr:8989", "your-api-key")
	if err != nil {
		log.Fatal(err)
	}
	defer sonarr.Close()

	// Verify connectivity + credentials up front (fails fast on a bad key).
	if err := sonarr.Ping(ctx); err != nil {
		log.Fatalf("sonarr unreachable: %v", err)
	}

	// Fetch the whole series library in one batched, retried request.
	series, err := sonarr.GetSeries(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// Tag filtering: keep series tagged "anime", drop those tagged "skip".
	tags, err := sonarr.GetTags(ctx)
	if err != nil {
		log.Fatal(err)
	}
	anime := arrapi.TagIDs(tags, "anime")
	skip := arrapi.TagIDs(tags, "skip")
	for _, s := range series {
		if arrapi.HasAnyTag(s.Tags, anime) && !arrapi.HasAnyTag(s.Tags, skip) {
			fmt.Printf("%s (tvdb %d, %d)\n", s.Title, s.TvdbID, s.Year)
		}
	}

	// Radarr is a separate typed client; options tune retry/timeout.
	radarr, err := arrapi.NewRadarr("http://radarr:7878", "your-api-key", arrapi.WithMaxAttempts(5))
	if err != nil {
		log.Fatal(err)
	}
	defer radarr.Close()

	movies, err := radarr.GetMovies(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d movies\n", len(movies))
}
```

## API

### Constructors

- `NewSonarr(baseURL, apiKey string, opts ...Option) (*Sonarr, error)`
- `NewRadarr(baseURL, apiKey string, opts ...Option) (*Radarr, error)`

`baseURL` must be an absolute `http(s)` URL with a host and no query or fragment (a path is allowed, for reverse-proxy sub-paths); `apiKey` must be non-empty. Both are validated at construction.

### Sonarr

- `GetSeries(ctx) ([]Series, error)` — every series in the library
- `GetSeriesByID(ctx, seriesID int) (Series, error)` — a single series by ID (`IsNotFound` reports a missing ID)
- `GetEpisodes(ctx, seriesID int) ([]Episode, error)` — episodes for a series, including episode-file details
- `GetEpisodeFiles(ctx, seriesID int) ([]EpisodeFile, error)` — the series' episode files from the dedicated episodefile endpoint: exactly the episodes with a file on disk, without the fileless rows `GetEpisodes` includes (a smaller payload on a long airing series); each file carries its `SeriesID` and `SeasonNumber`
- `GetEpisodeByID(ctx, episodeID int) (Episode, error)` — a single episode by ID (`IsNotFound` reports a missing ID)
- `RescanSeries(ctx, seriesID int) (Command, error)` — rescan the series' folder for new or changed files; returns the queued command
- `RefreshSeries(ctx, seriesID int) (Command, error)` — refresh series metadata and rescan; returns the queued command

### Radarr

- `GetMovies(ctx) ([]Movie, error)` — every movie in the library
- `GetMovieByID(ctx, movieID int) (Movie, error)` — a single movie by ID (`IsNotFound` reports a missing ID)
- `RescanMovie(ctx, movieID int) (Command, error)` — rescan the movie's folder for new or changed files; returns the queued command
- `RefreshMovie(ctx, movieID int) (Command, error)` — refresh movie metadata and rescan; returns the queued command

### Shared (both clients)

- `GetTags(ctx) ([]Tag, error)` — all tags defined on the instance
- `ResolveTagIDs(ctx, labels ...string) (ids map[int]struct{}, unmatched []string, err error)` — fetch tags and resolve labels to IDs in one call; returns the matched IDs and the labels that matched no tag (no labels = no request)
- `GetQualityProfiles(ctx) ([]QualityProfile, error)` — configured quality profiles
- `GetRootFolders(ctx) ([]RootFolder, error)` — configured root folders
- `GetSystemStatus(ctx) (SystemStatus, error)` — version and app name
- `GetHistorySince(ctx, since time.Time, eventTypes ...EventType) ([]HistoryRecord, error)` — history events on or after `since`, newest first; pass one or more `EventType`s to filter (client-side), or none for all
- `GetHistory(ctx, opts HistoryOptions) (HistoryPage, error)` — one page of history (newest first), bounded by page size for backfills and large scans
- `GetCommandByID(ctx, id int) (Command, error)` — the state of a queued command, to poll a rescan or refresh to completion
- `Ping(ctx) error` — connectivity + credential check with a short timeout (no retry)
- `Close()` — release idle connections; safe to call more than once

### History types

`GetHistorySince` returns `[]HistoryRecord` (`Date`, `EventType`, `SourceTitle`, `SeriesID`/`EpisodeID` for Sonarr or `MovieID` for Radarr, plus a `Data` map). `HistoryRecord.ImportedPath()` pulls the imported file path from a download-import event. `EventType` decodes both Sonarr's integer and Radarr's string encodings; the exported constants are `EventGrabbed`, `EventFolderImported`, `EventDownloadImported`, `EventDownloadFailed`, `EventFileDeleted`, `EventFileRenamed`, and `EventDownloadIgnored`. It implements `fmt.Stringer` for logs, and an unrecognized upstream event decodes to `0` with its raw name preserved in `HistoryRecord.RawEventType`. Event filtering is client-side: the arr `eventType` query parameter is numbered per service (Sonarr and Radarr disagree on the integers), so a server-side filter is not portable.

`GetHistory` returns a `HistoryPage` (`Records`, `Page`, `PageSize`, `TotalRecords`) for bounded paging; `HistoryOptions` sets `Page` and `PageSize`.

### Tag helpers (pure)

- `TagIDs(tags []Tag, labels ...string) map[int]struct{}` — resolve label names to their IDs (case-insensitive, whitespace-trimmed)
- `UnmatchedLabels(tags []Tag, labels ...string) []string` — the labels (verbatim) that match no tag, for flagging a misconfigured name
- `HasAnyTag(itemTags []int, ids map[int]struct{}) bool` — does an item carry any of those tag IDs

### Web deep-links

DTO methods that build a link to the item's page in the arr web UI:

- `(*Series).WebURL(baseURL string) string` → `{baseURL}/series/{titleSlug}` (Sonarr)
- `(*Movie).WebURL(baseURL string) string` → `{baseURL}/movie/{tmdbID}` (Radarr keys its web UI by the TMDB id)

Each returns `""` when `baseURL` or the required field (the Sonarr title slug / Radarr TMDB id) is empty, so a caller reads `""` as "no link". The Sonarr title slug is percent-escaped and confined to a single path segment — a `.`/`..` or slash-bearing slug can't break out into the path, query, or fragment — so a community-editable slug is safe to interpolate.

### Options

| Option               | Description                                                                           |
| -------------------- | ------------------------------------------------------------------------------------- |
| `WithHTTPClient(c)`  | Use a caller-owned `*http.Client` (share a pool, pin a CA, inject a test client)      |
| `WithMaxAttempts(n)` | Total attempts including the first, for a transient failure. Clamped to ≥1. Default 3 |
| `WithBaseDelay(d)`   | Base delay for the exponential backoff between retries. Default 1s                    |
| `WithTimeout(d)`     | Per-request timeout applied when the caller's context has no deadline. Default 120s   |

### Errors

Non-2xx responses surface as `*StatusError` (fields `Code`, `Path`, `Body`, and `RetryAfter`, the capped `Retry-After` hint on a `429`). It implements `httpx.Transient`, so a `429` or any `5xx` is classified as retryable and every `4xx` as permanent, and `httpx.RetryAfterHint`, so `httpx.RetryWithBackoff` waits out that capped `Retry-After` before the next retry instead of its jittered backoff. `IsNotFound(err)` and `IsRateLimited(err)` report whether an error is (or wraps) a `*StatusError` with a `404` or `429`. A response body that exceeds the size cap surfaces as `*ResponseTooLargeError` rather than being silently truncated.

The captured `Body` is made log-safe at capture (via [`cplieger/runesafe`](https://github.com/cplieger/runesafe)): the request API key is redacted, the body is capped at 64 KiB, and terminal-escape (C0/C1), bidi-control, and line-separator runes are replaced with spaces, with invalid UTF-8 mapped to `U+FFFD`. An arr error body is untrusted text that typically lands verbatim in consumer logs (`"error", err`), so the field itself is safe to log — no consumer-side escaping needed.

## Resilience

- Retries `429`, any `5xx`, and transient transport errors (timeouts, connection resets, DNS failures) with jittered exponential backoff (via `httpx.RetryWithBackoff`), honoring the server's `Retry-After` hint (capped) on a `429`. `4xx` (non-429) and non-transient transport errors fail immediately.
- Retry diagnostics are emitted through `httpx`'s default `slog` logger (a `Debug` line per retry, a `Warn` when retries are exhausted, tagged `arrapi`); the library owns no logger of its own and still returns typed errors regardless.
- Every request carries the `X-Api-Key` header and a `User-Agent`, and is bounded by `WithTimeout` (spanning the body decode) when the caller's context has none.
- Redirects are followed only within the same host (via `httpx.RedirectPolicyFunc` with `WithSameHost`), so the `X-Api-Key` is never forwarded to another origin (Go strips only `Authorization`/`Cookie` headers on a cross-host redirect, not custom ones). A same-host `http`->`https` upgrade is followed; a same-host `https`->`http` downgrade is refused so the key never rides a cleartext hop, and a cross-host redirect is refused outright. The policy matches on host only, not port, so a same-host redirect to a different port is also followed. A caller-supplied client via `WithHTTPClient` owns its own redirect policy.
- Response bodies are size-capped before decoding (64 MB for list endpoints, 1 MB for single objects); an over-cap body is rejected as `*ResponseTooLargeError` rather than truncated.
- Clients own no long-lived goroutines and hold no locks a caller can observe; a single client is safe for concurrent use.

## Timeouts and retries

arrapi bounds every request by context and retries only transient failures:

- A caller-supplied context deadline is the authoritative total budget across all attempts and backoffs. It is honored as-is; arrapi imposes no separate client-level ceiling on top of it.
- `WithTimeout` (default 120s) is a per-attempt budget, applied only when the caller's context carries no deadline of its own. The total is then bounded by the attempt count (`WithMaxAttempts`, default 3).
- Retries cover transient failures: HTTP 429 and 5xx (honoring a capped `Retry-After`), and transient transport errors (timeouts, connection resets, DNS). A 4xx other than 429 is permanent and fails fast.
- A timeout or deadline expiry is terminal, not a retryable condition: once the budget is exhausted the call stops rather than retrying. Mutations (rescan/refresh commands) are single-attempt and never retried.

## Unsupported by Design

Deliberate non-goals, not TODOs:

| Not included                                    | Rationale                                                                                                                                                                          |
| ----------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Adding / editing / deleting media               | This is a read + connectivity + rescan client, not a full library-management client. Use [golift/starr](https://github.com/golift/starr) or the devopsarr `*-go` clients for CRUD. |
| Quality-profile item / cutoff detail            | `QualityProfile` models identity (name + ID); the nested quality-item and custom-format tree is out of scope.                                                                      |
| Indexer / download-client / notification config | Management-plane surface with no consumer need.                                                                                                                                    |

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude Opus](https://www.anthropic.com/claude) and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

GPL-3.0 — see [LICENSE](LICENSE).
