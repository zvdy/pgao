package db

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

// ConnectionPool manages database connections
type ConnectionPool struct {
	pools map[string]*pgxpool.Pool
	mu    sync.RWMutex
	log   *logrus.Logger
}

// ConnectionConfig holds database connection configuration
type ConnectionConfig struct {
	Host            string
	Port            int
	User            string
	Password        string
	Database        string
	MaxConnections  int
	MinConnections  int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
	SSLMode         string
}

// NewConnectionPool creates a new connection pool manager
func NewConnectionPool(log *logrus.Logger) *ConnectionPool {
	return &ConnectionPool{
		pools: make(map[string]*pgxpool.Pool),
		log:   log,
	}
}

// AddCluster adds a new cluster connection to the pool
func (cp *ConnectionPool) AddCluster(clusterID string, config ConnectionConfig) error {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	// Check if already exists
	if _, exists := cp.pools[clusterID]; exists {
		return fmt.Errorf("cluster %s already exists in pool", clusterID)
	}

	// Build connection string
	connString := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		config.User,
		config.Password,
		config.Host,
		config.Port,
		config.Database,
		config.SSLMode,
	)

	// Parse connection string and create pool config
	poolConfig, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return fmt.Errorf("failed to parse connection string: %w", err)
	}

	// Configure pool
	if config.MaxConnections > 0 {
		poolConfig.MaxConns = int32(config.MaxConnections)
	} else {
		poolConfig.MaxConns = 25 // default
	}

	if config.MinConnections > 0 {
		poolConfig.MinConns = int32(config.MinConnections)
	} else {
		poolConfig.MinConns = 5 // default
	}

	if config.ConnMaxLifetime > 0 {
		poolConfig.MaxConnLifetime = config.ConnMaxLifetime
	} else {
		poolConfig.MaxConnLifetime = time.Hour
	}

	if config.ConnMaxIdleTime > 0 {
		poolConfig.MaxConnIdleTime = config.ConnMaxIdleTime
	} else {
		poolConfig.MaxConnIdleTime = 30 * time.Minute
	}

	// Create pool
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("failed to ping database: %w", err)
	}

	cp.pools[clusterID] = pool
	cp.log.Infof("Successfully connected to cluster %s", clusterID)

	return nil
}

// GetPool returns the connection pool for a cluster
func (cp *ConnectionPool) GetPool(clusterID string) (*pgxpool.Pool, error) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	pool, exists := cp.pools[clusterID]
	if !exists {
		return nil, fmt.Errorf("no connection pool found for cluster %s", clusterID)
	}

	return pool, nil
}

// HealthCheck performs a health check on a cluster connection
func (cp *ConnectionPool) HealthCheck(clusterID string) error {
	pool, err := cp.GetPool(clusterID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return pool.Ping(ctx)
}

// GetAllClusters returns a list of all cluster IDs
func (cp *ConnectionPool) GetAllClusters() []string {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	clusters := make([]string, 0, len(cp.pools))
	for clusterID := range cp.pools {
		clusters = append(clusters, clusterID)
	}

	return clusters
}

// RemoveCluster removes a cluster from the pool
func (cp *ConnectionPool) RemoveCluster(clusterID string) error {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	pool, exists := cp.pools[clusterID]
	if !exists {
		return fmt.Errorf("cluster %s not found in pool", clusterID)
	}

	pool.Close()
	delete(cp.pools, clusterID)
	cp.log.Infof("Removed cluster %s from pool", clusterID)

	return nil
}

// Close closes all connections in the pool
func (cp *ConnectionPool) Close() {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	for clusterID, pool := range cp.pools {
		pool.Close()
		cp.log.Infof("Closed connection pool for cluster %s", clusterID)
	}

	cp.pools = make(map[string]*pgxpool.Pool)
}

// GetPoolStats returns statistics for a cluster's connection pool
func (cp *ConnectionPool) GetPoolStats(clusterID string) (map[string]interface{}, error) {
	pool, err := cp.GetPool(clusterID)
	if err != nil {
		return nil, err
	}

	stat := pool.Stat()
	stats := map[string]interface{}{
		"acquired_conns":             stat.AcquiredConns(),
		"canceled_acquire_count":     stat.CanceledAcquireCount(),
		"constructing_conns":         stat.ConstructingConns(),
		"empty_acquire_count":        stat.EmptyAcquireCount(),
		"idle_conns":                 stat.IdleConns(),
		"max_conns":                  stat.MaxConns(),
		"total_conns":                stat.TotalConns(),
		"new_conns_count":            stat.NewConnsCount(),
		"max_lifetime_destroy_count": stat.MaxLifetimeDestroyCount(),
		"max_idle_destroy_count":     stat.MaxIdleDestroyCount(),
	}

	return stats, nil
}

// ExecuteQuery executes a query on a specific cluster
// Note: This is a simplified wrapper. For production use, implement proper row handling
func (cp *ConnectionPool) ExecuteQuery(ctx context.Context, clusterID, query string, args ...interface{}) error {
	pool, err := cp.GetPool(clusterID)
	if err != nil {
		return err
	}

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to execute query: %w", err)
	}
	defer rows.Close()

	// Note: Caller should process rows before this returns
	return nil
}

// QueryRow executes a query that returns a single row
func (cp *ConnectionPool) QueryRow(ctx context.Context, clusterID, query string, args ...interface{}) error {
	pool, err := cp.GetPool(clusterID)
	if err != nil {
		return err
	}

	row := pool.QueryRow(ctx, query, args...)
	// Caller should scan the row
	_ = row

	return nil
}
