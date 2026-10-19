# Request behavior

This page sets out how arrapi detects the server's API, sends, retries, bounds and reports every request. Read it if you tune retries and timeouts, pass your own `*http.Client`, run Sonarr or Radarr behind a reverse proxy, or log arrapi's errors.

## Construction

- `baseURL` must be an absolute `http` or `https` URL with a host and no query or fragment. A path is allowed, for a reverse proxy that serves the app under a sub-path. A trailing slash is removed.
- The API key must not be empty or only whitespace, and may hold only letters, digits, `.`, `_` and `-`. A key Sonarr or Radarr generates is 32 hex digits and always passes. A key you set by hand (`Sonarr__Auth__ApiKey`, `Radarr__Auth__ApiKey` or `ApiKey` in `config.xml`), which the server accepts as written, must use only these characters, or `New` refuses it. `sonarr.New` and `radarr.New` return an error instead of a client when a check fails. The error names the rule, never the URL or the key.
- `New` sends no request and starts no goroutine. The first call detects the API.
- `APIKey` is its own type, so a URL variable and a key variable cannot be swapped without a compile error. An untyped string literal still converts to it.
- No exported function takes or returns an httpx type, so building a client needs no httpx import.

## Detection

arrapi reads three server shapes:

| Server | API arrapi reads |
| --- | --- |
| Sonarr 4 | `/api/v3` |
| Sonarr 5 and later | `/api/v5` |
| Radarr | `/api/v3` |

A Sonarr 5 reachable only through `/api/v3` is refused. Its `/api/v3` reports each series in the series' own episode order as if it were TheTVDB's aired order, so a Sonarr 4 reader would read wrong season and episode numbers there.

A detection sends `GET /api`, which names the API the server announces, then `GET /api/<api>/system/status`, which names the application and its version. When `/api` is hidden, a Sonarr client tries `/api/v5/system/status` and then `/api/v3/system/status`, and a Radarr client tries `/api/v3/system/status`. Each detection sends two or three requests.

A detection fails with an `*arrapi.UnsupportedAPIError` when the server answers but cannot be read. Its `Reason` and its text name the fix:

| Reason | What happened | Fix |
| --- | --- | --- |
| `ReasonV5Unreachable` | a Sonarr 5 answers, but `/api/v5` does not | let `/api/v5` through the reverse proxy |
| `ReasonNoVersionHeader` | an answer lacks `X-Application-Version` | let the header through the reverse proxy |
| `ReasonNotAPI` | a status path answers with a web page | fix the base path in the URL |
| `ReasonWrongApp` | the URL answers as another application | point the URL at the right application |
| `ReasonUnsupportedVersion` | a Sonarr below 4, or one announcing API v3 at another major | run Sonarr 4 or 5 |
| `ReasonUnknownGeneration` | the server announces an API arrapi does not read | update arrapi |

Any other answer to a detection request is returned as it came. A 401 is a 401 `*arrapi.StatusError`, and a URL where every detection request answers 404 gives a 404 `*arrapi.StatusError`.

`Info` runs a fresh detection every time, sends one attempt per request and takes at most 5 seconds, so it suits a connection test. `Pin` also detects fresh, under the read budget ([Reads from one server version](#reads-from-one-server-version)). Every other call uses the client's current detection, or detects first when it has none.

## Why every answer needs `X-Application-Version`

Sonarr and Radarr put an `X-Application-Version` header on the answers their API controllers write under `/api`. An error their exception handler writes, such as their own 500s, can come without it, and so can a proxy's error page. arrapi compares the header of every data and command answer with the version the client detected before it reads the body. Detection's first probe, `GET /api`, is the one exception. It runs before any version is known, so arrapi decodes a successful answer there without the header. `system/status` must then name the server before arrapi picks an engine.

- An answer from another version is never decoded, and it ends the client's detection. When its attempt and detection budgets allow, the read then goes on within the same call with a detection that names the new server, as [Retries](#retries) describes. Otherwise it returns an `*arrapi.ServerChangedError`, and a later call detects. After an in-place upgrade from Sonarr 4 to Sonarr 5, the next answer carries the 5.x header and the read can move to `/api/v5`.
- A successful answer without the header is refused with `ReasonNoVersionHeader`, because it cannot show that the server changed.
- An error answer without the header becomes an `*arrapi.StatusError`, and it ends the client's detection. A 429 or a 5xx without the header is retried within the same call while the budgets allow, as [Retries](#retries) describes. Any other such error is returned at once, and a later call detects.

A detection stays in use until an answer shows another version or no header, or until a detection that reached the server fails. A failed `Info` that got an answer makes the next call detect again. A failed `Info` that reached nothing, such as a refused connection, changes nothing.

A different server that sends the identical version string cannot be told apart by any answer.

## Retries

One call sends at most `WithMaxAttempts` attempts (default 3) for its own request, wherever they go, and runs at most one detection.

- A 429 or a 5xx that carries the header is retried on the same detection.
- A 429 or a 5xx without the header, Sonarr's own 500s among them, is retried after one fresh detection when the call used a stored detection. So is a transport error on the hop of a redirect without the header. When the call already detected, both retry on that detection.
- Any other transient transport error, such as a refused connection, a reset or a per-attempt `WithTimeout` expiry, is retried on the same detection.
- When an answer shows another server version, or another call's answer did so before this attempt left, a read moves to a detection installed since, or detects when the call has not detected yet. Otherwise it returns an `*arrapi.ServerChangedError`.
- Any other 4xx, and a transport error that is not transient, fail at once.
- The wait before a retry is the answer's `Retry-After`, capped at 60 seconds. Without one it starts at `WithBaseDelay` (default 1s), doubles after each retry and is drawn at random between half and all of the current delay. A move to another server version waits nothing.

arrapi writes these `slog` records, each labeled `arrapi`:

| Message | Level | When |
| --- | --- | --- |
| `arrapi failed, retrying` | Debug | before each retry, with `attempt`, `max`, `delay`, `fresh` and `error` |
| `arrapi succeeded after retry` | Debug | when a retried call succeeds |
| `arrapi retries exhausted` | Warn, or Debug under `WithMaxAttempts(1)` | when the budget ends a call, with `attempts`, `bound` and `error` |
| `arrapi: detected` | Debug | after each detection, with `app`, `version`, `api` and `deprecated` |
| `arrapi: server version changed` | Info | when an answer names another version, with `app`, `from` and `to` |

Without `WithLogger`, the records go to `slog.Default()`. arrapi writes no other log lines and returns every failure as an error. No record carries the URL or the key. The `error` attribute is the error's text, so it can hold the server's message or the body of an error answer, made safe to log as described under [Errors](#errors).

## Timeouts

- A deadline on your context is the total budget across every attempt and wait, used as-is.
- When your context has no deadline, `WithTimeout` (default 120s) bounds each attempt, including the decode of its body. The attempt count then bounds the total.
- A zero or negative `WithTimeout` or `WithBaseDelay` falls back to its default.
- `Info` sets its own 5-second bound. A shorter deadline on your context wins.

## Rescans

`RescanSeries` and `RescanMovie` run a fresh detection, then send their command once. They never retry and never follow a redirect, because Go turns a `POST` answered with a 301, 302 or 303 into a `GET`, whose 2xx would read as the command's success.

- Any 2xx that carries the detected version is success.
- A 2xx from another version, or without the header, returns an error that satisfies `errors.Is(err, arrapi.ErrCommandOutcomeUnknown)`. The server may have run the command, and arrapi does not send it again.
- A redirect answer is an `*arrapi.StatusError` with its 3xx code. Sonarr answers 307 to a URL that lacks its base path, so a rescan through such a URL fails while reads follow the redirect.
- When an answer after the detection showed another server version or carried no header, the command is not sent, and the call returns an `*arrapi.ServerChangedError`.

## Your own HTTP client

`WithHTTPClient(c)` sends every request, detection included, through copies of `c` whose `Transport` wraps the one in `c`, so arrapi sees every answer and every redirect. Your client keeps its connection pool, `Jar` and `Timeout`.

- Reads keep the [redirect rules](#redirects) of the default client whatever `c.CheckRedirect` allows, so the key never follows a redirect to another origin. `c.CheckRedirect`, when set, runs after them and can refuse more. Commands follow none.
- `c.Transport` must not follow redirects itself, as the `http.RoundTripper` contract requires. A transport that does hides a redirect from arrapi and can turn a command into a `GET` that reads as success.
- arrapi's `WithTimeout` still bounds each attempt when your context has no deadline.

`WithHTTPClient` is also how you share a connection pool, pin a CA with `httpx.CATransport`, or inject a test client.

## Redirects

Every request carries the `X-Api-Key` header and a `User-Agent` that names arrapi. Go forwards `X-Api-Key` across a redirect, because `net/http` withholds only the headers it treats as sensitive, such as `Authorization` and `Cookie`. So every read, through the default client or your own, follows a redirect only to the same origin, the same scheme, host and port:

- A redirect to another host or another port is refused, so the key never reaches another origin. The error reads `arrapi: <path> redirect to <origin> refused`.
- A redirect from `http` to `https` on the same host is refused too. The error names the URL to configure, base path included, for example `change the url to https://sonarr.example:8989/sonarr`.
- A redirect from `https` to `http` is refused, so a redirect never moves the key off an encrypted connection. An `http` base URL sends it unencrypted from the first request.
- At most 5 redirects are followed. The 6th fails with `arrapi: <path> stopped after 5 redirects`.

Every refusal by these rules wraps an `*httpx.RedirectRefusedError` you can match with `errors.As`, and its text names only the target origin, never the target's path or query. A hop your `CheckRedirect` refuses returns your error.

A redirect a reverse proxy writes without `X-Application-Version` is followed, and the answer at its target is judged. A redirect a call ends on without the header ends the client's detection, as an error answer without the header does. A refused redirect is returned at once, and a later call detects.

## Size limits

Each response body is read up to a cap before it is decoded:

| Response | Cap |
| --- | --- |
| A list, such as series, movies, episodes or `HistorySince` | 64 MiB |
| A single object, such as a series by ID, or a detection answer | 1 MiB |
| The body kept on a `*StatusError` | 64 KiB |
| A body arrapi discards, read only to reuse the connection | 2 KiB |

A body over its cap returns an `*arrapi.ResponseTooLargeError` with the request path and the limit. It is never truncated and decoded.

## Errors

A GET succeeds only on 200, and a command on any 2xx. Any other answer returns an `*arrapi.StatusError`:

- `Code` is the HTTP status. A 204 or 206 to a GET is a `*StatusError` too, never decoded.
- `Path` is the request path.
- `Message` is the server's one-line message, at most 512 bytes, or `""` when it sent none.
- `Body` is the response body, made safe to log as described below, at most 64 KiB. A body that was cut ends in `...`.

`Error()` renders `arrapi: <path>: HTTP <code>`, then the message, else the body, on one line. `IsTransient()` reports true for a 429 or a 500 through 599. `*StatusError` implements httpx's `Transient` and `RetryAfterHint` interfaces.

A by-ID read such as `SeriesByID` returns `(zero, false, nil)` for a 404 that carries the detected version. A 404 without the header is a proxy's page, not Sonarr's answer, so it is a 404 `*StatusError`.

An `*arrapi.ServerChangedError` says an answer came from another server version than the one the call relied on, or that version can no longer be confirmed. Its text names the origin and the versions, never a path. A later call uses a current detection or detects again. It wraps the answer's `*StatusError` when the answer was not a success.

A body that does not decode returns an error naming the path, the field and the Go type, never a value the server sent.

Every error is safe to log:

- The API key is redacted from every field and every error text. The encoders that can write the key into an answer, Go's `net/url` and `encoding/json` and .NET's `Uri.EscapeDataString`, `WebUtility.UrlEncode` and System.Text.Json, leave a key of letters, digits, `.`, `_` and `-` as it is. So the key as sent is the only form those encoders write. A JSON writer may escape any character (RFC 8259 section 7), so an echo written another way is outside this guarantee.
- C0 and C1 control characters, DEL, Unicode bidi controls and the U+2028 and U+2029 separators become spaces, and invalid UTF-8 becomes U+FFFD. CR and LF stay in `Body`, for a sink that escapes them, as slog's handlers do. `Message` and the `Error()` text of every error arrapi returns are one line: there CR and LF become spaces too. An error you reach inside it with `errors.As`, such as a TLS certificate error, renders its own text.
- A transport error carries no URL, so a redirect target's path and query never reach it.

## Values across an upgrade

A detection serves one server version, so the values you hold from calls made before and after an in-place upgrade can come from different APIs. Each value is correct for the server that answered. `Info.Features` describes the detection behind that `Info`, so read the per-item fields, such as `Series.Order.Official`, when you hold values across calls.

## Reads from one server version

`sonarr.Client.Pin` runs a fresh detection and returns a `sonarr.Pinned`, which has the client's eight reads. Every value a `Pinned` read returns comes from an answer that carries the pinned `X-Application-Version`, so a series list and the episodes you read for it are numbered in one order even when Sonarr is upgraded during your pass.

- `Pin` gives each detection request the same attempts and backoff as a read. Only your context bounds it, where `Info` sends one attempt per request.
- A `Pinned` read retries as a client read does. When its detection is stale, it re-detects or reuses a detection, and continues only when that detection names the pinned version.
- When Sonarr answers as another version, a minor upgrade included, a `Pinned` read returns an `*arrapi.ServerChangedError` and never switches APIs. It keeps doing so until an answer from the pinned version reaches the client again. Take a new `Pin` to read the new version.
- An answer without `X-Application-Version` makes the next `Pinned` reads re-detect. Sonarr 4, and Sonarr 5 before 5.0.0.2701, send it on no error answer their exception handler writes, such as a list read for a series that no longer exists. Current Sonarr 5 sends it on its API error answers.
- `Pinned.Info` reports the pinned detection without a request. Its `Features` hold for every read of that `Pinned`.

Radarr has one API, so `radarr.Client` has no `Pin`.

A URL from `MediaCoverURL` keeps the API path of the detection it was built from. After an upgrade it names `/api/v3` until another call's answer ends that detection. `Info` and the rescans detect fresh, so either one ends it.

## Concurrency

A single `sonarr.Client` or `radarr.Client` is safe for concurrent use and starts no goroutines. Concurrent first calls share one detection. Reads are not merged, so each call makes its own request and returns a slice no other call shares. Cache at your own layer to avoid repeated fetches. Several clients are independent, each with its own detection. `Close` releases idle connections and is safe to call more than once.
