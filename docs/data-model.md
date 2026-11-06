# Data types

This page covers what arrapi's types carry on each server, and how episode orders, audio languages, history, the queue, release searches, file revisions, tags and links behave. Read it if your code reads episode numbers, file details or history, or runs against both Sonarr 4 and Sonarr 5. Every field and method is listed on [pkg.go.dev](https://pkg.go.dev/github.com/cplieger/arrapi/v3).

## One set of types for both Sonarr versions

The `sonarr` package returns the same `Series`, `Season`, `Episode` and `EpisodeFile` types from Sonarr 4 and Sonarr 5. Each type carries a chosen subset of its resource's fields, not the full resource. A fact only Sonarr 5 reports reads as its zero value on Sonarr 4, so every field is safe to read on both. `Info.Features` says which facts carry data on the server the client detected:

| Field of `Features` | Sonarr 4 | Sonarr 5 | Meaning when true |
| --- | --- | --- | --- |
| `SeriesOrders` | false | true | `Series.Order` can name an order other than TheTVDB's aired order |
| `SeasonTitles` | false | true | `Season.Title` can be set |
| `Translations` | false | true | `Series.Language`, `OriginalTitle` and `Translations` are filled |
| `AnimeIDs` | false | true | `Series.IDs.AniList` is filled where the server knows it |

`Episode.TVDBID` is the episode's TVDB ID on both Sonarr versions, and 0 means unknown. A series Sonarr 5 last refreshed while its metadata source sent no episode TVDB IDs reads 0 on every episode until its next refresh.

The `radarr` package has its own `Movie`, `MovieFile`, `EventType` and `HistoryRecord` types, and no `Features` set.

## Episode orders

`Series.Order` names the series' episode order:

- `Type` is the server's code, such as `official`, `dvd`, `alternate` or `alttwo`.
- `Name` is the order's display name, such as "Aired Order", "DVD Order" or "Superfan Order".
- `Official` is true when `Type` is `official`, meaning the season and episode numbers are TheTVDB's aired order.

Sonarr reuses a code with different meanings across series. `alttwo` is "Superfan Order" for The Office and "3 Episode Order" for Shogun, so compare orders by `Name`. `Name` is `""` when the server lists no order of that type, which Sonarr 5 does for a series it has not refreshed since its upgrade. On Sonarr 4 every series reads `{Type: "official", Official: true}` with an empty `Name`.

`Season.Number`, `Episode.Season`, `Episode.Number` and `EpisodeFile.Season` are in the series' own order. For a series in another order, neither Sonarr API reports TheTVDB's aired season and episode number, and `Episode.Absolute` reads 0. A caller that needs aired order reads it from the metadata source, joined on `Series.IDs.TVDB`, `Order` and the episode's season and number.

## Series types

`Series.Type` is how Sonarr numbers and searches the series' episodes, on both Sonarr versions: `SeriesStandard` (season and episode numbers), `SeriesDaily` (air dates) or `SeriesAnime` (absolute episode numbers). It reads `""` when the server sent no type or one arrapi does not know, so treat `""` as unknown. A series whose type is not one of Sonarr's own still decodes, and the rest of the list with it.

## Episodes and files

- `Episodes` returns every episode of a series, each with `HasFile` and `FileID`, and `File` nil.
- `EpisodesWithFiles` returns the same episodes with `File` set on each episode that has one.
- `EpisodeFiles` returns only the files on disk, a smaller answer on a long series. Join them to episodes on `Episode.FileID`.

A file that holds several episodes appears on each of them with the same `FileID`. Each `EpisodeFile` carries its `Season`, so no episode list is needed to place it.

`EpisodeFile.MediaInfo` and `MovieFile.MediaInfo` hold `VideoCodec` and `AudioLanguages`. `AudioLanguages` lists the language of every tagged audio track in track order, lowercased, duplicates kept. Sonarr 4 and Radarr send the languages as one joined string and Sonarr 5 as one entry per stream. On every server a track tagged `""` or `und` is left out, so an index into the list is not a track index. Subtitle tracks are not modeled.

## File revisions

`EpisodeFile.Revision` and `MovieFile.Revision` hold the revision the server recorded when it imported the file. `Revision.Version` is 1 for an original release, and a `v2`-style token, a PROPER or a REPACK each raise it. A REPACK also sets `Revision.IsRepack`. The value survives a later rename that drops the token from the file name. `Revision` is nil when the answer carries no quality or no revision.

## History

`HistorySince(ctx, since, types...)` returns, in one request, every history record dated at or after `since`, oldest first. `since` is sent at second precision. Pass one or more `EventType` values to keep only those types, or none for every type. The filter runs in arrapi after the answer arrives, because `/history/since` takes only one event type. A zero `EventType` matches nothing. `/history/since` has no page size, so a wide window can return a large answer, up to the 64 MiB list cap.

Sonarr joins each record with its series and its episode, and Radarr with its movie. A deleted item's records are not returned, and a later call never brings them back.

A `HistoryRecord` carries `ID`, `Date`, `EventType` and `SeriesID` from Sonarr or `MovieID` from Radarr. The two services number their events differently, so each package has its own `EventType` constants: `EventGrabbed`, `EventFolderImported`, `EventDownloadImported`, `EventDownloadFailed`, `EventFileDeleted`, `EventFileRenamed` and `EventDownloadIgnored`. An event token arrapi does not know decodes to 0. `EventType` implements `fmt.Stringer` for logs.

Each record also carries the fields a download leaves behind:

- `DownloadID` is the download client's id for the release. The grabbed record, the queue item and the later import, failed and ignored records of one download share it.
- `SourceTitle` is the release title. On a file-deleted or file-renamed record it is the file path the server recorded.
- `GUID`, `Indexer` and `InfoURL` name the release at its indexer, on grabbed records. Radarr and Sonarr 5 also write `Indexer` on failed and ignored records; Sonarr 4 does not.
- `Source` is what made the server grab the release, one of `arrapi.SourceRSS`, `SourceSearch`, `SourceUserInvokedSearch`, `SourceInteractiveSearch` and `SourceReleasePush`. It is "" on other events, for the server's `Unknown`, and for a value arrapi does not know.
- `Message` is the server's reason on a failed or ignored record. A failed download is blocklisted by both servers, so this is also the blocklisting's reason.
- `EpisodeID`, Sonarr only, is the episode the record is about. Sonarr writes one record per episode, so a season pack's grab is several records sharing one `DownloadID`.

These strings are the server's text, unchecked: render them through an escaping template. A `GUID` can be a link carrying the indexer's passkey, so log neither it nor `InfoURL`.

## Commands

`Commands(ctx)` returns, in one request, every command the server holds in memory: the queued and running ones, and those that ended in the last 5 to 10 minutes, since the server drops an ended command after 5 minutes on a 5-minute cleanup. The list is in the server's order (queued and running first, then by priority), not by time, and a restart empties it. `sonarr.Pinned` has it too.

A `Command` carries `ID`, `Name` (the command class without its `Command` suffix, such as `RescanSeries`, `RefreshSeries` or `MoviesSearch`), `Message` (the server's latest progress or completion text), `Queued`, and `Started` and `Ended`, which stay zero until the command starts and ends. `Status` is one of `arrapi.CommandQueued`, `CommandStarted`, `CommandCompleted`, `CommandFailed`, `CommandAborted`, `CommandCancelled` and `CommandOrphaned` (it was running when the server stopped), and "" for a status arrapi does not know.

`SeriesIDs` (Sonarr) or `MovieIDs` (Radarr) are the items the command's body names, merged from its list and single id members, in that order and without repeats, and nil when it names none. `NewSeries` or `NewMovie` is set on the `RefreshSeries` or `RefreshMovie` the server queues for an item just added or imported. `Name` and `Message` are the server's text, unchecked: render them through an escaping template.

## Queue

`Queue(ctx)` returns, in one request, the server's whole queue from `/queue/details`: every download it tracks at its download clients, matched to a series or movie or not, and every release it holds back before sending it to a client, such as one waiting on a delay profile. `sonarr.Pinned` has it too. The server refreshes the queue from its clients about once a minute, and 5 seconds after a grab.

A `QueueItem` carries:

- `Title`, the release title without its file extension.
- `DownloadID`, the download client's id for it, the same `DownloadID` its history records carry, and `DownloadClient`, the client's name in the server. A held-back release has no `DownloadID`.
- `State`, one of `arrapi.DownloadDownloading`, `DownloadImportBlocked` (finished, and the server will not import it without the user), `DownloadImportPending`, `DownloadImporting`, `DownloadImported`, `DownloadFailedPending` (failed at the client, not yet handled by the server), `DownloadFailed` and `DownloadIgnored`. It is "" for a held-back release and for a state arrapi does not know.
- `Messages`, the server's notes on the item as `arrapi.StatusMessage` groups, each a `Title` (often a file or release name) and its `Messages` lines, such as why it will not import. nil when there are none.
- `SeriesID` and `EpisodeIDs` from Sonarr, `MovieID` from Radarr, each 0 or nil when the server matched the item to none.
- `Size` and `SizeLeft` in bytes. `Size` is 0 when the client has not reported one.

Each download is one item on every server, so you can key the queue by `DownloadID`. Sonarr 4 writes one row per episode of a download, and arrapi folds the rows that share a download client and a `DownloadID` into one item whose `EpisodeIDs` lists their episodes, as Sonarr 5 does. A held-back release has no `DownloadID` to fold on, so on Sonarr 4 it stays one item per episode, while Sonarr 5 lists it once.

The string fields are the server's text, unchecked: render them through an escaping template.

## Release searches

`SeasonReleases`, `EpisodeReleases` and `MovieReleases` return the server's interactive search results, best first, each package with its own `Release` type. [Release searches](request-behavior.md#release-searches) covers how a search is sent. A `Release` carries:

- `GUID` and `IndexerID`, the pair `Grab` names, and `Indexer`, the indexer's name in the server.
- `Title`, `Quality` (the quality's name, such as `Bluray-1080p`), `Size` in bytes and `CustomFormatScore`.
- `InfoHash`, `InfoURL` and `CommentURL`, each "" when the indexer sent none.
- The server's decision: `Approved`, `TemporarilyRejected` (it would hold the release, for a delay profile say) or `Rejected`, with its reasons in `Rejections`. Sonarr 5 sends each rejection as an object, and arrapi keeps its message, the text Sonarr 4 and Radarr send.
- `Weight`, the server's rank: 0 is its best, and the list is in this order.
- From Sonarr, `MappedSeriesID` and `MappedEpisodes`, each a `ReleaseEpisode` with `ID`, `SeasonNumber`, `EpisodeNumber` and `Absolute`. From Radarr, `MappedMovieID`. Each is 0 or nil when the server mapped the title onto nothing.
- `Flags`, the indexer's flags.

`Flags` is an `IndexerFlags` bit set of the release's own package. Sonarr sends an integer and Radarr a list of names, and the two give one name different bits: `sonarr.FlagScene` is 16 and `radarr.FlagScene` is 128. Test a flag with its own package's constant, as in `r.Flags&radarr.FlagScene != 0`. A name in Radarr's list that arrapi does not know sets no bit. `sonarr.FlagSubtitles` is Sonarr 5's, and Sonarr 4 never sets it.

A release holds no download or magnet link. The server's answer carries both, and either can carry an indexer key or a tracker passkey, so arrapi never decodes them. `GUID`, `InfoURL` and `CommentURL` are the indexer's own values and can carry one too, so keep them out of logs.

## Tags

- `TagIDs(tags, labels...)` returns the set of tag IDs whose labels match, after trimming whitespace and lowercasing both sides. It returns nil only when no label is given. Given labels that match nothing, it returns an empty set, so a filter that is on but matches nothing stays visible.
- `HasAnyTag(itemTags, ids)` reports whether a series or movie carries any of those IDs.

## Links and images

- `(*Series).WebURL(baseURL)` returns `{baseURL}/series/{titleSlug}` for Sonarr.
- `(*Movie).WebURL(baseURL)` returns `{baseURL}/movie/{tmdbID}` for Radarr, which addresses a movie's page by its TMDB ID.
- Each returns `""` when `baseURL` is empty or the item has no slug or TMDB ID. A trailing slash on `baseURL` is removed. A Sonarr slug comes from community-edited metadata, so it is percent-escaped into one path segment, and a slug of `.` or `..` has its dots encoded as `%2E`.
- `MediaCoverURL(ctx, id, file)` returns `{baseURL}/api/<api>/mediacover/{id}/{file}` for the API the client detected, such as `poster.jpg` or `fanart-360.jpg`. It sends no request beyond a detection and fetches nothing. `file` must be one file name ending in `.jpg`, `.png` or `.gif`, and `id` must be positive.
- `MediaCover(ctx, id, file, limit)` reads the file `MediaCoverURL` names, with the client's key, so your own code never sends the key or builds a credentialed fetch. It returns an `arrapi.Cover`: `ContentType` is the media type the file's extension names (`image/jpeg` for `.jpg`, `image/png` for `.png`, `image/gif` for `.gif`), and `Data` is the whole file, never empty. It returns `(zero, false, nil)` when the server has no such file. `limit` is the most bytes you accept, and must be 1 or more. `file` and `id` follow `MediaCoverURL`'s rules. It is a read, so after an upgrade it moves to the new API as every read does. [Size limits](request-behavior.md#size-limits) covers what it refuses.

## Answers arrapi refuses to decode

- A `null` answer, a `null` item of a list, and a `null` item of an array inside a resource, such as `seasons: [null]`, are decode errors. Decoding them would hand you a zero-value series, season or tag ID. No Sonarr or Radarr route sends one. Arrays arrapi does not read are not checked.
- An `*arrapi.DecodeError` names the request path and, where it applies, the field and the item's index. Its text never quotes a value the server sent; the one exception, in the decoder's error it wraps, is in [Request behavior: Errors](request-behavior.md#errors).

## How the types stay in line with upstream

The test suite checks each decoded type against the OpenAPI document of its server shape:

| Server shape | Document |
| --- | --- |
| Sonarr 4 (API v3) | Sonarr's `v4-develop` branch, `src/Sonarr.Api.V3/openapi.json` |
| Sonarr 5 (API v5) | Sonarr's `develop` branch, `src/Sonarr.Api.V5/openapi.json` |
| Radarr (API v3) | Radarr's `develop` branch, `src/Radarr.Api.V3/openapi.json` |

The Sonarr 4 document comes from `v4-develop`, because `develop`'s `Sonarr.Api.V3` is Sonarr 5's deprecated view. Each check covers three things:

- Every JSON field arrapi decodes exists in the document, with the JSON kind it declares.
- Every request path and query parameter arrapi sends exists in the document.
- Every history event token arrapi maps is still one of the document's values.

So an upstream rename, removal or type change fails the next test run instead of decoding to a zero value. When a download fails, the tests log a warning and use the last-known-good copies on this repository's `schema-mirror` branch, which a workflow refreshes daily. These are the only tests that use the network, and `go test -short ./...` skips them.
