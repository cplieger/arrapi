# Data types and helpers

This page covers what arrapi's response types carry and how its history, revision, tag, command and web-link helpers behave. Read it if your code reads history, compares file revisions, filters by tag or links to the Sonarr or Radarr web UI. Every field and method is listed on [pkg.go.dev](https://pkg.go.dev/github.com/cplieger/arrapi/v2).

## Response types

Each type carries a chosen subset of the fields of its Sonarr or Radarr resource, not the full resource. They include the TVDB, TMDB and IMDb IDs, tags, titles, years, the quality profile ID and paths. They also carry the original language, release dates, scene numbering and file details such as the release group, size and media info.

The test suite keeps these types in line with upstream. It downloads the OpenAPI documents that Sonarr and Radarr commit in their own repositories, from each default branch, and checks three things:

- Every JSON field arrapi decodes exists in the document. A type both services share must exist in both documents. `HistoryRecord` is checked against the two together, because its series and episode fields come from Sonarr and its movie field from Radarr.
- Each field decodes the same JSON kind the document declares. `HistoryRecord.eventType` is the one recorded exception, because Sonarr sends an integer where its document declares a string.
- Every request path and query parameter arrapi builds exists in the document's paths.

So an upstream rename, removal or type change fails the next test run instead of decoding to a zero value. When the upstream download fails, the tests log a warning and use the last-known-good copies on this repository's `schema-mirror` branch, which a workflow refreshes daily. These are the only tests that use the network, and `go test -short ./...` skips them.

## History

`HistorySince(ctx, since, eventTypes...)` returns the events on or after `since`, newest first. Pass one or more `EventType` values to keep only those types, or none for every type. A zero `EventType` in the filter is ignored. The filter runs in arrapi after the response arrives. Sonarr and Radarr number the `eventType` query parameter differently, so a server-side filter would return the wrong events on one of them. `/history/since` has no page size, so a wide window can return a large payload, up to the 64 MiB list cap.

`History(ctx, opts)` returns one `HistoryPage` of the paged endpoint, sorted by date, newest first, for backfills and large scans. `HistoryOptions` sets `Page` and `PageSize`, and a zero value uses the server's default. A `HistoryPage` carries `Records`, `Page`, `PageSize` and `TotalRecords`.

A `HistoryRecord` carries `Date`, `EventType`, `SourceTitle`, `SeriesID` and `EpisodeID` from Sonarr or `MovieID` from Radarr, and a `Data` map of event details. `ImportedPath()` returns the imported file path from a download-import event, or `""` when there is none.

`EventType` decodes Sonarr's integer form and Radarr's string form into the same values. The constants are `EventGrabbed`, `EventFolderImported`, `EventDownloadImported`, `EventDownloadFailed`, `EventFileDeleted`, `EventFileRenamed` and `EventDownloadIgnored`. It implements `fmt.Stringer` for logs. An event arrapi does not model decodes to `0`, and `HistoryRecord.RawEventType` keeps its original token so it stays identifiable.

## File revisions

`EpisodeFile` and `MovieFile` carry `Quality`, which holds the `Revision` Sonarr or Radarr recorded when it imported the file. `Revision.Version` is 1 for an original release, and a `v2`-style token, a PROPER or a REPACK each raise it. A REPACK also sets `Revision.IsRepack`.

The value is the one recorded at import, so it survives a later rename that drops the token from the file name. `Quality` is nil when the payload carries no quality object, and `Quality.Revision` is nil when the quality carries no revision. The quality definition itself and the separate counter for REAL releases are not modeled.

## Episode files

`Episodes` returns every episode of a series, with the file details of each episode that has one. `SeasonEpisodes` returns the same for one season, and season 0 holds the specials. A file that spans several episodes appears on each of them. `EpisodeFiles` returns only the files on disk, from Sonarr's episode-file endpoint, which is a smaller payload on a long airing series. Each `EpisodeFile` carries its `SeriesID` and `SeasonNumber`, so no episode list is needed to place it in a season.

## Tags

- `TagIDs(tags, labels...)` returns the set of tag IDs whose labels match, ignoring case and surrounding whitespace.
- `UnmatchedLabels(tags, labels...)` returns the labels that match no tag, as given and in input order, so you can flag a misspelled tag name.
- `HasAnyTag(itemTags, ids)` reports whether a series or movie carries any of those IDs.
- `ResolveTagIDs(ctx, labels...)` fetches the tags and returns both the matched IDs and the unmatched labels in one call. With no labels it returns nil and sends no request.

## Commands

`RescanSeries` and `RescanMovie` ask the app to rescan the item's folder for new or changed files, for example after another tool wrote a subtitle. `RefreshSeries` and `RefreshMovie` refresh the metadata and then rescan.

Each returns the queued `Command` with its `ID`, `Name` and current `Status`. `Status` is one of `queued`, `started`, `completed`, `failed`, `aborted`, `cancelled` or `orphaned`. Call `CommandByID` to poll a command until it finishes.

## Web links

- `(*Series).WebURL(baseURL)` returns `{baseURL}/series/{titleSlug}` for Sonarr.
- `(*Movie).WebURL(baseURL)` returns `{baseURL}/movie/{tmdbID}` for Radarr, which addresses a movie's page by its TMDB ID.

Each returns `""` when `baseURL` is empty or the item has no title slug or TMDB ID, so `""` means no link. A trailing slash on `baseURL` is removed.

A Sonarr title slug comes from community-edited metadata, so it is percent-escaped into a single path segment, and a slug of `.` or `..` has its dots encoded as `%2E`. A slug cannot add a path, a query or a fragment to the link.
