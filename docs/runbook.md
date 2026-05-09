# pgao operator runbook

Symptom-first guide. Every section ends with a verification step, so you
know when you're done.

## A cluster shows `unhealthy` in `/ready` or `pgao_cluster_up=0`

The connection supervisor failed to ping the cluster. pgao keeps retrying
with jittered exponential backoff (5 s → 60 s); recovery happens
automatically once Postgres is reachable again.

**Check the supervisor's last error:**

```sh
curl -s "$PGAO/api/v1/clusters/<id>" | jq '.status, .configuration'
```

Pgao's logs include the structured field `cluster_id` so you can filter:

```sh
kubectl logs -l app=pgao | grep cluster_id=<id>
```

Common causes:

| `LastError` substring        | Likely cause                                             |
| ---------------------------- | -------------------------------------------------------- |
| `connection refused`         | Postgres not listening, or wrong port                    |
| `x509: certificate signed by unknown authority` | `ssl_root_cert` not set or wrong bundle |
| `x509: certificate is valid for ..., not <host>` | Cert SAN mismatch — set `ssl_server_name`           |
| `tls: failed to verify certificate`     | `ssl_mode: verify-full` against an unsigned/expired cert  |
| `dial tcp: lookup ... NX`    | DNS / Service name typo                                  |
| `password authentication`    | Wrong `password` or expired credential                   |
| `pq: SSL is not enabled`     | `ssl_mode: require` against a server with TLS off        |
| `context deadline exceeded`  | Network path is up but Postgres is too slow to ping      |

**Verify recovery:**

```sh
curl -s "$PGAO/ready" | jq
# expect: { "status":"ready", "healthy": >=1, ... }
curl -s "$PGAO/metrics" | grep "pgao_cluster_up{cluster=\"<id>\"} 1"
```

## `/api/v1/clusters/{id}/queries` returns `412 Precondition Failed`

`pg_stat_statements` is not installed on the target cluster. The error
message includes the fix verbatim:

```sh
psql -h <host> -U postgres -d postgres -c \
  "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;"
```

Then add the library to `shared_preload_libraries` and restart Postgres:

```sql
ALTER SYSTEM SET shared_preload_libraries = 'pg_stat_statements';
-- restart Postgres for shared_preload_libraries to take effect
```

**Verify:**

```sh
curl -s "$PGAO/api/v1/clusters/<id>/queries?limit=5" | jq 'length'
# expect: 0..5
```

## Metrics show gaps right after a pgao restart

Expected. Counter-derived rates (`transactions_per_sec`,
`disk_io_read/write`, `deadlock_count`) need two consecutive samples
before they can compute a delta. The first scrape after a restart shows
zero for those series.

**Verify:** wait one `metrics.collection_interval` (default 60 s) and
re-scrape:

```sh
sleep 60
curl -s "$PGAO/metrics" | grep pgao_transactions_per_sec
```

## `/metrics` is missing collection / HTTP histograms

You're hitting an old build. The runtime instrumentation
(`pgao_collection_*`, `pgao_http_*`) is registered unconditionally;
their absence means the binary predates that change. Re-roll the image:

```sh
kubectl set image deployment/pgao pgao=ghcr.io/zvdy/pgao:<latest>
kubectl rollout status deployment/pgao
```

**Verify:**

```sh
curl -s "$PGAO/metrics" | grep -E '^pgao_(collection|http)'
```

## High `pgao_collection_errors_total{kind="metrics"}` for one cluster

A specific sub-collection (transactions, locks, replication, bloat, …)
is failing repeatedly. The collector logs the failing `kind` as a
structured field:

```sh
kubectl logs -l app=pgao --tail=200 | grep '"sub-collection failed"'
```

Likely causes:

- `pg_stat_*` view permissions (the monitoring role is missing
  `pg_monitor` or `pg_read_all_stats` membership);
- a slow query inside one of the collector functions exceeding
  `metrics.query_timeout` (raise it in `config.yaml`).

**Verify:**

```sh
psql -h <host> -U <pgao_user> -c \
  "SELECT * FROM pg_stat_database LIMIT 1;"
# expect: rows, no permission errors
```

## `/api/v1/analyze` returns `413 Request Entity Too Large`

The body exceeded `server.max_body_bytes` (default 1 MiB). Bump it:

```yaml
server:
  max_body_bytes: 4194304   # 4 MiB
```

There is no API for streamed analysis — the limit exists to bound the
parser cache.

## Rate-limited (`429`) under load

`server.rate_limit_rps` (default 50 rps, burst 100) is process-wide and
intentional: pgao protects the underlying PostgreSQL fleet, not the
caller. Either raise the limit:

```yaml
server:
  rate_limit_rps: 200
  rate_limit_burst: 400
```

…or scale the deployment horizontally.

**Verify:**

```sh
curl -s -o /dev/null -w "%{http_code}\n" -X GET \
  -H "X-API-Key: $TOKEN" \
  "$PGAO/api/v1/clusters" | head
```

## Connecting to RDS / Aurora / managed Postgres with TLS

AWS-hosted Postgres requires `verify-full` against the [RDS root
bundle](https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem).
On a self-managed cluster issued by an internal CA you'll have your own
bundle; the procedure is the same.

**Step 1.** Pull the bundle (RDS):

```sh
curl -fsSL https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem \
  -o rds-ca.pem
```

**Step 2.** Create a k8s Secret holding the bundle. Add a client cert
+ key only if you're doing mTLS (Aurora IAM cert auth or operator
mTLS); for plain TLS `ca.crt` alone is enough.

```sh
kubectl -n pgao create secret generic pgao-prod-tls \
  --from-file=ca.crt=rds-ca.pem
# For mTLS:
#   --from-file=tls.crt=client.crt --from-file=tls.key=client.key
```

**Step 3.** Reference it from the Helm values:

```yaml
clusters:
  - id: prod
    host: prod.cluster-abc.us-east-1.rds.amazonaws.com
    user: pgao_monitor
    ssl_mode: verify-full
    existingPasswordSecret: pgao-prod-pw
    existingTLSSecret: pgao-prod-tls
    # When the LB hostname doesn't match the server cert SAN:
    # sslServerName: <writer-endpoint>
```

**Step 4.** Verify the supervisor reports healthy:

```sh
curl -s "$PGAO/api/v1/clusters/prod" | jq '.status'
# expect: "healthy"
```

If you see `x509: certificate signed by unknown authority`, the bundle
in the Secret is wrong (truncated, incorrect format). If you see
`x509: certificate is valid for <CN>, not <host>`, set
`sslServerName` to a hostname covered by the cert SAN.

**Rotation.** Pgao reads cert files once at startup. After replacing the
Secret contents, restart the Deployment:

```sh
kubectl -n pgao rollout restart deployment/pgao
```

## CI release didn't push to GHCR

The release workflow only runs on tags matching `v*.*.*`. Push the tag
explicitly:

```sh
git tag v0.2.0
git push origin v0.2.0
```

**Verify** by visiting the run output URL printed by GitHub Actions —
the summary block contains the resolved image digest and the
`cosign verify` command.

## I want to confirm the image I'm deploying is the one CI built

```sh
cosign verify ghcr.io/zvdy/pgao:<tag> \
  --certificate-identity-regexp 'https://github.com/zvdy/pgao/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

A non-zero exit means the image was not signed by the release workflow —
treat it as untrusted.
