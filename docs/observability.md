# Observability for pgao

Pgao publishes three categories of Prometheus metrics on `/metrics`:

1. **Per-cluster Postgres gauges** scraped from `pg_stat_*` views.
   Source: `src/metrics/exporter.go`.
2. **Pgao runtime metrics** about its own behaviour — collection
   error/duration, HTTP request count/latency. Source:
   `src/metrics/instrumentation.go`.
3. **Go runtime + process metrics** from `prometheus/client_golang`'s
   stock collectors.

## Metric reference

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `pgao_cluster_up` | gauge | `cluster` | 1 when the supervisor reports the cluster healthy/degraded; 0 otherwise. Reflects state, not the presence of a sample. |
| `pgao_connections_active` | gauge | `cluster` | Active backend connections (pg_stat_activity). |
| `pgao_connections_total` | gauge | `cluster` | `max_connections` setting from the cluster. |
| `pgao_cache_hit_ratio` | gauge | `cluster` | Buffer cache hit ratio, 0–100. |
| `pgao_transactions_per_sec` | gauge | `cluster` | Commit + rollback rate over the collection window. |
| `pgao_replication_lag_ms` | gauge | `cluster` | Replay lag for standbys; 0 on primaries. |
| `pgao_table_bloat_pct` | gauge | `cluster` | Average dead-tuple percentage. |
| `pgao_deadlock_count` | gauge | `cluster` | Deadlocks since the last collection. |
| `pgao_lock_waits` | gauge | `cluster` | Backends currently waiting on a lock. |
| `pgao_disk_io_read_kbps` | gauge | `cluster` | Block reads attributed to Postgres backends. |
| `pgao_disk_io_write_kbps` | gauge | `cluster` | Block writes. |
| `pgao_collection_errors_total` | counter | `cluster, kind` | Failed collection rounds. `kind` is one of `metrics`, `cluster_info`. |
| `pgao_collection_duration_seconds` | histogram | `cluster, kind` | Per-cluster collection round latency. |
| `pgao_http_requests_total` | counter | `code, method, route` | Handled HTTP requests. `route` is the gorilla/mux template (e.g. `/api/v1/clusters/{id}/metrics`), so cluster IDs roll up cleanly. |
| `pgao_http_request_duration_seconds` | histogram | `route` | Handler latency. |

## PromQL recipes

### Cluster health

```promql
# Number of healthy clusters
sum(pgao_cluster_up == 1)

# Clusters that have been unhealthy for > 5 m
sum_over_time(pgao_cluster_up[5m]) == 0
```

### Collection pipeline

```promql
# Error rate by cluster + kind
sum by (cluster, kind) (
  rate(pgao_collection_errors_total[5m])
)

# p95 collection round duration
histogram_quantile(0.95,
  sum by (le, cluster, kind) (
    rate(pgao_collection_duration_seconds_bucket[5m])
  )
)
```

### HTTP API

```promql
# Requests per second by route + status code
sum by (route, code) (
  rate(pgao_http_requests_total[5m])
)

# 5xx error rate as fraction of all requests
sum by (route) (rate(pgao_http_requests_total{code=~"5.."}[5m]))
/ ignoring(code)
sum by (route) (rate(pgao_http_requests_total[5m]))

# p95 handler latency by route
histogram_quantile(0.95,
  sum by (le, route) (
    rate(pgao_http_request_duration_seconds_bucket[5m])
  )
)
```

### Postgres health

```promql
# Cache hit ratio under 99% on any cluster (alerting candidate)
min by (cluster) (pgao_cache_hit_ratio) < 99

# Replication lag over 5 s
max by (cluster) (pgao_replication_lag_ms) > 5000
```

## Suggested alert rules

```yaml
groups:
  - name: pgao
    interval: 30s
    rules:
      - alert: PgaoClusterDown
        expr: pgao_cluster_up == 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "pgao supervisor reports {{ $labels.cluster }} unhealthy"
          runbook: https://github.com/zvdy/pgao/blob/main/docs/runbook.md#a-cluster-shows-unhealthy-in-ready-or-pgao_cluster_up0

      - alert: PgaoCollectionErrors
        expr: |
          sum by (cluster, kind) (
            rate(pgao_collection_errors_total[5m])
          ) > 0.1
        for: 10m
        labels: { severity: warning }
        annotations:
          summary: "pgao collection failing for {{ $labels.cluster }} ({{ $labels.kind }})"
          runbook: https://github.com/zvdy/pgao/blob/main/docs/runbook.md#high-pgao_collection_errors_totalkindmetrics-for-one-cluster

      - alert: PgaoApiHighLatency
        expr: |
          histogram_quantile(0.95,
            sum by (le, route) (
              rate(pgao_http_request_duration_seconds_bucket[5m])
            )
          ) > 2
        for: 10m
        labels: { severity: warning }
        annotations:
          summary: "pgao route {{ $labels.route }} p95 > 2 s"
```

## Grafana dashboard

[`docs/grafana/pgao-overview.json`](grafana/pgao-overview.json) is an
importable Grafana dashboard with twelve panels covering supervisor
state, collection health, HTTP traffic, and per-cluster Postgres
metrics. Variables: `$DS_PROMETHEUS` (datasource), `$job` (multi),
`$cluster` (multi).

Import:

```
Grafana → Dashboards → Import
Paste docs/grafana/pgao-overview.json → choose your Prometheus datasource
```

Or via the API:

```sh
curl -sfL -H "Authorization: Bearer $GRAFANA_TOKEN" \
  -H "Content-Type: application/json" \
  -X POST "$GRAFANA_URL/api/dashboards/db" \
  --data-binary @- <<EOF
{
  "dashboard": $(cat docs/grafana/pgao-overview.json),
  "overwrite": true,
  "folderUid": ""
}
EOF
```

## Local end-to-end stack

[`scripts/observability/up.sh`](../scripts/observability/up.sh) spins up
a kind cluster with kube-prometheus-stack, deploys pgao via the Helm
chart with `serviceMonitor.enabled=true`, and port-forwards Grafana on
`http://127.0.0.1:3000` (admin/admin) so you can import the dashboard
and see real data from a kind-hosted Postgres. Tear down with
`scripts/observability/down.sh`.
