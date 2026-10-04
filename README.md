# arrapi

[![Go Reference](https://pkg.go.dev/badge/github.com/cplieger/arrapi/v2.svg)](https://pkg.go.dev/github.com/cplieger/arrapi/v2) [![Go version](https://img.shields.io/github/go-mod/go-version/cplieger/arrapi)](https://github.com/cplieger/arrapi/blob/main/go.mod) [![Mutation](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/arrapi/badges/mutation.json)](https://github.com/cplieger/arrapi/issues?q=label%3Agremlins-tracker)

arrapi gives your Go code typed clients for the Sonarr and Radarr v3 APIs. They read a media library with its history and tags, queue rescans and retry brief failures.

It replaces the `net/http` calls, response types and retry loop you would otherwise write around a Sonarr or Radarr instance. It depends on [httpx](https://github.com/cplieger/httpx) for retries and [runesafe](https://github.com/cplieger/runesafe) for log-safe error text, both from the same author. It needs Go 1.27.1 or later and is licensed under Apache-2.0.

## Why use it

arrapi is built for Go tools that read, filter and rescan a Sonarr or Radarr library.

- Sonarr and Radarr get separate client types, so calling `Movies` on a Sonarr client is a compile error, not a 404.
- Reads retry a 429, any 5xx and transient network errors with jittered backoff, honoring `Retry-After` up to 60 seconds.
- The default client refuses a redirect to another host or from `https` to `http`, so the API key goes only to the host you set.
- Error bodies come capped at 64 KiB with the API key redacted, ready to log.
- Tests check every decoded field and request path against the OpenAPI documents in the Sonarr and Radarr repositories.

Consider [golift/starr](https://github.com/golift/starr) if you need to add, edit or delete media, reach Lidarr, Prowlarr or Readarr, or handle webhooks and custom scripts. Consider [devopsarr/sonarr-go](https://github.com/devopsarr/sonarr-go) or [radarr-go](https://github.com/devopsarr/radarr-go) if you want a generated client for every endpoint.

## Install

```sh
go get github.com/cplieger/arrapi/v2@latest
```

## Usage

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/cplieger/arrapi/v2"
)

func main() {
	ctx := context.Background()

	sonarr, err := arrapi.NewSonarr("http://sonarr:8989", "your-api-key")
	if err != nil {
		log.Fatal(err)
	}
	defer sonarr.Close()

	// Check the connection and the API key up front.
	if err := sonarr.Ping(ctx); err != nil {
		log.Fatalf("sonarr unreachable: %v", err)
	}

	// Fetch the whole series library in one retried request.
	series, err := sonarr.Series(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// Keep series tagged "anime", drop those tagged "skip".
	tags, err := sonarr.Tags(ctx)
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

	// Radarr has its own client type. Options tune retries and timeouts.
	radarr, err := arrapi.NewRadarr("http://radarr:7878", "your-api-key", arrapi.WithMaxAttempts(5))
	if err != nil {
		log.Fatal(err)
	}
	defer radarr.Close()

	movies, err := radarr.Movies(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d movies\n", len(movies))
}
```

The package examples on pkg.go.dev show the same flow and tag filtering.

## API

- `NewSonarr` and `NewRadarr` take a base URL, an `APIKey` and options, and reject a malformed URL or an empty key.
- `*Sonarr` adds `Series`, `SeriesByID`, `Episodes`, `EpisodeFiles`, `EpisodeByID`, `RescanSeries` and `RefreshSeries`.
- `*Radarr` adds `Movies`, `MovieByID`, `RescanMovie` and `RefreshMovie`.
- Both clients share `Tags`, `ResolveTagIDs`, `QualityProfiles`, `RootFolders`, `SystemStatus`, `History`, `HistorySince`, `CommandByID`, `Ping` and `Close`.
- `TagIDs`, `UnmatchedLabels` and `HasAnyTag` filter by tag label, and `Series.WebURL` and `Movie.WebURL` link to an item's page in the web UI.
- The options are `WithHTTPClient`, `WithMaxAttempts`, `WithBaseDelay`, `WithTimeout` and `WithLogger`. A client you pass with `WithHTTPClient` keeps its own redirect policy. Set its `CheckRedirect` as [Request behavior](docs/request-behavior.md#redirects-and-headers) shows, so the API key stays on your host.
- A non-2xx response is a `*StatusError`, an oversized body a `*ResponseTooLargeError`, and `IsNotFound` and `IsRateLimited` test for a 404 and a 429.

The full reference is on [pkg.go.dev](https://pkg.go.dev/github.com/cplieger/arrapi/v2). [Data types and helpers](docs/data-model.md) covers history events, file revisions, tag matching and web links.

## Retries and timeouts

- When your context has a deadline, that deadline covers every attempt and every wait between them. arrapi sets no shorter limit of its own.
- When your context has no deadline, `WithTimeout` (default 120s) limits each attempt, body decode included. `WithMaxAttempts` (default 3) sets how many attempts run.
- Backoff starts at `WithBaseDelay` (default 1s) and doubles with jitter.
- A `WithTimeout` or context-deadline expiry ends the call and is not retried.
- A 4xx other than 429 and a transport error that is not transient fail at once.
- `Ping` checks the connection and the API key with a 5-second timeout and no retry.
- Rescan and refresh commands are sent once and never retried.
- arrapi logs one Debug record per retry, one Debug record when a retried call succeeds and one Warn record when the retries run out, labeled `arrapi`. They go to `slog.Default()` unless you pass `WithLogger`, and arrapi logs nothing else.
- One client is safe for concurrent use. It starts no goroutines, and each call makes its own request and returns a slice no other call shares.

[Request behavior](docs/request-behavior.md) has the full rules for retries, redirects, size limits and errors.

## Unsupported by design

arrapi reads a library, checks the connection and queues rescans and refreshes. It leaves out:

- Adding, editing or deleting media.
- Quality-profile items, cutoffs and custom formats. `QualityProfile` carries the name and ID only.
- Indexer, download-client and notification settings.
- The queue, calendar, disk-space, health and wanted endpoints.

## Documentation

- [Request behavior](docs/request-behavior.md) is for callers who tune retries and timeouts, bring their own HTTP client or log errors.
- [Data types and helpers](docs/data-model.md) is for callers who read history, file revisions, tags, commands or web links.

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the design rules and the test suite.

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude](https://claude.com), [GPT](https://openai.com), and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

Apache-2.0. See [LICENSE](LICENSE).
