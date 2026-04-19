# pgao PostgreSQL extension

In-database collectors for pgao. Exposes the same observability data the Go
sidecar reads from `pg_catalog` via SQL functions under the `pgao` schema, so
pgao can be deployed as an extension on hosts where running a separate process
is inconvenient (RDS/Aurora/Cloud SQL with the relevant allow-lists,
self-managed nodes behind firewalls, or Kubernetes Operators that prefer to
bundle observability with the database pod).

## Functions

| Function | Returns | Source |
|---|---|---|
| `pgao.version()` | `text` | extension version |
| `pgao.cluster_info()` | row | `version()`, `pg_is_in_recovery()`, `pg_postmaster_start_time()`, connection counts |
| `pgao.health()` | set | `pg_stat_database` per-DB cache hit ratio + txn/deadlock counters |
| `pgao.table_bloat()` | set | `pg_stat_user_tables` live/dead tuples + computed bloat percentage |
| `pgao.replication_lag_ms()` | `bigint` | `pg_last_xact_replay_timestamp()` delta; 0 on a primary |

All functions are SQL-only, `STABLE`/`IMMUTABLE`, and require no superuser.

## Build & install

```
cd extension
make
sudo make install
```

`make install` drops `pgao.control` and `pgao--0.1.0.sql` into
`$(pg_config --sharedir)/extension/`. Once installed:

```
CREATE EXTENSION pgao;
SELECT * FROM pgao.health();
```

## Tests

Regression tests use `pg_regress` with a temp instance — no running Postgres
required, but must run as a non-root user (initdb refuses to run as root):

```
cd extension
sudo -u postgres make installcheck
```

Add new tests by dropping `sql/<name>.sql` + `expected/<name>.out` and
appending `<name>` to the `REGRESS` list in `Makefile`.

## Relationship to the Go sidecar

This extension is deliberately narrow — it surfaces data, it doesn't collect
or alert. The Go service continues to:

- compute rates from counters (the `rateCache` logic)
- expose `/metrics` in Prometheus format
- drive the REST API

An operator can choose either deployment (or both): the extension for
in-database SQL access, the sidecar for cross-cluster aggregation and the
public API.
