package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
	"github.com/zvdy/pgao/src/db"
	"github.com/zvdy/pgao/src/models"
)

// MetricsCollector gathers performance metrics from PostgreSQL clusters
type MetricsCollector struct {
	pool     *db.ConnectionPool
	log      *logrus.Logger
	interval time.Duration
	rates    *rateCache
	now      func() time.Time
}

// NewMetricsCollector creates a new MetricsCollector instance
func NewMetricsCollector(pool *db.ConnectionPool, log *logrus.Logger, interval time.Duration) *MetricsCollector {
	return &MetricsCollector{
		pool:     pool,
		log:      log,
		interval: interval,
		rates:    newRateCache(),
		now:      time.Now,
	}
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

// collectAllMetrics collects metrics for all registered clusters
func (mc *MetricsCollector) collectAllMetrics(ctx context.Context) {
	clusters := mc.pool.GetAllClusters()

	for _, clusterID := range clusters {
		if _, err := mc.CollectClusterMetrics(ctx, clusterID); err != nil {
			mc.log.Errorf("Failed to collect metrics for cluster %s: %v", clusterID, err)
		}
	}
}

// CollectClusterMetrics collects metrics for a specific cluster and returns them
func (mc *MetricsCollector) CollectClusterMetrics(ctx context.Context, clusterID string) (*models.Metrics, error) {
	metrics := models.NewMetrics(clusterID)

	pool, err := mc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	// Collect connection metrics
	if err := mc.collectConnectionMetrics(ctx, pool, metrics); err != nil {
		mc.log.Warnf("Failed to collect connection metrics: %v", err)
	}

	// Collect cache metrics
	if err := mc.collectCacheMetrics(ctx, pool, metrics); err != nil {
		mc.log.Warnf("Failed to collect cache metrics: %v", err)
	}

	// Collect transaction metrics
	if err := mc.collectTransactionMetrics(ctx, clusterID, pool, metrics); err != nil {
		mc.log.Warnf("Failed to collect transaction metrics: %v", err)
	}

	// Collect lock metrics
	if err := mc.collectLockMetrics(ctx, clusterID, pool, metrics); err != nil {
		mc.log.Warnf("Failed to collect lock metrics: %v", err)
	}

	// Collect replication metrics
	if err := mc.collectReplicationMetrics(ctx, pool, metrics); err != nil {
		mc.log.Warnf("Failed to collect replication metrics: %v", err)
	}

	// Collect table bloat metrics
	if err := mc.collectBloatMetrics(ctx, pool, metrics); err != nil {
		mc.log.Warnf("Failed to collect bloat metrics: %v", err)
	}

	// Collect disk I/O metrics
	if err := mc.collectDiskIOMetrics(ctx, clusterID, pool, metrics); err != nil {
		mc.log.Warnf("Failed to collect disk I/O metrics: %v", err)
	}

	mc.log.Debugf("Collected metrics for cluster %s", clusterID)
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

// CollectQueryMetrics returns the top 100 queries by mean execution time from
// pg_stat_statements. The extension must be installed; if it isn't, callers
// will see a `relation "pg_stat_statements" does not exist` error from pgx.
// Issue #7 will add the precondition check + per-request limit/order knobs.
func (mc *MetricsCollector) CollectQueryMetrics(ctx context.Context, clusterID, database string) ([]*models.QueryMetrics, error) {
	pool, err := mc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	query := `
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
		ORDER BY mean_exec_time DESC
		LIMIT 100
	`

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query pg_stat_statements: %w", err)
	}
	defer rows.Close()

	results := make([]*models.QueryMetrics, 0)
	for rows.Next() {
		qm := models.NewQueryMetrics("", "", clusterID, database)
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
		return nil, fmt.Errorf("iterate query rows: %w", err)
	}
	return results, nil
}

// CollectTableMetrics returns the top 100 user tables by total scan activity
// from pg_stat_user_tables.
func (mc *MetricsCollector) CollectTableMetrics(ctx context.Context, clusterID, database string) ([]*models.TableMetrics, error) {
	pool, err := mc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
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
		LIMIT 100
	`

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query pg_stat_user_tables: %w", err)
	}
	defer rows.Close()

	results := make([]*models.TableMetrics, 0)
	for rows.Next() {
		tm := models.NewTableMetrics(clusterID, database, "", "")
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
