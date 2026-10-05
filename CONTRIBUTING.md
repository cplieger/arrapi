# Contributing to arrapi

The [shared rules](https://github.com/cplieger/.github/blob/main/CONTRIBUTING.md) for commits, releases, synced files and checks apply here.

## Rules

- Put a new endpoint's method on `*Sonarr`, `*Radarr`, or the embedded `*client` when both serve it. A read uses `fetchAll` for a list, `fetchOne` for an object or `fetchPage` for a paged collection. List it in the README `## API` section.
- Give each new response type a row in `TestUpstreamSchemaDrift`, with both services' schemas when both return it. Without the row, an upstream field rename decodes silently to a zero value.
- Pin each new path and query parameter in `TestUpstreamEndpointDrift`. Without the pin, a moved path or a renamed parameter shows up only at runtime.
- When an upstream OpenAPI document moves, change its location in `specURLs` and in `.github/workflows/schema-mirror.yaml` together. Otherwise the tests stay green on the live document while the daily mirror refresh fails.
- New code that parses or sanitizes text from an arr instance gets a `testing.F` fuzz target beside its table tests. Plain record decoding stays with `encoding/json` and needs none.
