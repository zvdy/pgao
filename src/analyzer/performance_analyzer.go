package analyzer

import (
	"fmt"
	"time"

	"github.com/zvdy/pgao/src/models"
)

// PerformanceAnalyzer analyzes database performance metrics
type PerformanceAnalyzer struct {
	thresholds PerformanceThresholds
}

// PerformanceThresholds defines performance thresholds
type PerformanceThresholds struct {
	MaxConnectionsPercent float64
	MinCacheHitRatio      float64
	MaxCPUPercent         float64
	MaxMemoryPercent      float64
	MaxReplicationLagMs   int64
	MaxSlowQueryTimeMs    float64
	MaxTableBloatPercent  float64
}

// DefaultThresholds returns default performance thresholds
func DefaultThresholds() PerformanceThresholds {
	return PerformanceThresholds{
		MaxConnectionsPercent: 80.0,
		MinCacheHitRatio:      95.0,
		MaxCPUPercent:         80.0,
		MaxMemoryPercent:      85.0,
		MaxReplicationLagMs:   10000,  // 10 seconds
		MaxSlowQueryTimeMs:    1000.0, // 1 second
		MaxTableBloatPercent:  20.0,
	}
}

// NewPerformanceAnalyzer creates a new PerformanceAnalyzer instance
func NewPerformanceAnalyzer() *PerformanceAnalyzer {
	return &PerformanceAnalyzer{
		thresholds: DefaultThresholds(),
	}
}

// NewPerformanceAnalyzerWithThresholds creates a new analyzer with custom thresholds
func NewPerformanceAnalyzerWithThresholds(thresholds PerformanceThresholds) *PerformanceAnalyzer {
	return &PerformanceAnalyzer{
		thresholds: thresholds,
	}
}

// AnalyzeMetrics analyzes cluster metrics and generates alerts
func (pa *PerformanceAnalyzer) AnalyzeMetrics(metrics *models.Metrics) []*models.Alert {
	alerts := make([]*models.Alert, 0)

	// Check connection usage
	if metrics.ConnectionsTotal > 0 {
		connPercent := (float64(metrics.ConnectionsActive) / float64(metrics.ConnectionsTotal)) * 100
		if connPercent > pa.thresholds.MaxConnectionsPercent {
			alert := models.NewAlert(
				models.AlertTypeConnection,
				pa.getSeverity(connPercent, pa.thresholds.MaxConnectionsPercent, 90.0, 95.0),
				metrics.ClusterID,
				"High Connection Usage",
				fmt.Sprintf("Active connections at %.1f%% of maximum capacity", connPercent),
			)
			alert.Metric = "connections_active"
			alert.Threshold = pa.thresholds.MaxConnectionsPercent
			alert.CurrentValue = connPercent
			alert.AddAction("Consider increasing max_connections or optimizing connection pooling")
			alerts = append(alerts, alert)
		}
	}

	// Check cache hit ratio
	if metrics.CacheHitRatio < pa.thresholds.MinCacheHitRatio {
		alert := models.NewAlert(
			models.AlertTypePerformance,
			pa.getSeverityBelow(metrics.CacheHitRatio, pa.thresholds.MinCacheHitRatio, 90.0, 85.0),
			metrics.ClusterID,
			"Low Cache Hit Ratio",
			fmt.Sprintf("Cache hit ratio at %.1f%%, below recommended %.1f%%", metrics.CacheHitRatio, pa.thresholds.MinCacheHitRatio),
		)
		alert.Metric = "cache_hit_ratio"
		alert.Threshold = pa.thresholds.MinCacheHitRatio
		alert.CurrentValue = metrics.CacheHitRatio
		alert.AddAction("Consider increasing shared_buffers")
		alert.AddAction("Review query patterns for optimization")
		alerts = append(alerts, alert)
	}

	// Check CPU usage
	if metrics.CPUUsage > pa.thresholds.MaxCPUPercent {
		alert := models.NewAlert(
			models.AlertTypePerformance,
			pa.getSeverity(metrics.CPUUsage, pa.thresholds.MaxCPUPercent, 90.0, 95.0),
			metrics.ClusterID,
			"High CPU Usage",
			fmt.Sprintf("CPU usage at %.1f%%", metrics.CPUUsage),
		)
		alert.Metric = "cpu_usage"
		alert.Threshold = pa.thresholds.MaxCPUPercent
		alert.CurrentValue = metrics.CPUUsage
		alert.AddAction("Identify and optimize expensive queries")
		alert.AddAction("Consider scaling up the instance")
		alerts = append(alerts, alert)
	}

	// Check memory usage
	if metrics.MemoryUsage > pa.thresholds.MaxMemoryPercent {
		alert := models.NewAlert(
			models.AlertTypeCapacity,
			pa.getSeverity(metrics.MemoryUsage, pa.thresholds.MaxMemoryPercent, 90.0, 95.0),
			metrics.ClusterID,
			"High Memory Usage",
			fmt.Sprintf("Memory usage at %.1f%%", metrics.MemoryUsage),
		)
		alert.Metric = "memory_usage"
		alert.Threshold = pa.thresholds.MaxMemoryPercent
		alert.CurrentValue = metrics.MemoryUsage
		alert.AddAction("Review and optimize memory-intensive queries")
		alert.AddAction("Consider increasing available memory")
		alerts = append(alerts, alert)
	}

	// Check replication lag
	if metrics.ReplicationLag > pa.thresholds.MaxReplicationLagMs {
		alert := models.NewAlert(
			models.AlertTypeReplication,
			pa.getSeverityLag(metrics.ReplicationLag, pa.thresholds.MaxReplicationLagMs, 30000, 60000),
			metrics.ClusterID,
			"High Replication Lag",
			fmt.Sprintf("Replication lag at %dms", metrics.ReplicationLag),
		)
		alert.Metric = "replication_lag"
		alert.Threshold = float64(pa.thresholds.MaxReplicationLagMs)
		alert.CurrentValue = float64(metrics.ReplicationLag)
		alert.AddAction("Check network connectivity between primary and replica")
		alert.AddAction("Review write load on primary")
		alerts = append(alerts, alert)
	}

	// Check for lock waits
	if metrics.LockWaits > 100 {
		alert := models.NewAlert(
			models.AlertTypePerformance,
			models.AlertSeverityMedium,
			metrics.ClusterID,
			"High Lock Waits",
			fmt.Sprintf("%d queries waiting for locks", metrics.LockWaits),
		)
		alert.Metric = "lock_waits"
		alert.CurrentValue = float64(metrics.LockWaits)
		alert.AddAction("Review long-running transactions")
		alert.AddAction("Optimize query access patterns")
		alerts = append(alerts, alert)
	}

	// Check for deadlocks
	if metrics.DeadlockCount > 0 {
		alert := models.NewAlert(
			models.AlertTypePerformance,
			models.AlertSeverityHigh,
			metrics.ClusterID,
			"Deadlocks Detected",
			fmt.Sprintf("%d deadlocks detected", metrics.DeadlockCount),
		)
		alert.Metric = "deadlock_count"
		alert.CurrentValue = float64(metrics.DeadlockCount)
		alert.AddAction("Review transaction ordering")
		alert.AddAction("Consider implementing retry logic")
		alerts = append(alerts, alert)
	}

	// Check table bloat
	if metrics.TableBloat > pa.thresholds.MaxTableBloatPercent {
		alert := models.NewAlert(
			models.AlertTypeCapacity,
			pa.getSeverity(metrics.TableBloat, pa.thresholds.MaxTableBloatPercent, 30.0, 40.0),
			metrics.ClusterID,
			"High Table Bloat",
			fmt.Sprintf("Table bloat at %.1f%%", metrics.TableBloat),
		)
		alert.Metric = "table_bloat"
		alert.Threshold = pa.thresholds.MaxTableBloatPercent
		alert.CurrentValue = metrics.TableBloat
		alert.AddAction("Run VACUUM ANALYZE")
		alert.AddAction("Consider VACUUM FULL for heavily bloated tables")
		alerts = append(alerts, alert)
	}

	return alerts
}

// AnalyzeQueryPerformance analyzes query performance
func (pa *PerformanceAnalyzer) AnalyzeQueryPerformance(qm *models.QueryMetrics) []*models.Alert {
	alerts := make([]*models.Alert, 0)

	// Check slow queries
	if qm.ExecutionTime > pa.thresholds.MaxSlowQueryTimeMs {
		severity := models.AlertSeverityMedium
		if qm.ExecutionTime > pa.thresholds.MaxSlowQueryTimeMs*5 {
			severity = models.AlertSeverityHigh
		}
		if qm.ExecutionTime > pa.thresholds.MaxSlowQueryTimeMs*10 {
			severity = models.AlertSeverityCritical
		}

		alert := models.NewAlert(
			models.AlertTypeQuery,
			severity,
			qm.ClusterID,
			"Slow Query Detected",
			fmt.Sprintf("Query took %.2fms to execute", qm.ExecutionTime),
		)
		alert.Metric = "execution_time"
		alert.Threshold = pa.thresholds.MaxSlowQueryTimeMs
		alert.CurrentValue = qm.ExecutionTime
		alert.Metadata = map[string]interface{}{
			"query_id": qm.QueryID,
			"database": qm.Database,
		}
		alert.AddAction("Analyze query with EXPLAIN ANALYZE")
		alert.AddAction("Check for missing indexes")
		alert.AddAction("Consider query optimization")
		alerts = append(alerts, alert)
	}

	// Check for high temp blocks usage (indicates lack of work_mem)
	if qm.TempBlocksRead > 10000 || qm.TempBlocksWritten > 10000 {
		alert := models.NewAlert(
			models.AlertTypePerformance,
			models.AlertSeverityMedium,
			qm.ClusterID,
			"High Temp Block Usage",
			fmt.Sprintf("Query using excessive temp blocks (read: %d, written: %d)", qm.TempBlocksRead, qm.TempBlocksWritten),
		)
		alert.Metadata = map[string]interface{}{
			"query_id":            qm.QueryID,
			"temp_blocks_read":    qm.TempBlocksRead,
			"temp_blocks_written": qm.TempBlocksWritten,
		}
		alert.AddAction("Consider increasing work_mem")
		alert.AddAction("Optimize sort and hash operations")
		alerts = append(alerts, alert)
	}

	return alerts
}

// GenerateHealthStatus generates overall health status for a cluster
func (pa *PerformanceAnalyzer) GenerateHealthStatus(clusterID string, metrics *models.Metrics, alerts []*models.Alert) *models.HealthStatus {
	health := models.NewHealthStatus(clusterID)

	// Count alerts by severity
	criticalCount := 0
	for _, alert := range alerts {
		if alert.Status == "active" {
			health.ActiveAlerts++
			if alert.Severity == models.AlertSeverityCritical {
				criticalCount++
			}
		}
	}
	health.CriticalAlerts = criticalCount

	// Add health checks
	health.AddCheck(models.HealthCheck{
		Name:        "Database Connectivity",
		Status:      "ok",
		Message:     "Database is reachable",
		LastChecked: time.Now(),
	})

	if metrics.ConnectionsTotal > 0 {
		connPercent := (float64(metrics.ConnectionsActive) / float64(metrics.ConnectionsTotal)) * 100
		status := "ok"
		message := fmt.Sprintf("%.1f%% connections in use", connPercent)
		if connPercent > pa.thresholds.MaxConnectionsPercent {
			status = "warning"
		}
		health.AddCheck(models.HealthCheck{
			Name:        "Connection Pool",
			Status:      status,
			Message:     message,
			LastChecked: time.Now(),
			Value:       connPercent,
		})
	}

	cacheStatus := "ok"
	if metrics.CacheHitRatio < pa.thresholds.MinCacheHitRatio {
		cacheStatus = "warning"
	}
	health.AddCheck(models.HealthCheck{
		Name:        "Cache Performance",
		Status:      cacheStatus,
		Message:     fmt.Sprintf("%.1f%% cache hit ratio", metrics.CacheHitRatio),
		LastChecked: time.Now(),
		Value:       metrics.CacheHitRatio,
	})

	cpuStatus := "ok"
	if metrics.CPUUsage > pa.thresholds.MaxCPUPercent {
		cpuStatus = "warning"
	}
	health.AddCheck(models.HealthCheck{
		Name:        "CPU Usage",
		Status:      cpuStatus,
		Message:     fmt.Sprintf("%.1f%% CPU usage", metrics.CPUUsage),
		LastChecked: time.Now(),
		Value:       metrics.CPUUsage,
	})

	memStatus := "ok"
	if metrics.MemoryUsage > pa.thresholds.MaxMemoryPercent {
		memStatus = "warning"
	}
	health.AddCheck(models.HealthCheck{
		Name:        "Memory Usage",
		Status:      memStatus,
		Message:     fmt.Sprintf("%.1f%% memory usage", metrics.MemoryUsage),
		LastChecked: time.Now(),
		Value:       metrics.MemoryUsage,
	})

	return health
}

// getSeverity determines severity based on thresholds
func (pa *PerformanceAnalyzer) getSeverity(value, warning, high, critical float64) models.AlertSeverity {
	switch {
	case value >= critical:
		return models.AlertSeverityCritical
	case value >= high:
		return models.AlertSeverityHigh
	case value >= warning:
		return models.AlertSeverityMedium
	default:
		return models.AlertSeverityLow
	}
}

// getSeverityBelow determines severity for values that should be above threshold
func (pa *PerformanceAnalyzer) getSeverityBelow(value, warning, high, critical float64) models.AlertSeverity {
	switch {
	case value <= critical:
		return models.AlertSeverityCritical
	case value <= high:
		return models.AlertSeverityHigh
	case value <= warning:
		return models.AlertSeverityMedium
	default:
		return models.AlertSeverityLow
	}
}

// getSeverityLag determines severity for lag values
func (pa *PerformanceAnalyzer) getSeverityLag(value, warning, high, critical int64) models.AlertSeverity {
	switch {
	case value >= critical:
		return models.AlertSeverityCritical
	case value >= high:
		return models.AlertSeverityHigh
	case value >= warning:
		return models.AlertSeverityMedium
	default:
		return models.AlertSeverityLow
	}
}
