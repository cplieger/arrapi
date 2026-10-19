# Contributing to arrapi

The [shared rules](https://github.com/cplieger/.github/blob/main/CONTRIBUTING.md) for commits, releases, synced files and checks apply here.

## Rules

- Add a new Sonarr endpoint to both engine files and call it from the public method through `detect.Session`, never directly. A direct call skips version detection and can decode a Sonarr 5 answer with the Sonarr 4 types.
- Read the Sonarr 4 (API v3) contract only from Sonarr's `v4-develop` branch. `develop`'s `src/Sonarr.Api.V3` is Sonarr 5's deprecated view, which renumbers series into their own order.
- Give each new top-level wire type a row in its package's `TestUpstreamSchemaDrift`, naming the schema it decodes. Without the row, an upstream field rename decodes silently to a zero value.
- Call each new public method in its package's `TestUpstreamRequestDrift`. A moved path or a renamed parameter then fails a test run instead of showing up at runtime.
- When an upstream OpenAPI document moves, change its contract in the package's `schemadrift_internal_test.go` and its `mirror` line in `.github/workflows/schema-mirror.yaml` together. `TestMirrorWorkflowMirrorsContract` fails while the two differ.
- New code that parses or sanitizes text from an arr instance gets a `testing.F` fuzz target beside its table tests.

## Checks

- The tests behind the `integration` build tag need a live Sonarr 4, Sonarr 5 and Radarr through `ARRAPI_SONARR_V4_URL`, `ARRAPI_SONARR_V5_URL` and `ARRAPI_RADARR_URL`, each with its `_KEY`. They send GET requests only, and CI does not run them.
- Re-record `testdata/` with `ARRAPI_RECORD=1 go test -tags integration -run TestRecord ./sonarr/ ./radarr/`. The recorder keeps only the fields arrapi decodes and replaces paths, names and IDs. It writes nothing when an answer carries a key, a URL or a hash.
