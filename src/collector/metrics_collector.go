package collector

import (
	"context"
	"fmt"
	"time"

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
		if err := mc.CollectClusterMetrics(ctx, clusterID); err != nil {
			mc.log.Errorf("Failed to collect metrics for cluster %s: %v", clusterID, err)
		}
	}
}

// CollectClusterMetrics collects metrics for a specific cluster
func (mc *MetricsCollector) CollectClusterMetrics(ctx context.Context, clusterID string) error {
	metrics := models.NewMetrics(clusterID)

	pool, err := mc.pool.GetPool(clusterID)
	if err != nil {
		return err
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
	return nil
}

// collectConnectionMetrics collects connection-related metrics
func (mc *MetricsCollector) collectConnectionMetrics(ctx context.Context, pool interface{}, metrics *models.Metrics) error {
	query := `
		SELECT 
			(SELECT COUNT(*) FROM pg_stat_activity) as active,
			(SELECT setting::int FROM pg_settings WHERE name = 'max_connections') as max_conn
	`

	var active, maxConn int
	// Note: In real implementation, use pool.QueryRow to scan these values
	_ = query

	// Placeholder values for demonstration
	active = 25
	maxConn = 100

	metrics.ConnectionsActive = active
	metrics.ConnectionsTotal = maxConn

	return nil
}

// collectCacheMetrics collects cache hit ratio metrics
func (mc *MetricsCollector) collectCacheMetrics(ctx context.Context, pool interface{}, metrics *models.Metrics) error {
	query := `
		SELECT 
			sum(blks_hit) * 100.0 / NULLIF(sum(blks_hit) + sum(blks_read), 0) as cache_hit_ratio
		FROM pg_stat_database
		WHERE datname = current_database()
	`

	_ = query
	// Placeholder
	metrics.CacheHitRatio = 98.5

	return nil
}

// collectTransactionMetrics collects transaction rate metrics
func (mc *MetricsCollector) collectTransactionMetrics(ctx context.Context, pool interface{}, metrics *models.Metrics) error {
	query := `
		SELECT 
			xact_commit + xact_rollback as total_txn
		FROM pg_stat_database
		WHERE datname = current_database()
	`

	_ = query
	// Placeholder
	metrics.TransactionsPerSec = 150.5

	return nil
}

// collectLockMetrics collects lock-related metrics
func (mc *MetricsCollector) collectLockMetrics(ctx context.Context, pool interface{}, metrics *models.Metrics) error {
	query := `
		SELECT 
			COUNT(*) as lock_waits
		FROM pg_locks
		WHERE NOT granted
	`

	_ = query
	// Placeholder
	metrics.LockWaits = 5

	deadlocksQuery := `
		SELECT 
			COALESCE(deadlocks, 0) as deadlocks
		FROM pg_stat_database
		WHERE datname = current_database()
	`

	_ = deadlocksQuery
	metrics.DeadlockCount = 0

	return nil
}

// collectReplicationMetrics collects replication lag metrics
func (mc *MetricsCollector) collectReplicationMetrics(ctx context.Context, pool interface{}, metrics *models.Metrics) error {
	query := `
		SELECT 
			EXTRACT(EPOCH FROM (NOW() - pg_last_xact_replay_timestamp())) * 1000 as lag_ms
	`

	_ = query
	// Placeholder
	metrics.ReplicationLag = 50

	return nil
}

// collectBloatMetrics collects table bloat metrics
func (mc *MetricsCollector) collectBloatMetrics(ctx context.Context, pool interface{}, metrics *models.Metrics) error {
	query := `
		SELECT 
			COALESCE(AVG(
				(pg_stat_get_live_tuples(c.oid) + pg_stat_get_dead_tuples(c.oid))::float / 
				NULLIF(pg_stat_get_live_tuples(c.oid), 0) * 100
			), 0) as bloat_pct
		FROM pg_class c
		WHERE c.relkind = 'r'
	`

	_ = query
	// Placeholder
	metrics.TableBloat = 5.2

	return nil
}

// collectDiskIOMetrics collects disk I/O metrics
func (mc *MetricsCollector) collectDiskIOMetrics(ctx context.Context, pool interface{}, metrics *models.Metrics) error {
	query := `
		SELECT 
			sum(blks_read) as blocks_read,
			sum(blks_hit) as blocks_hit
		FROM pg_stat_database
	`

	_ = query
	// Placeholder
	metrics.DiskIORead = 1024.5
	metrics.DiskIOWrite = 512.3

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
	metrics := models.NewMetrics(clusterID)

	if err := mc.CollectClusterMetrics(ctx, clusterID); err != nil {
		return nil, fmt.Errorf("failed to collect metrics: %w", err)
	}

	return metrics, nil
}
