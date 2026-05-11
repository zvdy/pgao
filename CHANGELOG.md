# Changelog

All notable changes to pgao are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project tracks [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- High availability: Kubernetes lease-based leader election so
  `replicaCount > 1` doesn't double Postgres load. Disabled by default;
  enable via `server.leader_election.enabled` (chart:
  `leaderElection.enabled`). Followers serve `/ready` as 503 so the
  Service rotates them out. (#31)
- Graceful drain of supervisor + collectors on SIGTERM. New
  `server.shutdown_timeout` config field (default 30 s). (#30)
- TLS to Postgres: `verify-ca` / `verify-full` with operator-supplied
  root CA, optional mTLS client cert + key, and SNI override for
  LB-fronted topologies. Helm chart mounts cert material from k8s
  Secrets read-only at mode 0400. (#29)
- Embedded web UI Phase 1: React + TypeScript + TanStack Query SPA
  served at `/` via `//go:embed`. Pages: Overview (cluster list +
  status) and Cluster Detail (metrics / slow queries / tables). (#28)
- Helm chart with NetworkPolicy, PodDisruptionBudget,
  HorizontalPodAutoscaler, ServiceMonitor, Ingress, RBAC for leader
  election, and per-cluster password + TLS material sourcing from k8s
  Secrets. `validate-helm` CI job runs `helm lint` + `kubeconform`
  against rendered manifests. (#27, this PR)
- OpenAPI 3.1 spec at `api/openapi.yaml` covering every endpoint;
  redocly lint runs in CI. `docs/architecture.md` (sidecar vs
  extension), `docs/runbook.md` (symptom-first playbook),
  `docs/observability.md`, and an importable Grafana dashboard at
  `docs/grafana/pgao-overview.json`. (#26)
- Runtime instrumentation: `pgao_collection_errors_total`,
  `pgao_collection_duration_seconds`, `pgao_http_requests_total`,
  `pgao_http_request_duration_seconds`. Structured `cluster_id`
  logging via `logrus.WithField`. (#25)
- Release pipeline (`.github/workflows/release.yml`): multi-arch
  builds (`linux/amd64,linux/arm64`), GHCR push with semver + sha
  tags, keyless cosign signing via OIDC, CycloneDX SBOM, Trivy SARIF
  upload to the GitHub Security tab. `govulncheck` job in CI. (#24)
- Per-cluster connection state machine
  (`connecting → healthy ↔ unhealthy`) with jittered exponential
  backoff (5 s → 60 s). Collectors fan out per cluster via
  `errgroup` with a per-cluster query timeout so one slow Postgres
  can't stall the fleet. (#23)
- API hardening (`/api/v1/*` only): recoverer → logger → timeout →
  body limit → optional bearer / `X-API-Key` auth → rate limiter.
  Internal errors return a generic body; the underlying error stays
  in logs. (#22)
- Slow-query and table endpoints wired to `pg_stat_statements` and
  `pg_stat_user_tables` with `?limit`, `?order_by`, `?database`
  params. Missing extension surfaces as `412 Precondition Failed`
  with a `CREATE EXTENSION pg_stat_statements` hint. (#21)
- PostgreSQL extension scaffold under `extension/` with PGXS
  Makefile, five SQL functions, and `pg_regress` regression tests
  running in CI against PG 15 / 16 / 17. (#20)
- Prometheus exporter publishing `pgao_*` per-cluster gauges. (#19)
- Real collectors replacing placeholder data: actual `pg_catalog`
  queries for connections, cache hit, deadlocks, replication, bloat,
  disk I/O. TPS and disk I/O computed as deltas, not lifetime
  totals. (#17, #18)
- `gosec` SARIF upload to the GitHub Security tab + CodeQL workflow.
  (this PR)
- `SECURITY.md` (responsible-disclosure policy) and this
  `CHANGELOG.md`. (this PR)

### Changed

- Go toolchain `1.23 → 1.25.10` and `golangci-lint v1.64.8 → v2.5.0`
  to clear stdlib CVEs surfaced by `govulncheck`. (#24, #28, #31)
- `pgx v5.5.3 → v5.9.2` to clear `GO-2024-2606` (SQL injection in
  `pgproto3`). (#24)
- `golang.org/x/net → v0.53.0` to clear `GO-2026-4918` pulled in by
  `client-go`. (#31)
- Readiness probe (`/ready`) now actually pings every registered
  cluster instead of returning 200 unconditionally. (#16)
- Docker base images pinned to `golang:1.25-alpine3.22` +
  `alpine:3.22` (was `golang:1.23-alpine` + `alpine:latest`).
  Static-ish build with `-trimpath`; `postgresql-client` removed from
  the runtime image. (#24, #28)

### Fixed

- Readiness probe returning ok when no cluster was reachable. (#16)
- Lifetime-total counters being reported as instantaneous rates
  (TPS / disk I/O / deadlocks now deltas). (#17)

## How releases are cut

1. Update the `## [Unreleased]` heading above to `## [vX.Y.Z] —
   YYYY-MM-DD`. Move the section's contents under the new heading.
2. Create a new empty `## [Unreleased]` section at the top.
3. Tag the commit with `git tag vX.Y.Z && git push origin vX.Y.Z`.
4. The release workflow (`.github/workflows/release.yml`) builds the
   multi-arch image, signs it with cosign, generates SBOMs, and
   uploads Trivy SARIF.

The first release will be cut once leader election (#31) has soaked
in main long enough to be confident in the failover behaviour under
real load.
