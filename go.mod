module github.com/cplieger/arrapi

go 1.26.5

require (
	github.com/cplieger/httpx/v3 v3.2.0
	github.com/cplieger/runesafe v1.2.1
)

// v1.0.0 through v1.0.2 shipped a large-read context-cancellation bug and a
// cross-host API-key leak, both fixed in v1.1.0. Prefer v1.1.0 or later.
retract [v1.0.0, v1.0.2]
