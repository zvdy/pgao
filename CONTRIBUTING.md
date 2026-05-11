# Contributing to pgao

Thanks for taking the time to contribute. This document covers how the
project is structured and the local checks every PR is expected to pass.

## Project layout

```
src/             Go code (cmd in src/main.go, packages by responsibility)
  api/           HTTP handlers + middleware chain
  collector/    Periodic metrics + cluster-info collection
  config/        YAML + env config loader
  db/            pgxpool wrapper + per-cluster supervisor + TLS
  leader/        Kubernetes lease-based leader election
  metrics/       Prometheus exporter + runtime instrumentation
  models/        Shared structs

web/             Embedded React + TypeScript SPA (Vite)
  src/           Source
  dist/          Build output (placeholder committed; CI builds the real one)
  embed.go       //go:embed wrapper served at /

charts/pgao/     Helm chart (deployment, service, RBAC, etc.)
extension/       PostgreSQL extension scaffold (PGXS, pg_regress)
docs/            Architecture, runbook, observability, Grafana JSON
api/openapi.yaml OpenAPI 3.1 contract

scripts/         kind / pgbench / observability helpers
.github/workflows/ CI: lint-go, test-go, vuln-go, sast-gosec,
                  codeql-go, validate-openapi, validate-helm,
                  build-ui, build, integration (kind), release
```

## Local prerequisites

- Go 1.25.10 (matches `go.mod`)
- Node 22 + npm (for the web UI)
- Helm 3.16+ + kubeconform (for chart validation)
- Docker (for the kind-based integration test)
- `golangci-lint v2.5.0` is auto-installed by `make lint`

## Required checks

Every PR must pass:

```sh
make fmt              # gofmt; CI fails if anything moves
make test             # go test -race -coverprofile
make lint             # golangci-lint v2 (gosec, errcheck, staticcheck, ...)
govulncheck ./...     # stdlib + dep CVEs
make ui               # vite build (when touching web/)
npm --prefix web test # vitest
helm lint charts/pgao -f charts/pgao/ci/sample-values.yaml
```

CI runs the same set plus CodeQL, gosec SARIF upload, kubeconform on the
rendered chart, and the kind-based e2e integration test.

## Conventions

- **Commit messages**: short subject (`type(scope): summary`) plus a body
  explaining the *why*. Examples in the existing log: `feat(api):`,
  `fix(ci):`, `docs:`, `chore:`, `ci:`.
- **Branches**: one feature per branch. Naming follows
  `claude/<short-slug>` for AI-assisted work, but anything readable is
  fine.
- **No PRs without context**: link the issue you're addressing (if any),
  list local-verification steps, and call out anything intentionally
  deferred. The merged PRs in this repo are a reference for the
  expected level of detail.
- **Don't auto-fix lint by suppressing**: every `//nolint:*` directive
  needs a comment explaining why the lint is wrong, not just that it
  fires.
- **Tests over assertions**: prefer tests that verify behaviour over
  comments that describe behaviour. Tests catch regressions; comments
  rot.

## When changes need extra care

- **Public HTTP API.** `api/openapi.yaml` is the contract. Bump it
  alongside any handler change; the `validate-openapi` job runs
  `redocly lint` on every PR.
- **Database queries.** Anything touching `pg_catalog` / `pg_stat_*`
  views needs an integration test that exercises it in the kind
  cluster. The CI integration test deploys a real Postgres with
  `pg_stat_statements` preloaded.
- **Helm chart.** Render with the sample values *and* an empty values
  set. `kubeconform -strict` runs against the result.
- **Toolchain bumps.** Run `govulncheck ./...` locally before pushing —
  a fresh CVE in the standard library can sit in main for hours before
  it shows up in CI's database refresh.

## Reporting a vulnerability

See [`SECURITY.md`](SECURITY.md). Please don't open a public issue for
security bugs.

## Release process

See the "How releases are cut" section of [`CHANGELOG.md`](CHANGELOG.md).
Tagging `v*.*.*` triggers the release workflow (multi-arch image,
cosign signing, SBOM, Trivy SARIF).
