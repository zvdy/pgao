package db

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

// ConnectionPool manages database connections plus a per-cluster
// state machine. Failed clusters are NOT dropped: they stay registered
// so the background supervisor can probe and recover them.
type ConnectionPool struct {
	pools  map[string]*pgxpool.Pool
	mu     sync.RWMutex
	log    *logrus.Logger
	status *statusMap

	// Probe is the function called by Supervise to test cluster health.
	// Defaults to pool.Ping; tests inject a fake.
	probe func(ctx context.Context, clusterID string) error

	// SupervisorConfig governs probe cadence + backoff. Mutated only via
	// SetSupervisorConfig before Supervise starts.
	supervisorCfg SupervisorConfig
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

// clampToInt32 narrows an int pool-size to int32, saturating on overflow so
// absurd config values can't wrap negative.
func clampToInt32(v int) int32 {
	switch {
	case v > math.MaxInt32:
		return math.MaxInt32
	case v < 0:
		return 0
	default:
		return int32(v)
	}
}

// NewConnectionPool creates a new connection pool manager. The default probe
// uses pgxpool.Pool.Ping; tests can swap it via SetProbe.
func NewConnectionPool(log *logrus.Logger) *ConnectionPool {
	cp := &ConnectionPool{
		pools:  make(map[string]*pgxpool.Pool),
		log:    log,
		status: newStatusMap(),
	}
	cp.probe = cp.defaultProbe
	return cp
}

// SetProbe overrides the cluster health probe. Used by tests so the supervisor
// can be exercised without a real PostgreSQL.
func (cp *ConnectionPool) SetProbe(probe func(ctx context.Context, clusterID string) error) {
	cp.probe = probe
}

// SetSupervisorConfig overrides the default probe cadence + backoff. Must be
// called before Supervise; later changes have no effect.
func (cp *ConnectionPool) SetSupervisorConfig(cfg SupervisorConfig) {
	cp.supervisorCfg = cfg
}

// defaultProbe pings the cluster pool. Used as the default cp.probe.
func (cp *ConnectionPool) defaultProbe(ctx context.Context, clusterID string) error {
	pool, err := cp.GetPool(clusterID)
	if err != nil {
		return err
	}
	return pool.Ping(ctx)
}

// AddCluster registers a cluster and creates its pgxpool. The pool itself
// connects lazily on first use, so this call does NOT ping; the supervisor
// (started via Supervise) handles probing and recovery. A pool that fails to
// build (parse error, DNS resolution failure on the connection string) is
// still registered with state=Unhealthy so the supervisor can retry.
func (cp *ConnectionPool) AddCluster(clusterID string, config ConnectionConfig) error {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	if _, exists := cp.pools[clusterID]; exists {
		return fmt.Errorf("cluster %s already exists in pool", clusterID)
	}

	connString := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		url.QueryEscape(config.User),
		url.QueryEscape(config.Password),
		config.Host,
		config.Port,
		url.QueryEscape(config.Database),
		url.QueryEscape(config.SSLMode),
	)

	poolConfig, err := pgxpool.ParseConfig(connString)
	if err != nil {
		// The string itself is malformed. Register as unhealthy so the
		// supervisor surfaces the problem; the operator can fix config and
		// re-add the cluster after a restart.
		cp.status.ensure(clusterID)
		cp.status.transition(clusterID, false, err, time.Now(), cp.supervisorCfg.withDefaults().MinBackoff)
		cp.log.WithError(err).WithField("cluster_id", clusterID).Error("invalid cluster connection string")
		return nil
	}

	if config.MaxConnections > 0 {
		poolConfig.MaxConns = clampToInt32(config.MaxConnections)
	} else {
		poolConfig.MaxConns = 25
	}
	if config.MinConnections > 0 {
		poolConfig.MinConns = clampToInt32(config.MinConnections)
	} else {
		poolConfig.MinConns = 5
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

	// pgxpool.NewWithConfig does not actually open connections — it lazily
	// dials on first use. This call only fails for malformed config.
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		cp.status.ensure(clusterID)
		cp.status.transition(clusterID, false, err, time.Now(), cp.supervisorCfg.withDefaults().MinBackoff)
		cp.log.WithError(err).WithField("cluster_id", clusterID).Error("failed to build connection pool")
		return nil
	}

	cp.pools[clusterID] = pool
	cp.status.ensure(clusterID)
	cp.log.WithField("cluster_id", clusterID).Info("registered cluster (state=connecting)")
	return nil
}

// GetState returns a snapshot of the cluster's current state. Returns
// (zero, false) if the cluster is not registered.
func (cp *ConnectionPool) GetState(clusterID string) (ClusterStatus, bool) {
	return cp.status.snapshot(clusterID)
}

// AllStates returns a snapshot of every registered cluster's state.
func (cp *ConnectionPool) AllStates() map[string]ClusterStatus {
	return cp.status.all()
}

// Supervise runs a per-cluster health probe loop until ctx is cancelled.
// On success the cluster transitions to Healthy and the next probe is
// scheduled HealthInterval out. On failure the cluster goes Unhealthy and
// the next probe is scheduled with jittered exponential backoff (5 s → 60 s
// by default).
func (cp *ConnectionPool) Supervise(ctx context.Context) {
	cfg := cp.supervisorCfg.withDefaults()
	cp.log.WithFields(logrus.Fields{
		"health_interval": cfg.HealthInterval,
		"min_backoff":     cfg.MinBackoff,
		"max_backoff":     cfg.MaxBackoff,
	}).Info("connection supervisor started")

	// Tick at the smallest useful granularity (MinBackoff/2). The per-cluster
	// nextProbe field is what actually gates whether a probe runs this tick.
	tick := cfg.MinBackoff / 2
	if tick <= 0 {
		tick = time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	// Run an immediate first pass so /ready reflects reality before the
	// first tick fires.
	cp.runProbeRound(ctx, cfg)

	for {
		select {
		case <-ctx.Done():
			cp.log.Info("connection supervisor stopped")
			return
		case <-ticker.C:
			cp.runProbeRound(ctx, cfg)
		}
	}
}

// runProbeRound walks every registered cluster and probes any whose
// nextProbe has elapsed.
func (cp *ConnectionPool) runProbeRound(ctx context.Context, cfg SupervisorConfig) {
	now := time.Now()
	for _, id := range cp.GetAllClusterIDs() {
		state, ok := cp.status.snapshot(id)
		if !ok {
			continue
		}
		if !state.NextProbe.IsZero() && now.Before(state.NextProbe) {
			continue
		}
		cp.probeOnce(ctx, id, cfg)
	}
}

// probeOnce runs one probe and updates state.
func (cp *ConnectionPool) probeOnce(ctx context.Context, clusterID string, cfg SupervisorConfig) {
	probeCtx, cancel := context.WithTimeout(ctx, cfg.ProbeTimeout)
	defer cancel()

	err := cp.probe(probeCtx, clusterID)
	now := time.Now()
	if err == nil {
		cp.status.transition(clusterID, true, nil, now, cfg.HealthInterval)
		cp.log.WithField("cluster_id", clusterID).Debug("cluster probe ok")
		return
	}

	prev, _ := cp.status.snapshot(clusterID)
	failed := prev.FailedProbes + 1
	backoff := cfg.nextBackoff(failed)
	backoff = applyJitter(backoff)
	cp.status.transition(clusterID, false, err, now, backoff)
	cp.log.WithError(err).WithFields(logrus.Fields{
		"cluster_id":   clusterID,
		"failed_count": failed,
		"next_probe":   backoff,
	}).Warn("cluster probe failed")
}

// applyJitter spreads probe attempts across retries by adding ±25% noise.
// Without jitter every cluster recovering from a shared outage would probe
// in lockstep and stampede the database the moment it comes back.
func applyJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return d
	}
	// Use only the low 32 bits for a uniform float in [0, 1).
	r := float64(binary.LittleEndian.Uint32(b[:4])) / float64(1<<32)
	jitter := (r - 0.5) * 0.5 * float64(d) // ±25%
	return d + time.Duration(jitter)
}

// GetAllClusterIDs returns every registered cluster ID, regardless of state.
// Distinct from GetAllClusters which only returns clusters with a live pool;
// the supervisor needs to see clusters whose pool failed to build too.
func (cp *ConnectionPool) GetAllClusterIDs() []string {
	cp.mu.RLock()
	pooled := make(map[string]struct{}, len(cp.pools))
	for id := range cp.pools {
		pooled[id] = struct{}{}
	}
	cp.mu.RUnlock()

	for id := range cp.status.all() {
		pooled[id] = struct{}{}
	}
	out := make([]string, 0, len(pooled))
	for id := range pooled {
		out = append(out, id)
	}
	return out
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

// PingAll pings every registered cluster concurrently. Returns a map of
// clusterID to ping error (nil on success). The shared ctx bounds the entire
// call; slow clusters surface as context.DeadlineExceeded errors.
func (cp *ConnectionPool) PingAll(ctx context.Context) map[string]error {
	cp.mu.RLock()
	snapshot := make(map[string]*pgxpool.Pool, len(cp.pools))
	for id, p := range cp.pools {
		snapshot[id] = p
	}
	cp.mu.RUnlock()

	results := make(map[string]error, len(snapshot))
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for id, pool := range snapshot {
		wg.Add(1)
		go func(id string, pool *pgxpool.Pool) {
			defer wg.Done()
			err := pool.Ping(ctx)
			mu.Lock()
			results[id] = err
			mu.Unlock()
		}(id, pool)
	}
	wg.Wait()
	return results
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
	cp.status.remove(clusterID)
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
