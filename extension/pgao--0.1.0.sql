-- complain if script is sourced in psql, rather than via CREATE EXTENSION
\echo Use "CREATE EXTENSION pgao" to load this file. \quit

CREATE FUNCTION pgao.version()
RETURNS text
LANGUAGE sql
IMMUTABLE
AS $$ SELECT '0.1.0'::text $$;

CREATE FUNCTION pgao.cluster_info(
    OUT version            text,
    OUT is_in_recovery     boolean,
    OUT current_database   text,
    OUT server_start       timestamptz,
    OUT active_connections integer,
    OUT max_connections    integer
)
LANGUAGE sql
STABLE
AS $$
    SELECT
        version(),
        pg_is_in_recovery(),
        current_database(),
        pg_postmaster_start_time(),
        (SELECT count(*)::int FROM pg_stat_activity WHERE state = 'active'),
        (SELECT setting::int FROM pg_settings WHERE name = 'max_connections')
$$;

CREATE FUNCTION pgao.health()
RETURNS TABLE (
    datname         name,
    cache_hit_ratio double precision,
    xact_commit     bigint,
    xact_rollback   bigint,
    deadlocks       bigint,
    blks_read       bigint,
    blks_hit        bigint
)
LANGUAGE sql
STABLE
AS $$
    SELECT
        datname,
        COALESCE(blks_hit * 100.0 / NULLIF(blks_hit + blks_read, 0), 0)::double precision,
        xact_commit,
        xact_rollback,
        deadlocks,
        blks_read,
        blks_hit
    FROM pg_stat_database
    WHERE datname IS NOT NULL
$$;

CREATE FUNCTION pgao.table_bloat()
RETURNS TABLE (
    schemaname name,
    relname    name,
    live_tuples bigint,
    dead_tuples bigint,
    bloat_pct   double precision
)
LANGUAGE sql
STABLE
AS $$
    SELECT
        schemaname,
        relname,
        n_live_tup,
        n_dead_tup,
        CASE WHEN n_live_tup > 0
             THEN (n_dead_tup::double precision / n_live_tup::double precision) * 100
             ELSE 0
        END
    FROM pg_stat_user_tables
$$;

CREATE FUNCTION pgao.replication_lag_ms()
RETURNS bigint
LANGUAGE sql
STABLE
AS $$
    SELECT CASE
        WHEN pg_is_in_recovery() THEN
            COALESCE(
                (EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp())) * 1000)::bigint,
                0
            )
        ELSE 0
    END
$$;
