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
}

// NewMetricsCollector creates a new MetricsCollector instance
func NewMetricsCollector(pool *db.ConnectionPool, log *logrus.Logger, interval time.Duration) *MetricsCollector {
	return &MetricsCollector{
		pool:     pool,
		log:      log,
		interval: interval,
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
	if err := mc.collectTransactionMetrics(ctx, pool, metrics); err != nil {
		mc.log.Warnf("Failed to collect transaction metrics: %v", err)
	}

	// Collect lock metrics
	if err := mc.collectLockMetrics(ctx, pool, metrics); err != nil {
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
	if err := mc.collectDiskIOMetrics(ctx, pool, metrics); err != nil {
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

// collectTransactionMetrics collects transaction rate metrics
func (mc *MetricsCollector) collectTransactionMetrics(ctx context.Context, pool *pgxpool.Pool, metrics *models.Metrics) error {
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

	// Calculate TPS (simplified - real implementation would track delta over time)
	metrics.TransactionsPerSec = float64(totalTxn) / 60.0 // Rough estimate

	return nil
}

// collectLockMetrics collects lock-related metrics
func (mc *MetricsCollector) collectLockMetrics(ctx context.Context, pool *pgxpool.Pool, metrics *models.Metrics) error {
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

	var deadlocks int

	if err := pool.QueryRow(ctx, deadlocksQuery).Scan(&deadlocks); err == nil {
		metrics.DeadlockCount = deadlocks
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

// collectDiskIOMetrics collects disk I/O metrics
func (mc *MetricsCollector) collectDiskIOMetrics(ctx context.Context, pool *pgxpool.Pool, metrics *models.Metrics) error {
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

	// Convert blocks to KB (assuming 8KB blocks)
	metrics.DiskIORead = float64(blocksRead) * 8.0
	metrics.DiskIOWrite = float64(blocksWritten) * 8.0

	return nil
}

// CollectQueryMetrics collects query-level metrics
func (mc *MetricsCollector) CollectQueryMetrics(ctx context.Context, clusterID, database string) ([]*models.QueryMetrics, error) {
	pool, err := mc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	_ = pool

	query := `
		SELECT 
			queryid,
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

	_ = query

	// Placeholder - in real implementation, scan query results
	queryMetrics := make([]*models.QueryMetrics, 0)

	return queryMetrics, nil
}

// CollectTableMetrics collects table-level statistics
func (mc *MetricsCollector) CollectTableMetrics(ctx context.Context, clusterID, database string) ([]*models.TableMetrics, error) {
	pool, err := mc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	_ = pool

	query := `
		SELECT 
			schemaname,
			relname,
			seq_scan,
			seq_tup_read,
			idx_scan,
			idx_tup_fetch,
			n_tup_ins,
			n_tup_upd,
			n_tup_del,
			n_tup_hot_upd,
			n_live_tup,
			n_dead_tup,
			vacuum_count,
			autovacuum_count,
			analyze_count,
			last_vacuum,
			last_autovacuum,
			last_analyze
		FROM pg_stat_user_tables
		ORDER BY seq_scan + idx_scan DESC
		LIMIT 100
	`

	_ = query

	// Placeholder
	tableMetrics := make([]*models.TableMetrics, 0)

	return tableMetrics, nil
}

// GetMetricsSnapshot returns current metrics snapshot for a cluster
func (mc *MetricsCollector) GetMetricsSnapshot(ctx context.Context, clusterID string) (*models.Metrics, error) {
	metrics, err := mc.CollectClusterMetrics(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to collect metrics: %w", err)
	}

	return metrics, nil
}
