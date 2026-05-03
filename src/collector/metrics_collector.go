package collector

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
	"github.com/zvdy/pgao/src/db"
	"github.com/zvdy/pgao/src/models"
	"golang.org/x/sync/errgroup"
)

// ErrPgStatStatementsMissing is returned by CollectQueryMetrics when the
// pg_stat_statements extension is not installed on the target cluster.
// Handlers surface this as HTTP 412 Precondition Failed so operators know
// they need to `CREATE EXTENSION pg_stat_statements`.
var ErrPgStatStatementsMissing = errors.New("pg_stat_statements extension not installed")

// CollectionObserver receives an observation per per-cluster collection
// round. Defined here so the collector package does not import the metrics
// package; *metrics.CollectionInstrumentation satisfies it.
type CollectionObserver interface {
	ObserveCollection(cluster, kind string, took time.Duration, err error)
}

// MetricsCollector gathers performance metrics from PostgreSQL clusters.
type MetricsCollector struct {
	pool         *db.ConnectionPool
	log          *logrus.Logger
	interval     time.Duration
	queryTimeout time.Duration
	rates        *rateCache
	now          func() time.Time
	observer     CollectionObserver

	mu     sync.RWMutex
	latest map[string]*models.Metrics
}

func NewMetricsCollector(pool *db.ConnectionPool, log *logrus.Logger, interval time.Duration) *MetricsCollector {
	return &MetricsCollector{
		pool:         pool,
		log:          log,
		interval:     interval,
		queryTimeout: 10 * time.Second,
		rates:        newRateCache(),
		now:          time.Now,
		latest:       make(map[string]*models.Metrics),
	}
}

// WithQueryTimeout caps each per-cluster collection round at d. A slow or
// hung Postgres won't stall the rest of the fleet — its own goroutine
// cancels at d.
func (mc *MetricsCollector) WithQueryTimeout(d time.Duration) *MetricsCollector {
	if d > 0 {
		mc.queryTimeout = d
	}
	return mc
}

// WithObserver wires a CollectionObserver. Each per-cluster collection
// round records its duration and a non-nil error if collection failed.
func (mc *MetricsCollector) WithObserver(o CollectionObserver) *MetricsCollector {
	mc.observer = o
	return mc
}

// LatestMetrics returns a snapshot of the most recent per-cluster metrics.
// Used by the Prometheus exporter so /metrics never blocks on Postgres.
func (mc *MetricsCollector) LatestMetrics() map[string]*models.Metrics {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	out := make(map[string]*models.Metrics, len(mc.latest))
	for k, v := range mc.latest {
		out[k] = v
	}
	return out
}

func (mc *MetricsCollector) cacheLatest(clusterID string, m *models.Metrics) {
	mc.mu.Lock()
	mc.latest[clusterID] = m
	mc.mu.Unlock()
}

// Start begins collecting metrics for all clusters
func (mc *MetricsCollector) Start(ctx context.Context) {
	ticker := time.NewTicker(mc.interval)
	defer ticker.Stop()

	mc.log.Info("Metrics collector started")

	for {
		select {
		case <-ctx.Done():
			mc.log.Info("Metrics collector stopped")
			return
		case <-ticker.C:
			mc.collectAllMetrics(ctx)
		}
	}
}

// collectAllMetrics fans out one goroutine per cluster so a slow Postgres in
// cluster A cannot delay collection from clusters B and C. Each per-cluster
// context is bounded by mc.queryTimeout.
func (mc *MetricsCollector) collectAllMetrics(ctx context.Context) {
	clusters := mc.pool.GetAllClusters()
	if len(clusters) == 0 {
		return
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(len(clusters))
	for _, clusterID := range clusters {
		clusterID := clusterID
		g.Go(func() error {
			cctx, cancel := context.WithTimeout(gctx, mc.queryTimeout)
			defer cancel()
			start := time.Now()
			_, err := mc.CollectClusterMetrics(cctx, clusterID)
			took := time.Since(start)
			if mc.observer != nil {
				mc.observer.ObserveCollection(clusterID, "metrics", took, err)
			}
			if err != nil {
				mc.log.WithError(err).WithFields(logrus.Fields{
					"cluster_id": clusterID,
					"took":       took,
				}).Error("collect metrics failed")
			}
			return nil // never propagate per-cluster errors; they are logged
		})
	}
	_ = g.Wait()
}

// CollectClusterMetrics collects metrics for a specific cluster and returns them
func (mc *MetricsCollector) CollectClusterMetrics(ctx context.Context, clusterID string) (*models.Metrics, error) {
	metrics := models.NewMetrics(clusterID)

	pool, err := mc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	logc := mc.log.WithField("cluster_id", clusterID)
	warn := func(kind string, err error) {
		if err != nil {
			logc.WithField("kind", kind).WithError(err).Warn("sub-collection failed")
		}
	}
	warn("connection", mc.collectConnectionMetrics(ctx, pool, metrics))
	warn("cache", mc.collectCacheMetrics(ctx, pool, metrics))
	warn("transaction", mc.collectTransactionMetrics(ctx, clusterID, pool, metrics))
	warn("lock", mc.collectLockMetrics(ctx, clusterID, pool, metrics))
	warn("replication", mc.collectReplicationMetrics(ctx, pool, metrics))
	warn("bloat", mc.collectBloatMetrics(ctx, pool, metrics))
	warn("disk_io", mc.collectDiskIOMetrics(ctx, clusterID, pool, metrics))

	mc.cacheLatest(clusterID, metrics)
	logc.Debug("collected metrics")
	return metrics, nil
}

// collectConnectionMetrics collects connection-related metrics
func (mc *MetricsCollector) collectConnectionMetrics(ctx context.Context, pool *pgxpool.Pool, metrics *models.Metrics) error {
	query := `
		SELECT 
			(SELECT COUNT(*) FROM pg_stat_activity WHERE state = 'active') as active,
			(SELECT setting::int FROM pg_settings WHERE name = 'max_connections') as max_conn
	`

	var active, maxConn int

	if err := pool.QueryRow(ctx, query).Scan(&active, &maxConn); err != nil {
		return err
	}

	metrics.ConnectionsActive = active
	metrics.ConnectionsTotal = maxConn

	return nil
}

// collectCacheMetrics collects cache hit ratio metrics
func (mc *MetricsCollector) collectCacheMetrics(ctx context.Context, pool *pgxpool.Pool, metrics *models.Metrics) error {
	query := `
		SELECT 
			COALESCE(sum(blks_hit) * 100.0 / NULLIF(sum(blks_hit) + sum(blks_read), 0), 0) as cache_hit_ratio
		FROM pg_stat_database
		WHERE datname = current_database()
	`

	var cacheHitRatio float64

	if err := pool.QueryRow(ctx, query).Scan(&cacheHitRatio); err != nil {
		return err
	}

	metrics.CacheHitRatio = cacheHitRatio

	return nil
}

// collectTransactionMetrics records the cumulative xact counter and converts
// it to a per-second rate via the per-cluster rateCache. The first sample for
// a cluster reports TPS=0 (insufficient history); subsequent samples report
// (delta_txn / elapsed_seconds).
func (mc *MetricsCollector) collectTransactionMetrics(ctx context.Context, clusterID string, pool *pgxpool.Pool, metrics *models.Metrics) error {
	query := `
		SELECT
			COALESCE(xact_commit + xact_rollback, 0) as total_txn
		FROM pg_stat_database
		WHERE datname = current_database()
	`

	var totalTxn int64

	if err := pool.QueryRow(ctx, query).Scan(&totalTxn); err != nil {
		return err
	}

	if rate, ok := mc.rates.observe(clusterID, "txn", totalTxn, mc.now()); ok {
		metrics.TransactionsPerSec = rate
	}

	return nil
}

// collectLockMetrics gathers lock waits (instantaneous gauge) and converts the
// cumulative deadlocks counter to deadlocks-per-second via the rate cache.
func (mc *MetricsCollector) collectLockMetrics(ctx context.Context, clusterID string, pool *pgxpool.Pool, metrics *models.Metrics) error {
	query := `
		SELECT
			COUNT(*) as lock_waits
		FROM pg_locks
		WHERE NOT granted
	`

	var lockWaits int

	if err := pool.QueryRow(ctx, query).Scan(&lockWaits); err != nil {
		return err
	}

	metrics.LockWaits = lockWaits

	deadlocksQuery := `
		SELECT
			COALESCE(deadlocks, 0) as deadlocks
		FROM pg_stat_database
		WHERE datname = current_database()
	`

	var deadlocks int64

	if err := pool.QueryRow(ctx, deadlocksQuery).Scan(&deadlocks); err == nil {
		// DeadlockCount semantics: deadlocks observed since the previous
		// sample, not lifetime cumulative. First sample reports 0.
		if delta, ok := mc.rates.observeDelta(clusterID, "deadlock", deadlocks, mc.now()); ok {
			metrics.DeadlockCount = int(delta)
		}
	}

	return nil
}

// collectReplicationMetrics collects replication lag metrics
func (mc *MetricsCollector) collectReplicationMetrics(ctx context.Context, pool *pgxpool.Pool, metrics *models.Metrics) error {
	// Check if this is a replica
	query := `
		SELECT 
			CASE 
				WHEN pg_is_in_recovery() THEN 
					COALESCE(EXTRACT(EPOCH FROM (NOW() - pg_last_xact_replay_timestamp())) * 1000, 0)
				ELSE 0 
			END as lag_ms
	`

	var lagMs int64

	if err := pool.QueryRow(ctx, query).Scan(&lagMs); err != nil {
		return err
	}

	metrics.ReplicationLag = lagMs

	return nil
}

// collectBloatMetrics collects table bloat metrics
func (mc *MetricsCollector) collectBloatMetrics(ctx context.Context, pool *pgxpool.Pool, metrics *models.Metrics) error {
	query := `
		SELECT 
			COALESCE(AVG(
				CASE WHEN n_live_tup > 0 
				THEN (n_dead_tup::float / n_live_tup::float) * 100 
				ELSE 0 END
			), 0) as bloat_pct
		FROM pg_stat_user_tables
	`

	var bloatPct float64

	if err := pool.QueryRow(ctx, query).Scan(&bloatPct); err != nil {
		return err
	}

	metrics.TableBloat = bloatPct

	return nil
}

// collectDiskIOMetrics converts cumulative block counters to KB/sec via the
// rate cache. blks_read and the tup_* counters are monotonic since the cluster
// (or the stats subsystem) was last reset.
func (mc *MetricsCollector) collectDiskIOMetrics(ctx context.Context, clusterID string, pool *pgxpool.Pool, metrics *models.Metrics) error {
	query := `
		SELECT
			COALESCE(sum(blks_read), 0) as blocks_read,
			COALESCE(sum(tup_inserted + tup_updated + tup_deleted), 0) as blocks_written
		FROM pg_stat_database
	`

	var blocksRead, blocksWritten int64

	if err := pool.QueryRow(ctx, query).Scan(&blocksRead, &blocksWritten); err != nil {
		return err
	}

	now := mc.now()
	if rate, ok := mc.rates.observe(clusterID, "blks_read", blocksRead, now); ok {
		metrics.DiskIORead = rate * 8.0 // 8 KiB blocks → KB/sec
	}
	if rate, ok := mc.rates.observe(clusterID, "blks_written", blocksWritten, now); ok {
		metrics.DiskIOWrite = rate * 8.0
	}

	return nil
}

// SlowQueryOptions controls the shape of CollectQueryMetrics results.
// Zero values fall back to sensible defaults (Limit=100, OrderBy=mean_exec_time).
type SlowQueryOptions struct {
	Limit    int
	OrderBy  string // "mean_exec_time" | "total_exec_time" | "calls"
	Database string // reserved for future per-database filtering
}

// allowedSlowQueryOrderBy maps the allowed OrderBy values to the
// pg_stat_statements column names used in the query. Restricting to a fixed
// set keeps the SQL safe from injection via query parameters.
var allowedSlowQueryOrderBy = map[string]string{
	"mean_exec_time":  "mean_exec_time",
	"total_exec_time": "total_exec_time",
	"calls":           "calls",
}

// CollectQueryMetrics returns the top queries by the chosen ordering from
// pg_stat_statements. Returns ErrPgStatStatementsMissing when the extension
// is not installed so callers can surface a precondition failure.
func (mc *MetricsCollector) CollectQueryMetrics(ctx context.Context, clusterID string, opts SlowQueryOptions) ([]*models.QueryMetrics, error) {
	pool, err := mc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	orderCol, ok := allowedSlowQueryOrderBy[opts.OrderBy]
	if !ok {
		orderCol = "mean_exec_time"
	}

	query := fmt.Sprintf(`
		SELECT
			queryid::text,
			query,
			calls,
			total_exec_time,
			mean_exec_time,
			stddev_exec_time,
			rows,
			shared_blks_hit,
			shared_blks_read,
			temp_blks_read,
			temp_blks_written
		FROM pg_stat_statements
		ORDER BY %s DESC
		LIMIT $1
	`, orderCol)

	rows, err := pool.Query(ctx, query, limit)
	if err != nil {
		if isUndefinedTable(err) {
			return nil, ErrPgStatStatementsMissing
		}
		return nil, fmt.Errorf("query pg_stat_statements: %w", err)
	}
	defer rows.Close()

	results := make([]*models.QueryMetrics, 0)
	for rows.Next() {
		qm := models.NewQueryMetrics("", "", clusterID, opts.Database)
		if err := rows.Scan(
			&qm.QueryID,
			&qm.Query,
			&qm.CallCount,
			&qm.ExecutionTime,
			&qm.MeanExecTime,
			&qm.StddevExecTime,
			&qm.RowsReturned,
			&qm.SharedBlocksHit,
			&qm.SharedBlocksRead,
			&qm.TempBlocksRead,
			&qm.TempBlocksWritten,
		); err != nil {
			return nil, fmt.Errorf("scan query row: %w", err)
		}
		results = append(results, qm)
	}
	if err := rows.Err(); err != nil {
		if isUndefinedTable(err) {
			return nil, ErrPgStatStatementsMissing
		}
		return nil, fmt.Errorf("iterate query rows: %w", err)
	}
	return results, nil
}

// TableMetricsOptions controls CollectTableMetrics. Zero Limit falls back to
// 100 and is capped at 500.
type TableMetricsOptions struct {
	Limit    int
	Database string // reserved for future per-database filtering
}

// CollectTableMetrics returns the top user tables by total scan activity
// from pg_stat_user_tables.
func (mc *MetricsCollector) CollectTableMetrics(ctx context.Context, clusterID string, opts TableMetricsOptions) ([]*models.TableMetrics, error) {
	pool, err := mc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	query := `
		SELECT
			schemaname,
			relname,
			COALESCE(seq_scan, 0),
			COALESCE(seq_tup_read, 0),
			COALESCE(idx_scan, 0),
			COALESCE(idx_tup_fetch, 0),
			COALESCE(n_tup_ins, 0),
			COALESCE(n_tup_upd, 0),
			COALESCE(n_tup_del, 0),
			COALESCE(n_tup_hot_upd, 0),
			COALESCE(n_live_tup, 0),
			COALESCE(n_dead_tup, 0),
			COALESCE(vacuum_count, 0),
			COALESCE(autovacuum_count, 0),
			COALESCE(analyze_count, 0),
			last_vacuum,
			last_autovacuum,
			last_analyze
		FROM pg_stat_user_tables
		ORDER BY COALESCE(seq_scan, 0) + COALESCE(idx_scan, 0) DESC
		LIMIT $1
	`

	rows, err := pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query pg_stat_user_tables: %w", err)
	}
	defer rows.Close()

	results := make([]*models.TableMetrics, 0)
	for rows.Next() {
		tm := models.NewTableMetrics(clusterID, opts.Database, "", "")
		if err := rows.Scan(
			&tm.Schema,
			&tm.Table,
			&tm.SeqScan,
			&tm.SeqTupRead,
			&tm.IdxScan,
			&tm.IdxTupFetch,
			&tm.TupInserted,
			&tm.TupUpdated,
			&tm.TupDeleted,
			&tm.TupHotUpdated,
			&tm.LiveTuples,
			&tm.DeadTuples,
			&tm.VacuumCount,
			&tm.AutovacuumCount,
			&tm.AnalyzeCount,
			&tm.LastVacuum,
			&tm.LastAutovacuum,
			&tm.LastAnalyze,
		); err != nil {
			return nil, fmt.Errorf("scan table row: %w", err)
		}
		results = append(results, tm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate table rows: %w", err)
	}
	return results, nil
}

// GetMetricsSnapshot returns current metrics snapshot for a cluster
func (mc *MetricsCollector) GetMetricsSnapshot(ctx context.Context, clusterID string) (*models.Metrics, error) {
	metrics, err := mc.CollectClusterMetrics(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to collect metrics: %w", err)
	}

	return metrics, nil
}

// isUndefinedTable reports whether err is a Postgres "relation does not exist"
// (SQLSTATE 42P01). Used to translate a missing pg_stat_statements view into
// the ErrPgStatStatementsMissing sentinel.
func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P01"
	}
	return false
}
