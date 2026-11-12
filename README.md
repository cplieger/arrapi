# arrapi

[![Go Reference](https://pkg.go.dev/badge/github.com/cplieger/arrapi/v3.svg)](https://pkg.go.dev/github.com/cplieger/arrapi/v3) [![Go version](https://img.shields.io/github/go-mod/go-version/cplieger/arrapi)](https://github.com/cplieger/arrapi/blob/main/go.mod) [![Mutation](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/arrapi/badges/mutation.json)](https://github.com/cplieger/arrapi/issues?q=label%3Agremlins-tracker)

arrapi gives your Go code typed clients for Sonarr and Radarr. They read a media library with its history, queue and tags, queue rescans and searches, retry brief failures and detect which Sonarr API the server speaks.

It replaces the `net/http` calls, response types, version checks and retry loop you would write around a Sonarr or Radarr instance. It depends on [httpclient](https://github.com/cplieger/go/tree/main/httpclient) for redirect policy and backoff and on [textsafe](https://github.com/cplieger/go/tree/main/textsafe) for log-safe error text, both from the same author. It is licensed under Apache-2.0.

## Why use it

arrapi is built for Go tools that read, rescan and search a Sonarr or Radarr library.

- It reads Sonarr 4 through `/api/v3`, Sonarr 5 through `/api/v5` and Radarr through `/api/v3`. A Sonarr 5 reachable only through `/api/v3` is refused, because it reports a series in another order as if it were aired order.
- Each client detects the API and returns one set of types for both Sonarr versions. `Info` reports which fields carry data.
- Every data and command answer is checked against the detected server version before it is decoded, so an upgrade makes the client detect again.
- Reads retry a 408, a 429, a 5xx other than 501, 505, 506, 508, 510 and 511, and transient network errors with jittered backoff. Commands are sent once.
- Tests check every decoded field and request path against upstream's OpenAPI documents.

Consider [golift/starr](https://github.com/golift/starr) if you need to edit media or reach Lidarr, Prowlarr or Readarr. Consider [devopsarr/sonarr-go](https://github.com/devopsarr/sonarr-go) or [radarr-go](https://github.com/devopsarr/radarr-go) if you want a generated client for every endpoint.

## Install

```sh
go get github.com/cplieger/arrapi/v3@latest
```

## Usage

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/sonarr"
)

func main() {
	ctx := context.Background()

	c, err := sonarr.New("http://sonarr:8989", "your-api-key")
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	// Check the connection and the API key, and see which API the server speaks.
	info, err := c.Info(ctx)
	if err != nil {
		log.Fatalf("sonarr unreachable: %v", err)
	}
	fmt.Printf("Sonarr %s over API %s\n", info.Version, info.API)

	series, err := c.Series(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// Keep series tagged "anime" that use TheTVDB's aired order.
	tags, err := c.Tags(ctx)
	if err != nil {
		log.Fatal(err)
	}
	anime := arrapi.TagIDs(tags, "anime")
	for _, s := range series {
		if arrapi.HasAnyTag(s.Tags, anime) && s.Order.Official {
			fmt.Printf("%s (tvdb %d, %d)\n", s.Title, s.IDs.TVDB, s.Year)
		}
	}
}
```

The `radarr` package has the same shape: `radarr.New`, `Info`, `Movies` and so on. The package examples on pkg.go.dev show more cases, and `go test` keeps them true.

## API

- `sonarr.New` and `radarr.New` take a base URL, an `arrapi.APIKey` and options, reject a malformed URL, an empty key or a key holding anything but letters, digits, `.`, `_` and `-`, and send no request.
- `Info` runs a fresh detection and reports the version, the API and, for Sonarr, the `Features` set.
- `sonarr.Check` and `radarr.Check` test a URL and key before you save them, and name what to fix in an `*arrapi.CheckError`'s `Reason` ([Connection check](docs/request-behavior.md#connection-check)).
- `sonarr.Client` reads `Series`, `SeriesByID`, `Episodes`, `EpisodesWithFiles`, `EpisodeFiles`, `HistorySince`, `Commands`, `Queue`, `Tags` and `RootFolders`, runs the interactive searches `SeasonReleases` and `EpisodeReleases`, and sends `RescanSeries`, `SeriesSearch`, `SeasonSearch`, `EpisodeSearch` and `Grab`.
- `sonarr.Client.Pin` returns a `sonarr.Pinned` with the same reads, all from one server version.
- `radarr.Client` reads `Movies`, `MovieByID`, `HistorySince`, `Commands`, `Queue`, `Tags` and `RootFolders`, runs the interactive search `MovieReleases`, and sends `RescanMovie`, `MoviesSearch` and `Grab`.
- `MediaCoverURL` builds an image URL under your base URL, `MediaCover` reads that image's bytes with your key up to a limit you pass, and `Series.WebURL` and `Movie.WebURL` link to an item's web UI page.
- `arrapi.TagIDs` and `arrapi.HasAnyTag` filter by tag label.
- The options are `WithHTTPClient`, `WithMaxAttempts`, `WithBaseDelay`, `WithTimeout` and `WithLogger`, in each package.
- Errors are `*arrapi.StatusError`, `*arrapi.ResponseTooLargeError`, `*arrapi.DecodeError`, `*arrapi.UnsupportedAPIError` with a reason and a fix, `*arrapi.ServerChangedError`, `*arrapi.CheckError`, `*arrapi.GrabError` and `arrapi.ErrCommandOutcomeUnknown`.

The full reference is on [pkg.go.dev](https://pkg.go.dev/github.com/cplieger/arrapi/v3). [Data types](docs/data-model.md) covers the normalized types, episode orders, audio languages, history, the command list, the queue and release searches.

## Version detection

The first call of a client asks `GET /api` which API the server announces, then reads `system/status` on that API to learn the application and its version. It keeps the result and uses it while every answer carries the same `X-Application-Version` header. `Info` always detects fresh and takes at most 5 seconds.

An answer from another version, or one without the header, ends that detection. A read that gets another version's answer, or a 408, a 429, or a 5xx other than 501, 505, 506, 508, 510 and 511 without the header, goes on within the same call when its attempt and detection budgets allow, as [Retries](docs/request-behavior.md#retries) describes. Otherwise it returns the error, and a later call detects.

A read can follow the server across an upgrade, so values from separate calls can come from different Sonarr versions. When you join several reads into one result, read them through one `Pin`, which refuses another version with an `*arrapi.ServerChangedError` instead. If a reverse proxy blocks `/api/v5` in front of a Sonarr 5, or strips `X-Application-Version`, the call fails with an `*arrapi.UnsupportedAPIError` that names the fix. [Request behavior](docs/request-behavior.md#detection) has the full rules.

## Retries and timeouts

- One call sends at most `WithMaxAttempts` attempts (default 3) and runs at most one detection.
- When your context has a deadline, that deadline covers every attempt and every wait. When it has none, `WithTimeout` (default 120s) limits each attempt, body decode included.
- Backoff starts at `WithBaseDelay` (default 1s) and doubles with jitter.
- A 4xx other than 408 and 429, and a 501, 505, 506, 508, 510 or 511, fail at once.
- The rescans, searches and grabs detect first, then send their request once, never through a redirect ([Commands](docs/request-behavior.md#commands)). An interactive release search is sent once too ([Release searches](docs/request-behavior.md#release-searches)).
- arrapi logs its retry and detection records, and a search answer it could not read, through `slog`, labeled `arrapi`, to `slog.Default()` unless you pass `WithLogger`. It logs nothing else.
- One client is safe for concurrent use and starts no goroutines.

[Request behavior](docs/request-behavior.md) has the full rules for retries, redirects, size limits and errors.

## Security

- Every response body is read through a size cap. A list may hold 64 MiB, a single object or a detection answer 1 MiB, a cover file the limit you pass to `MediaCover`, the body kept on a `*StatusError` 64 KiB, and a body arrapi discards 2 KiB. A body over its cap is rejected, never decoded cut. See [Size limits](docs/request-behavior.md#size-limits).
- The API key is redacted from every error, including text the server echoes back. A key may hold only letters, digits, `.`, `_` and `-`, which Go's and .NET's URL and JSON encoders leave as they are, so the key as sent is the only form they write. A key Sonarr or Radarr generates (32 hex digits) always passes. A key you set by hand (`Sonarr__Auth__ApiKey`, `Radarr__Auth__ApiKey` or `ApiKey` in `config.xml`) must use only those characters, or `New` refuses it. Every error's text is one line. See [Errors](docs/request-behavior.md#errors).
- Every read follows a redirect only to the same scheme, host and port, for at most 5 redirects. That holds for the default client and for one passed to `WithHTTPClient`, so a redirect never takes the API key to another origin or from `https` down to `http`. A redirect from `http` to `https` is refused with the URL to configure instead. See [Redirects](docs/request-behavior.md#redirects).
- An `http` base URL sends the API key unencrypted on every request. Configure an `https` base URL when the connection to the server needs to be confidential.

## Unsupported by design

arrapi reads a library, checks the connection, queues rescans and searches, and runs interactive release searches and grabs. It leaves out:

- Adding, editing or deleting media.
- Sonarr 3, and Sonarr 5 through its deprecated `/api/v3`.
- Quality profiles, custom formats, indexer, download-client and notification settings.
- The calendar, statistics, disk-space, health and wanted endpoints.
- TheTVDB's aired order for a series in another order. Neither Sonarr API reports it, so a caller that needs it reads it from the metadata source.

## Documentation

- [Request behavior](docs/request-behavior.md) is for callers who tune retries and timeouts, bring their own HTTP client, run behind a reverse proxy or log errors.
- [Data types](docs/data-model.md) is for callers who read episode orders, audio languages, history, file revisions or web links.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude](https://claude.com), [GPT](https://openai.com), and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

Apache-2.0. See [LICENSE](LICENSE).
