package collector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/zvdy/pgao/src/db"
	"github.com/zvdy/pgao/src/models"
	"golang.org/x/sync/errgroup"
)

// ClusterCollector collects cluster information and status
type ClusterCollector struct {
	pool         *db.ConnectionPool
	log          *logrus.Logger
	mu           sync.RWMutex
	clusters     map[string]*models.Cluster
	interval     time.Duration
	queryTimeout time.Duration
}

// NewClusterCollector creates a new ClusterCollector instance
func NewClusterCollector(pool *db.ConnectionPool, log *logrus.Logger, interval time.Duration) *ClusterCollector {
	return &ClusterCollector{
		pool:         pool,
		log:          log,
		clusters:     make(map[string]*models.Cluster),
		interval:     interval,
		queryTimeout: 10 * time.Second,
	}
}

// WithQueryTimeout caps each per-cluster info-collection round at d so a
// slow Postgres can't stall the rest of the fleet.
func (cc *ClusterCollector) WithQueryTimeout(d time.Duration) *ClusterCollector {
	if d > 0 {
		cc.queryTimeout = d
	}
	return cc
}

// Start begins collecting cluster information
func (cc *ClusterCollector) Start(ctx context.Context) {
	ticker := time.NewTicker(cc.interval)
	defer ticker.Stop()

	cc.log.Info("Cluster collector started")

	// Initial collection
	cc.collectAllClusters(ctx)

	for {
		select {
		case <-ctx.Done():
			cc.log.Info("Cluster collector stopped")
			return
		case <-ticker.C:
			cc.collectAllClusters(ctx)
		}
	}
}

// collectAllClusters fans out one goroutine per cluster so a slow Postgres
// in cluster A cannot delay info collection from clusters B and C. Each
// per-cluster context is bounded by cc.queryTimeout.
func (cc *ClusterCollector) collectAllClusters(ctx context.Context) {
	clusterIDs := cc.pool.GetAllClusters()
	if len(clusterIDs) == 0 {
		return
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(len(clusterIDs))
	for _, clusterID := range clusterIDs {
		clusterID := clusterID
		g.Go(func() error {
			cctx, cancel := context.WithTimeout(gctx, cc.queryTimeout)
			defer cancel()
			if err := cc.CollectClusterInfo(cctx, clusterID); err != nil {
				cc.log.WithError(err).WithField("cluster_id", clusterID).Error("collect cluster info failed")
			}
			return nil
		})
	}
	_ = g.Wait()
}

// CollectClusterInfo collects information about a specific cluster
func (cc *ClusterCollector) CollectClusterInfo(ctx context.Context, clusterID string) error {
	if _, err := cc.pool.GetPool(clusterID); err != nil {
		return err
	}

	cc.mu.Lock()
	cluster, exists := cc.clusters[clusterID]
	if !exists {
		cluster = models.NewCluster(clusterID, clusterID, "unknown", make(map[string]interface{}))
		cc.clusters[clusterID] = cluster
	}
	cc.mu.Unlock()

	// Check cluster health
	if err := cc.pool.HealthCheck(clusterID); err != nil {
		cluster.UpdateStatus("unhealthy")
		cc.log.Warnf("Cluster %s is unhealthy: %v", clusterID, err)
		return err
	}

	cluster.UpdateStatus("healthy")

	// Collect PostgreSQL version
	version, err := cc.collectVersion(ctx, clusterID)
	if err == nil {
		cluster.Configuration["version"] = version
	}

	// Collect server settings
	settings, err := cc.collectSettings(ctx, clusterID)
	if err == nil {
		cluster.Configuration["settings"] = settings
	}

	// Collect database list
	databases, err := cc.collectDatabases(ctx, clusterID)
	if err == nil {
		cluster.Configuration["databases"] = databases
	}

	// Collect replication status
	replStatus, err := cc.collectReplicationStatus(ctx, clusterID)
	if err == nil {
		cluster.Configuration["replication"] = replStatus
	}

	// Collect extension list
	extensions, err := cc.collectExtensions(ctx, clusterID)
	if err == nil {
		cluster.Configuration["extensions"] = extensions
	}

	cc.log.Debugf("Collected cluster info for %s", clusterID)
	return nil
}

// collectVersion retrieves the running PostgreSQL version string.
func (cc *ClusterCollector) collectVersion(ctx context.Context, clusterID string) (string, error) {
	pool, err := cc.pool.GetPool(clusterID)
	if err != nil {
		return "", err
	}

	var version string
	if err := pool.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		return "", fmt.Errorf("query version: %w", err)
	}
	return version, nil
}

// collectSettings retrieves a curated set of pg_settings entries, formatting
// each value with its unit when one is present.
func (cc *ClusterCollector) collectSettings(ctx context.Context, clusterID string) (map[string]string, error) {
	pool, err := cc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT name, setting, COALESCE(unit, '')
		FROM pg_settings
		WHERE name IN (
			'max_connections',
			'shared_buffers',
			'effective_cache_size',
			'maintenance_work_mem',
			'work_mem',
			'max_worker_processes',
			'max_parallel_workers',
			'wal_level',
			'max_wal_senders',
			'max_replication_slots'
		)
	`

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query settings: %w", err)
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var name, setting, unit string
		if err := rows.Scan(&name, &setting, &unit); err != nil {
			return nil, fmt.Errorf("scan settings: %w", err)
		}
		if unit == "" {
			settings[name] = setting
		} else {
			settings[name] = setting + unit
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate settings: %w", err)
	}
	return settings, nil
}

// collectDatabases lists every non-template database on the cluster.
func (cc *ClusterCollector) collectDatabases(ctx context.Context, clusterID string) ([]string, error) {
	pool, err := cc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT datname
		FROM pg_database
		WHERE datistemplate = false
		ORDER BY datname
	`

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query databases: %w", err)
	}
	defer rows.Close()

	databases := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan database: %w", err)
		}
		databases = append(databases, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate databases: %w", err)
	}
	return databases, nil
}

// collectReplicationStatus reports whether the node is a primary and lists
// every connected replica via pg_stat_replication.
func (cc *ClusterCollector) collectReplicationStatus(ctx context.Context, clusterID string) (map[string]interface{}, error) {
	pool, err := cc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	var inRecovery bool
	if err := pool.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&inRecovery); err != nil {
		return nil, fmt.Errorf("query recovery state: %w", err)
	}

	status := map[string]interface{}{
		"is_primary": !inRecovery,
		"replicas":   []map[string]interface{}{},
	}

	// pg_stat_replication is empty on standbys; the query is still safe.
	query := `
		SELECT
			COALESCE(application_name, ''),
			COALESCE(client_addr::text, ''),
			COALESCE(state, ''),
			COALESCE(sync_state, ''),
			COALESCE(sent_lsn::text, ''),
			COALESCE(replay_lsn::text, ''),
			COALESCE(EXTRACT(EPOCH FROM (NOW() - backend_start))::int, 0)
		FROM pg_stat_replication
	`

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query replication: %w", err)
	}
	defer rows.Close()

	replicas := make([]map[string]interface{}, 0)
	for rows.Next() {
		var appName, clientAddr, state, syncState, sentLSN, replayLSN string
		var uptime int
		if err := rows.Scan(&appName, &clientAddr, &state, &syncState, &sentLSN, &replayLSN, &uptime); err != nil {
			return nil, fmt.Errorf("scan replication: %w", err)
		}
		replicas = append(replicas, map[string]interface{}{
			"application_name": appName,
			"client_addr":      clientAddr,
			"state":            state,
			"sync_state":       syncState,
			"sent_lsn":         sentLSN,
			"replay_lsn":       replayLSN,
			"uptime_seconds":   uptime,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate replication: %w", err)
	}
	status["replicas"] = replicas
	return status, nil
}

// collectExtensions returns every installed extension on the cluster.
func (cc *ClusterCollector) collectExtensions(ctx context.Context, clusterID string) ([]string, error) {
	pool, err := cc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, "SELECT extname FROM pg_extension ORDER BY extname")
	if err != nil {
		return nil, fmt.Errorf("query extensions: %w", err)
	}
	defer rows.Close()

	extensions := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan extension: %w", err)
		}
		extensions = append(extensions, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate extensions: %w", err)
	}
	return extensions, nil
}

// GetCluster returns cluster information
func (cc *ClusterCollector) GetCluster(clusterID string) (*models.Cluster, error) {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	cluster, exists := cc.clusters[clusterID]
	if !exists {
		return nil, fmt.Errorf("cluster %s not found", clusterID)
	}
	return cluster, nil
}

// GetAllClusters returns all cluster information
func (cc *ClusterCollector) GetAllClusters() []*models.Cluster {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	clusters := make([]*models.Cluster, 0, len(cc.clusters))
	for _, cluster := range cc.clusters {
		clusters = append(clusters, cluster)
	}
	return clusters
}

// RegisterCluster registers a new cluster for monitoring
func (cc *ClusterCollector) RegisterCluster(cluster *models.Cluster) {
	cc.mu.Lock()
	cc.clusters[cluster.ID] = cluster
	cc.mu.Unlock()
	cc.log.Infof("Registered cluster %s for monitoring", cluster.ID)
}

// UnregisterCluster removes a cluster from monitoring
func (cc *ClusterCollector) UnregisterCluster(clusterID string) error {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	if _, exists := cc.clusters[clusterID]; !exists {
		return fmt.Errorf("cluster %s not found", clusterID)
	}

	delete(cc.clusters, clusterID)
	cc.log.Infof("Unregistered cluster %s from monitoring", clusterID)
	return nil
}
