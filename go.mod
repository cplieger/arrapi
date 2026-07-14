module github.com/cplieger/arrapi

go 1.26.5

require github.com/cplieger/httpx/v2 v2.5.0

// v1.0.0 through v1.0.2 shipped a large-read context-cancellation bug and a
// cross-host API-key leak, both fixed in v1.1.0. Prefer v1.1.0 or later.
retract [v1.0.0, v1.0.2]
