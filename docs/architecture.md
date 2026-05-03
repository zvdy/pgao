# pgao architecture

## Purpose

`pgao` watches a fleet of PostgreSQL clusters and surfaces:

- per-cluster metrics (cache hit, TPS, deadlocks, replication lag, disk I/O,
  table bloat) at a Prometheus-compatible `/metrics` endpoint and via JSON;
- slow-query and table statistics (driven by `pg_stat_statements` and
  `pg_stat_user_tables`);
- standalone SQL analysis using `libpg_query` (no live cluster needed).

Two deployment modes are in scope: a Go **sidecar** (the default, what this
repository builds) and a **PostgreSQL extension** (scaffolded under
`extension/`, see [#14](https://github.com/zvdy/pgao/issues/14)).

## Process model (sidecar)

```
                    ┌──────────────────────────────────────────────────┐
                    │                 pgao process                      │
                    │                                                   │
   pg_stat_*        │   ┌───────────┐   ┌──────────────┐                │
   pg_settings      │   │ collector │──▶│ metrics      │── /metrics ──▶ │ Prometheus
   pg_replication ──┼──▶│ goroutines│   │ exporter     │                │
   pg_extension     │   └─────┬─────┘   └──────┬───────┘                │
                    │         │                │                        │
                    │   ┌─────▼─────┐   ┌──────▼───────┐                │
                    │   │ in-memory │◀──│ HTTP handler │── /api/v1/* ──▶ │ Operators
                    │   │ snapshot  │   │ (gorilla/mux)│                │ + Web UI (#13)
                    │   └─────┬─────┘   └──────┬───────┘                │
                    │         │                │                        │
                    │   ┌─────▼─────┐   ┌──────▼───────┐                │
                    │   │ pgxpool   │   │ middleware   │                │
                    │   │ + state   │   │ chain        │                │
                    │   │ machine   │   │ (auth, rate, │                │
                    │   └─────┬─────┘   │  recover)    │                │
                    │         │         └──────────────┘                │
                    └─────────┼──────────────────────────────────────────┘
                              │
                              ▼
                    PostgreSQL cluster(s)
```

### Components

| Package              | Responsibility                                              |
| -------------------- | ----------------------------------------------------------- |
| `src/main.go`        | Wires config, pool, collectors, supervisor, HTTP server     |
| `src/config`         | YAML + env-var config; precedence env > YAML > defaults     |
| `src/db`             | `pgxpool` per cluster + per-cluster state machine           |
| `src/collector`      | Periodic collection of metrics + cluster info               |
| `src/api`            | Handlers, middleware chain (auth/timeout/body/rate/recover) |
| `src/analyzer`       | SQL parsing (libpg_query) + thresholds                      |
| `src/metrics`        | Prometheus exporter + runtime instrumentation               |
| `src/models`         | Shared structs (Metrics, QueryMetrics, Alert, etc.)         |

### Cluster state machine (issue #11)

`db.ConnectionPool` registers every configured cluster regardless of initial
reachability. A background `Supervise(ctx)` goroutine probes each cluster
and tracks one of `connecting | healthy | unhealthy | degraded`. Probes use
jittered exponential backoff (5 s → 60 s) when a cluster is unhealthy and
the configured `metrics.health_check_interval` (default 30 s) when healthy.

The Prometheus `pgao_cluster_up` gauge reflects this state, *not* the
presence of a metrics sample — a cluster that disappeared still shows up in
the time series at `0`.

### HTTP middleware chain (issue #6)

Applied to `/api/v1/*` only; `/health`, `/ready`, `/metrics` see only the
recoverer + request logger so probes and Prometheus scrapes keep working
unauthenticated.

```
recoverer → requestLogger → httpInstrumentation → rateLimit → timeout → maxBody → apiKeyAuth → handler
```

## Data flow

1. **Config load.** `config.LoadConfig` reads `CONFIG_PATH` (default
   `config.yaml`), expands `${VAR}` placeholders, and overrides from env.
2. **Cluster registration.** `pool.AddCluster` builds a `pgxpool.Pool`
   without pinging (pgx connects lazily) and seeds the state machine.
3. **Supervisor.** `pool.Supervise` probes every registered cluster;
   transitions update `pgao_cluster_up` and `LastError`.
4. **Collectors.** Two background goroutines (`MetricsCollector`,
   `ClusterCollector`) tick on `metrics.collection_interval` and fan out
   per-cluster goroutines via `errgroup`. Each per-cluster call gets its own
   `metrics.query_timeout` deadline.
5. **HTTP API.** Handlers read from cached samples (so requests never
   block on Postgres) and from on-demand collector calls (e.g. `/queries`,
   `/tables`).
6. **Prometheus.** `/metrics` serves the exporter (per-cluster gauges driven
   by the supervisor state) plus runtime instrumentation
   (`pgao_collection_*`, `pgao_http_*`).

## Sidecar vs extension

| Concern                         | Sidecar (this repo)                | Extension (`extension/`, #14)              |
| ------------------------------- | ---------------------------------- | ------------------------------------------ |
| **Deployment**                  | Separate Pod / process             | `CREATE EXTENSION pgao` inside Postgres    |
| **Aurora / RDS / Cloud SQL**    | Works (any reachable Postgres)     | Limited to provider extension allowlists   |
| **Bare-metal / self-managed**   | Works                              | Works                                      |
| **Auth surface**                | HTTP middleware (#6)               | Postgres role / function ACLs              |
| **Multi-cluster fleet**         | Native — one pgao watches many     | One install per cluster                    |
| **Prometheus integration**      | Direct `/metrics` endpoint         | Needs `pgao_exporter` sidecar              |
| **Compute footprint**           | Small Go process (≈ 30 MiB)        | Inside the Postgres backend                |
| **Failure isolation**           | Crash doesn't affect Postgres      | Bug in extension can take down a backend   |

The intent is to ship both. The sidecar covers managed Postgres and large
fleets; the extension covers operators who want metrics co-located with the
database and don't run a sidecar fleet.

## Operational reference

- Endpoint contract: [`api/openapi.yaml`](../api/openapi.yaml)
- Prometheus metrics surface: see Exporter + instrumentation in
  `src/metrics/exporter.go` and `src/metrics/instrumentation.go`
- Operator playbooks: [`docs/runbook.md`](runbook.md)
