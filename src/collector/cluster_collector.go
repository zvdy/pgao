package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/zvdy/pgao/src/db"
	"github.com/zvdy/pgao/src/models"
)

// ClusterCollector collects cluster information and status
type ClusterCollector struct {
	pool     *db.ConnectionPool
	log      *logrus.Logger
	clusters map[string]*models.Cluster
	interval time.Duration
}

// NewClusterCollector creates a new ClusterCollector instance
func NewClusterCollector(pool *db.ConnectionPool, log *logrus.Logger, interval time.Duration) *ClusterCollector {
	return &ClusterCollector{
		pool:     pool,
		log:      log,
		clusters: make(map[string]*models.Cluster),
		interval: interval,
	}
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

// collectAllClusters collects information for all registered clusters
func (cc *ClusterCollector) collectAllClusters(ctx context.Context) {
	clusterIDs := cc.pool.GetAllClusters()

	for _, clusterID := range clusterIDs {
		if err := cc.CollectClusterInfo(ctx, clusterID); err != nil {
			cc.log.Errorf("Failed to collect info for cluster %s: %v", clusterID, err)
		}
	}
}

// CollectClusterInfo collects information about a specific cluster
func (cc *ClusterCollector) CollectClusterInfo(ctx context.Context, clusterID string) error {
	pool, err := cc.pool.GetPool(clusterID)
	if err != nil {
		return err
	}

	_ = pool

	// Create or update cluster information
	cluster, exists := cc.clusters[clusterID]
	if !exists {
		cluster = models.NewCluster(clusterID, clusterID, "unknown", make(map[string]interface{}))
		cc.clusters[clusterID] = cluster
	}

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

// collectVersion retrieves PostgreSQL version
func (cc *ClusterCollector) collectVersion(ctx context.Context, clusterID string) (string, error) {
	pool, err := cc.pool.GetPool(clusterID)
	if err != nil {
		return "", err
	}

	_ = pool

	query := "SELECT version()"
	_ = query

	// Placeholder
	return "PostgreSQL 15.3", nil
}

// collectSettings retrieves important PostgreSQL settings
func (cc *ClusterCollector) collectSettings(ctx context.Context, clusterID string) (map[string]string, error) {
	pool, err := cc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	_ = pool

	query := `
		SELECT name, setting, unit
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

	_ = query

	// Placeholder
	settings := map[string]string{
		"max_connections":      "100",
		"shared_buffers":       "128MB",
		"effective_cache_size": "4GB",
		"work_mem":             "4MB",
	}

	return settings, nil
}

// collectDatabases retrieves list of databases
func (cc *ClusterCollector) collectDatabases(ctx context.Context, clusterID string) ([]string, error) {
	pool, err := cc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	_ = pool

	query := `
		SELECT datname
		FROM pg_database
		WHERE datistemplate = false
		ORDER BY datname
	`

	_ = query

	// Placeholder
	databases := []string{"postgres", "myapp"}

	return databases, nil
}

// collectReplicationStatus retrieves replication status
func (cc *ClusterCollector) collectReplicationStatus(ctx context.Context, clusterID string) (map[string]interface{}, error) {
	pool, err := cc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	_ = pool

	query := `
		SELECT 
			application_name,
			client_addr,
			state,
			sync_state,
			sent_lsn,
			write_lsn,
			flush_lsn,
			replay_lsn,
			sync_priority,
			EXTRACT(EPOCH FROM (NOW() - backend_start))::int as uptime_seconds
		FROM pg_stat_replication
	`

	_ = query

	// Placeholder
	replStatus := map[string]interface{}{
		"is_primary": true,
		"replicas":   []interface{}{},
	}

	return replStatus, nil
}

// collectExtensions retrieves list of installed extensions
func (cc *ClusterCollector) collectExtensions(ctx context.Context, clusterID string) ([]string, error) {
	pool, err := cc.pool.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	_ = pool

	query := `
		SELECT extname
		FROM pg_extension
		ORDER BY extname
	`

	_ = query

	// Placeholder
	extensions := []string{"pg_stat_statements", "pgcrypto"}

	return extensions, nil
}

// GetCluster returns cluster information
func (cc *ClusterCollector) GetCluster(clusterID string) (*models.Cluster, error) {
	cluster, exists := cc.clusters[clusterID]
	if !exists {
		return nil, fmt.Errorf("cluster %s not found", clusterID)
	}

	return cluster, nil
}

// GetAllClusters returns all cluster information
func (cc *ClusterCollector) GetAllClusters() []*models.Cluster {
	clusters := make([]*models.Cluster, 0, len(cc.clusters))
	for _, cluster := range cc.clusters {
		clusters = append(clusters, cluster)
	}

	return clusters
}

// RegisterCluster registers a new cluster for monitoring
func (cc *ClusterCollector) RegisterCluster(cluster *models.Cluster) {
	cc.clusters[cluster.ID] = cluster
	cc.log.Infof("Registered cluster %s for monitoring", cluster.ID)
}

// UnregisterCluster removes a cluster from monitoring
func (cc *ClusterCollector) UnregisterCluster(clusterID string) error {
	if _, exists := cc.clusters[clusterID]; !exists {
		return fmt.Errorf("cluster %s not found", clusterID)
	}

	delete(cc.clusters, clusterID)
	cc.log.Infof("Unregistered cluster %s from monitoring", clusterID)

	return nil
}
