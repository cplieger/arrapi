# Contributing to arrapi

Notes on the client design, the retry contract, and the test suite. The
type-safe surface and the transient-retry behavior are the point of the
library, so most of this guide is about preserving them.

## What the library is

`arrapi` wraps the Sonarr and Radarr v3 HTTP APIs behind two concrete
clients built on a shared, unexported core. Its only runtime dependency is
[`cplieger/httpx`](https://github.com/cplieger/httpx), which supplies the
retry loop and transient-error classification.

Two invariants are load-bearing:

- **Type safety over the API surface.** Sonarr-only operations live on
  `*Sonarr`, Radarr-only operations on `*Radarr`, and the endpoints common
  to both (tags, system status, ping) live on the embedded `*client` so they
  are promoted to both. Do not collapse the two types back into one client
  that exposes every endpoint — the split is what makes a wrong call a
  compile error instead of a runtime 404.
- **Transient failures are retried; permanent ones are not.** A non-2xx
  response becomes a `*StatusError`, whose `IsTransient()` returns true for
  `429` and any `5xx`. That method is how `httpx.RetryWithBackoff` decides to
  retry (via the `httpx.Transient` interface), so keep `StatusError`
  satisfying `httpx.Transient` and keep the 429/5xx classification. Transport
  errors are classified by `httpx.IsTransient` (timeouts, resets, DNS).

When you add an endpoint:

- Route it through `fetchAll[T]` (list) or `fetchOne[T]` (single object) so
  it inherits authentication, the per-request timeout, bounded reads, and
  retry. Don't build a bespoke request path.
- Bound every response body before decoding (`maxListBytes` /
  `maxObjectBytes`), and cap captured error bodies at `maxErrorBodyBytes`.
- Numeric path parameters (a series ID) are safe to interpolate because they
  are already `int`; never interpolate an unvalidated string into a path.
- Add the DTO to `types.go` with fields ordered for `fieldalignment`
  (pointers/slices/strings, then ints, then bools last), and mirror the
  real arr JSON field names.

## Scope

The library covers the **read + connectivity** surface. The write/command
surface (history polling, rescan/refresh), quality-profile and root-folder
getters, and request coalescing are intentionally not implemented yet — see
the "Unsupported by Design" table in `README.md`. Add them as non-breaking
methods when an internal consumer actually needs them, so the API is shaped
by real usage rather than guessed at.

## Local development

The module targets the Go version pinned in `go.mod`. Use that toolchain or
newer.

```sh
go build ./...
go test ./...
go test -race ./...
```

### Linting and formatting

Lint config lives in `.golangci.yaml` (golangci-lint v2): `gosec`,
`gocritic`, `revive`, `gocyclo`, `gocognit`, `sloglint` (kv-only), and
others. Formatting is `gofumpt` (`extra-rules`) plus `gci` import grouping
(standard → third-party); `golangci-lint run` reports unformatted files as
issues, so format before pushing.

```sh
golangci-lint run
golangci-lint fmt
```

### Mutation testing

`.gremlins.yaml` configures [Gremlins](https://gremlins.dev) mutation
testing (synced from `cplieger/ci`; change it upstream). Run it locally to
confirm new tests actually kill mutants:

```sh
gremlins unleash .
```

## Test suite conventions

Tests are black-box (`package arrapi_test`) and exercise the public API
through an `httptest.Server` standing in for the arr instance (an unmanaged
dependency — the correct thing to fake). Match the file to the unit:

- `arrapi_test.go` — construction validation, request path/header
  assertions, decode, retry-on-transient, exhaustion, timeout, and context
  cancellation, all driven through the exported clients.
- `tags_test.go` — table-driven tests for the pure `TagIDs` / `HasAnyTag`
  helpers.
- `errors_test.go` — `StatusError` formatting, `IsTransient` classification,
  and `IsNotFound`.

Conventions that matter here:

- Set `WithBaseDelay(time.Millisecond)` in retry tests so the suite stays
  fast; assert the attempt count with an atomic counter in the handler.
- Assert observable behavior (returned values, request path, error type via
  `errors.As`), not internals.
- There is no bespoke parser to fuzz — parsing is delegated to
  `encoding/json`. If you add real byte/string parsing, add a `testing.F`
  fuzz target for it.

## Commits and PRs

Branch from `main`, keep changes focused with tests, and open a PR. This
account uses [Conventional Commits](https://www.conventionalcommits.org/)
parsed by git-cliff (`cliff.toml`), so the commit type drives the version
bump: `feat:`, `fix:`, `sec:`, and `chore:`/`docs:`/`refactor:`/`test:` (no
release). Write the subject as the changelog line a consumer would read.

## Conduct & security

By participating you agree to the org-wide
[Code of Conduct](https://github.com/cplieger/.github/blob/main/CODE_OF_CONDUCT.md).
Report security issues through the
[security policy](https://github.com/cplieger/.github/blob/main/SECURITY.md) —
never in a public issue.
