package models

import "time"

// Metrics represents database performance metrics
type Metrics struct {
	ClusterID          string    `json:"cluster_id"`
	Timestamp          time.Time `json:"timestamp"`
	ConnectionsActive  int       `json:"connections_active"`
	ConnectionsTotal   int       `json:"connections_total"`
	TransactionsPerSec float64   `json:"transactions_per_sec"`
	CacheHitRatio      float64   `json:"cache_hit_ratio"`
	DiskIORead         float64   `json:"disk_io_read"`
	DiskIOWrite        float64   `json:"disk_io_write"`
	CPUUsage           float64   `json:"cpu_usage"`
	MemoryUsage        float64   `json:"memory_usage"`
	LockWaits          int       `json:"lock_waits"`
	DeadlockCount      int       `json:"deadlock_count"`
	ReplicationLag     int64     `json:"replication_lag_ms"`
	TableBloat         float64   `json:"table_bloat_pct"`
	IndexSize          int64     `json:"index_size_bytes"`
	TableSize          int64     `json:"table_size_bytes"`
}

// NewMetrics creates a new Metrics instance
func NewMetrics(clusterID string) *Metrics {
	return &Metrics{
		ClusterID: clusterID,
		Timestamp: time.Now(),
	}
}

// QueryMetrics represents query-level performance metrics
type QueryMetrics struct {
	QueryID           string    `json:"query_id"`
	Query             string    `json:"query"`
	ClusterID         string    `json:"cluster_id"`
	Database          string    `json:"database"`
	ExecutionTime     float64   `json:"execution_time_ms"`
	PlanningTime      float64   `json:"planning_time_ms"`
	RowsReturned      int64     `json:"rows_returned"`
	RowsAffected      int64     `json:"rows_affected"`
	SharedBlocksHit   int64     `json:"shared_blocks_hit"`
	SharedBlocksRead  int64     `json:"shared_blocks_read"`
	TempBlocksRead    int64     `json:"temp_blocks_read"`
	TempBlocksWritten int64     `json:"temp_blocks_written"`
	Timestamp         time.Time `json:"timestamp"`
	CallCount         int64     `json:"call_count"`
	MeanExecTime      float64   `json:"mean_exec_time_ms"`
	StddevExecTime    float64   `json:"stddev_exec_time_ms"`
}

// NewQueryMetrics creates a new QueryMetrics instance
func NewQueryMetrics(queryID, query, clusterID, database string) *QueryMetrics {
	return &QueryMetrics{
		QueryID:   queryID,
		Query:     query,
		ClusterID: clusterID,
		Database:  database,
		Timestamp: time.Now(),
	}
}

// TableMetrics represents table-level statistics
type TableMetrics struct {
	ClusterID       string     `json:"cluster_id"`
	Database        string     `json:"database"`
	Schema          string     `json:"schema"`
	Table           string     `json:"table"`
	SeqScan         int64      `json:"seq_scan"`
	SeqTupRead      int64      `json:"seq_tup_read"`
	IdxScan         int64      `json:"idx_scan"`
	IdxTupFetch     int64      `json:"idx_tup_fetch"`
	TupInserted     int64      `json:"tup_inserted"`
	TupUpdated      int64      `json:"tup_updated"`
	TupDeleted      int64      `json:"tup_deleted"`
	TupHotUpdated   int64      `json:"tup_hot_updated"`
	LiveTuples      int64      `json:"live_tuples"`
	DeadTuples      int64      `json:"dead_tuples"`
	VacuumCount     int64      `json:"vacuum_count"`
	AutovacuumCount int64      `json:"autovacuum_count"`
	AnalyzeCount    int64      `json:"analyze_count"`
	LastVacuum      *time.Time `json:"last_vacuum,omitempty"`
	LastAutovacuum  *time.Time `json:"last_autovacuum,omitempty"`
	LastAnalyze     *time.Time `json:"last_analyze,omitempty"`
	Timestamp       time.Time  `json:"timestamp"`
}

// NewTableMetrics creates a new TableMetrics instance
func NewTableMetrics(clusterID, database, schema, table string) *TableMetrics {
	return &TableMetrics{
		ClusterID: clusterID,
		Database:  database,
		Schema:    schema,
		Table:     table,
		Timestamp: time.Now(),
	}
}
