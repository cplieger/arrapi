module github.com/cplieger/arrapi

go 1.26.5

require (
	github.com/cplieger/httpx/v3 v3.1.1
	github.com/cplieger/runesafe v1.1.1
)

// Test-only (schemadrift_test.go): OpenAPI-generated arr models used as the
// schema-drift oracle for the curated DTO subsets. Never a runtime dependency.
require (
	github.com/devopsarr/radarr-go v1.2.1
	github.com/devopsarr/sonarr-go v1.1.1
)

// v1.0.0 through v1.0.2 shipped a large-read context-cancellation bug and a
// cross-host API-key leak, both fixed in v1.1.0. Prefer v1.1.0 or later.
retract [v1.0.0, v1.0.2]
