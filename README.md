# schema-mirror

Machine-managed last-known-good copies of the official Sonarr and Radarr v3
OpenAPI documents, refreshed daily by the Schema mirror workflow
(.github/workflows/schema-mirror.yaml on main). The schema-drift tests fall
back to these when their live upstream HEAD fetch fails. One commit per
upstream contract change: the log of this branch is a changelog of upstream
API evolution, and each commit message records the exact upstream commits
it mirrors. Do not edit by hand.
