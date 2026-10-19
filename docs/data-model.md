# Data types

This page covers what arrapi's types carry on each server, and how episode orders, audio languages, history, file revisions, tags and links behave. Read it if your code reads episode numbers, file details or history, or runs against both Sonarr 4 and Sonarr 5. Every field and method is listed on [pkg.go.dev](https://pkg.go.dev/github.com/cplieger/arrapi/v3).

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

## Tags

- `TagIDs(tags, labels...)` returns the set of tag IDs whose labels match, after trimming whitespace and lowercasing both sides. It returns nil only when no label is given. Given labels that match nothing, it returns an empty set, so a filter that is on but matches nothing stays visible.
- `HasAnyTag(itemTags, ids)` reports whether a series or movie carries any of those IDs.

## Links and images

- `(*Series).WebURL(baseURL)` returns `{baseURL}/series/{titleSlug}` for Sonarr.
- `(*Movie).WebURL(baseURL)` returns `{baseURL}/movie/{tmdbID}` for Radarr, which addresses a movie's page by its TMDB ID.
- Each returns `""` when `baseURL` is empty or the item has no slug or TMDB ID. A trailing slash on `baseURL` is removed. A Sonarr slug comes from community-edited metadata, so it is percent-escaped into one path segment, and a slug of `.` or `..` has its dots encoded as `%2E`.
- `MediaCoverURL(ctx, id, file)` returns `{baseURL}/api/<api>/mediacover/{id}/{file}` for the API the client detected, such as `poster.jpg` or `fanart-360.jpg`. It sends no request beyond a detection and fetches nothing. `file` must be one file name ending in `.jpg`, `.png` or `.gif`, and `id` must be positive.

## Answers arrapi refuses to decode

- A `null` answer, a `null` item of a list, and a `null` item of an array inside a resource, such as `seasons: [null]`, are decode errors. Decoding them would hand you a zero-value series, season or tag ID. No Sonarr or Radarr route sends one. Arrays arrapi does not read are not checked.
- A decode error names the request path and, where it applies, the field and the item's index. It never quotes a value the server sent.

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
