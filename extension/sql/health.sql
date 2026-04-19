-- pgao.health() returns one row per visible database with the expected
-- columns, and cache_hit_ratio stays within [0, 100].
CREATE EXTENSION pgao;

-- Row count: at least one row (the regress database itself).
SELECT count(*) > 0 AS has_rows FROM pgao.health();

-- cache_hit_ratio bounded.
SELECT bool_and(cache_hit_ratio BETWEEN 0 AND 100) AS ratio_bounded
FROM pgao.health();

-- Column names accessible (implicit shape check — fails if any column is
-- renamed/removed).
SELECT datname, cache_hit_ratio, xact_commit, xact_rollback,
       deadlocks, blks_read, blks_hit
FROM pgao.health() LIMIT 0;

-- cluster_info returns exactly one row.
SELECT count(*) FROM pgao.cluster_info();

-- Replication lag on a primary is 0.
SELECT pgao.replication_lag_ms();

DROP EXTENSION pgao;
