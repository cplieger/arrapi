# Request behavior

This page sets out how arrapi builds, retries, bounds and reports every request. Read it if you tune retries and timeouts, pass your own `*http.Client`, or log arrapi's errors.

## Construction

- `baseURL` must be an absolute `http` or `https` URL with a host and no query or fragment. A path is allowed, for a reverse proxy that serves the app under a sub-path. A trailing slash is removed.
- The API key must not be empty or only whitespace. Both checks run in `NewSonarr` and `NewRadarr`, which return an error instead of a client.
- `APIKey` is its own type, so a URL variable and a key variable cannot be swapped without a compile error. An untyped string literal still converts to it.
- No exported function takes or returns an httpx type, so building a client needs no httpx import, and a new httpx major version does not change arrapi's signatures.

## Retries

- A read is retried on HTTP 429, on any 5xx, and on a transient transport error such as a network timeout, a connection reset or a DNS failure.
- Any other 4xx and any transport error that is not transient fail at once.
- `WithMaxAttempts(n)` sets the total attempts, the first included. A value below 1 means one attempt. The default is 3.
- The wait between attempts starts at `WithBaseDelay` (default 1s) and doubles after each retry. Each wait is drawn at random between half and all of the current delay.
- When a retried response carries a `Retry-After` header, the hint replaces that wait, capped at 60 seconds.
- A `WithTimeout` or context-deadline expiry ends the call and is not retried, whether it was your deadline or the per-attempt `WithTimeout`.
- Rescan and refresh commands are mutations, so they are sent once and never retried. Any 2xx counts as success.

arrapi logs one Debug record through `slog` for each retry, one Debug record when a retried call succeeds and one Warn record when the attempts run out, each labeled `arrapi`. With `WithMaxAttempts(1)` the final record is Debug instead. Without `WithLogger`, the records go to `slog.Default()`. arrapi writes no other log lines and returns every failure as an error.

## Timeouts

- A deadline on your context is the total budget across every attempt and backoff, and it is used as-is. arrapi sets no client-level ceiling that could cut it short.
- When your context has no deadline, `WithTimeout` (default 120s) bounds each attempt, including the decode of its body. The attempt count then bounds the total.
- A zero or negative `WithTimeout` or `WithBaseDelay` falls back to its default.
- `Ping` uses its own 5-second timeout and makes one attempt, so a wrong address or key fails fast at startup.

## Redirects and headers

Every request carries the `X-Api-Key` header and a `User-Agent` that names arrapi.

Go can forward `X-Api-Key` across a redirect, because `net/http` withholds only the headers it treats as sensitive, such as `Authorization` and `Cookie`. So the default client follows a redirect only when it stays on the same host name, for at most 10 hops:

- A same-host upgrade from `http` to `https` is followed.
- A downgrade from `https` to `http` is refused, so the key never travels in cleartext.
- A redirect to another host is refused, so the key never reaches another origin.
- The host name is compared without the port, so a same-host redirect to a different port is followed.

With `WithHTTPClient`, your client's transport, `Timeout` and redirect policy replace the default client's settings. arrapi's `WithTimeout` still bounds each attempt when your context has no deadline. A client that follows cross-host redirects, as `net/http`'s default policy does, sends the key to the redirect target. Set its `CheckRedirect` to `httpx.RedirectPolicyFunc(httpx.WithSameHost(true), httpx.WithMaxHops(10))` to keep both guards. `WithHTTPClient` is also how you share a connection pool, pin a CA with `httpx.CATransport`, or inject a test client.

## Size limits

Each response body is read up to a cap before it is decoded:

| Response | Cap |
| --- | --- |
| A list, such as series, movies, episodes or `HistorySince` | 64 MiB |
| A page from `History` | 64 MiB |
| A single object, such as a series by ID or the system status | 1 MiB |
| The body captured for a `*StatusError` | 64 KiB |

A body over its cap returns a `*ResponseTooLargeError` with the request path and the limit. It is never truncated and decoded.

## Errors

A non-2xx response returns a `*StatusError` with these fields:

- `Code` is the HTTP status.
- `Path` is the request path.
- `Body` is the response body, made safe to log as described below.
- `RetryAfter` is the capped `Retry-After` hint, set whenever the response carries the header, in practice on a 429 or a 503.

`IsTransient()` reports true for a 429 or any 5xx. `*StatusError` implements httpx's `Transient` and `RetryAfterHint` interfaces, so httpx's own retry helpers classify it the same way. `IsNotFound(err)` and `IsRateLimited(err)` report whether an error is, or wraps, a `*StatusError` with a 404 or a 429. The by-ID reads such as `SeriesByID` return a 404 `*StatusError` for an ID that does not exist.

`Body` is cleaned when it is captured, so it is safe to log as-is:

- The API key is removed, along with its whitespace-trimmed form, before and after the cleanup.
- C0 and C1 control characters, DEL, Unicode bidi controls and the U+2028 and U+2029 separators become spaces, and invalid UTF-8 becomes U+FFFD. CR and LF stay, because slog's handlers escape them.
- The body is cut at 64 KiB on a rune boundary. A body that was cut ends in `...`, so it is at most 64 KiB plus 3 bytes.

## Concurrency

A single `*Sonarr` or `*Radarr` is safe for concurrent use. It starts no goroutines and holds no locks a caller can see. Reads are not merged. Each call makes its own request and returns a slice no other call shares. To avoid repeated fetches, cache at your own layer. `Close` releases idle connections and is safe to call more than once.
