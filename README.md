# schema-mirror

Machine-managed last-known-good copies of the OpenAPI documents arrapi's
drift tests read: Sonarr v4 (API v3) from Sonarr v4-develop, Sonarr v5
(API v5) from Sonarr develop, and Radarr (API v3) from Radarr develop.
The Schema mirror workflow (.github/workflows/schema-mirror.yaml on main)
refreshes them daily, and the drift tests fall back to them when their
live upstream fetch fails. One commit per upstream contract change: the
log of this branch is a changelog of upstream API evolution, and each
commit message records the exact upstream commits it mirrors. The files
sonarr-openapi.json and radarr-openapi.json, if present, are no longer
read or refreshed. Do not edit by hand.
