// Package metrics exposes pgao internals as Prometheus gauges.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/zvdy/pgao/src/db"
	"github.com/zvdy/pgao/src/models"
)

type MetricsSource interface {
	LatestMetrics() map[string]*models.Metrics
}

// StateSource exposes the per-cluster connection state machine. The exporter
// uses it so pgao_cluster_up reflects the supervisor's view, not just whether
// a cached metrics sample exists. Optional — when nil the exporter falls back
// to the LatestMetrics presence check.
type StateSource interface {
	AllStates() map[string]db.ClusterStatus
}

type Exporter struct {
	source MetricsSource
	states StateSource

	clusterUp          *prometheus.Desc
	connectionsActive  *prometheus.Desc
	connectionsTotal   *prometheus.Desc
	cacheHitRatio      *prometheus.Desc
	transactionsPerSec *prometheus.Desc
	replicationLagMs   *prometheus.Desc
	tableBloatPct      *prometheus.Desc
	deadlockCount      *prometheus.Desc
	lockWaits          *prometheus.Desc
	diskIORead         *prometheus.Desc
	diskIOWrite        *prometheus.Desc
}

func NewExporter(source MetricsSource) *Exporter {
	labels := []string{"cluster"}
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(name, help, labels, nil)
	}
	return &Exporter{
		source:             source,
		clusterUp:          desc("pgao_cluster_up", "1 when the connection supervisor reports the cluster healthy, 0 otherwise"),
		connectionsActive:  desc("pgao_connections_active", "Active backend connections"),
		connectionsTotal:   desc("pgao_connections_total", "max_connections setting"),
		cacheHitRatio:      desc("pgao_cache_hit_ratio", "Buffer cache hit ratio (0-100)"),
		transactionsPerSec: desc("pgao_transactions_per_sec", "TPS over the collection window"),
		replicationLagMs:   desc("pgao_replication_lag_ms", "Replay lag for standbys, ms"),
		tableBloatPct:      desc("pgao_table_bloat_pct", "Avg dead-tuple percentage"),
		deadlockCount:      desc("pgao_deadlock_count", "Deadlocks since last collection"),
		lockWaits:          desc("pgao_lock_waits", "Backends waiting for a lock"),
		diskIORead:         desc("pgao_disk_io_read_kbps", "Block reads, KB/sec"),
		diskIOWrite:        desc("pgao_disk_io_write_kbps", "Block writes, KB/sec"),
	}
}

// WithStateSource wires the connection-pool supervisor so pgao_cluster_up
// reflects health probes rather than just the presence of a metrics sample.
func (e *Exporter) WithStateSource(s StateSource) *Exporter {
	e.states = s
	return e
}

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range e.descs() {
		ch <- d
	}
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	latest := e.source.LatestMetrics()

	// Build a union of every cluster the supervisor knows about AND every
	// cluster that has a metrics sample. That way a cluster that is
	// registered but permanently unreachable still shows up as cluster_up=0
	// instead of dropping off the time series.
	seen := make(map[string]struct{}, len(latest))
	for id := range latest {
		seen[id] = struct{}{}
	}
	var states map[string]db.ClusterStatus
	if e.states != nil {
		states = e.states.AllStates()
		for id := range states {
			seen[id] = struct{}{}
		}
	}

	for clusterID := range seen {
		ch <- prometheus.MustNewConstMetric(e.clusterUp, prometheus.GaugeValue, clusterUpValue(states, latest, clusterID), clusterID)
		m := latest[clusterID]
		if m == nil {
			continue
		}
		ch <- prometheus.MustNewConstMetric(e.connectionsActive, prometheus.GaugeValue, float64(m.ConnectionsActive), clusterID)
		ch <- prometheus.MustNewConstMetric(e.connectionsTotal, prometheus.GaugeValue, float64(m.ConnectionsTotal), clusterID)
		ch <- prometheus.MustNewConstMetric(e.cacheHitRatio, prometheus.GaugeValue, m.CacheHitRatio, clusterID)
		ch <- prometheus.MustNewConstMetric(e.transactionsPerSec, prometheus.GaugeValue, m.TransactionsPerSec, clusterID)
		ch <- prometheus.MustNewConstMetric(e.replicationLagMs, prometheus.GaugeValue, float64(m.ReplicationLag), clusterID)
		ch <- prometheus.MustNewConstMetric(e.tableBloatPct, prometheus.GaugeValue, m.TableBloat, clusterID)
		ch <- prometheus.MustNewConstMetric(e.deadlockCount, prometheus.GaugeValue, float64(m.DeadlockCount), clusterID)
		ch <- prometheus.MustNewConstMetric(e.lockWaits, prometheus.GaugeValue, float64(m.LockWaits), clusterID)
		ch <- prometheus.MustNewConstMetric(e.diskIORead, prometheus.GaugeValue, m.DiskIORead, clusterID)
		ch <- prometheus.MustNewConstMetric(e.diskIOWrite, prometheus.GaugeValue, m.DiskIOWrite, clusterID)
	}
}

// clusterUpValue prefers the supervisor state when available. It falls back
// to "metrics sample exists" so the exporter still works in tests that don't
// wire a StateSource.
func clusterUpValue(states map[string]db.ClusterStatus, latest map[string]*models.Metrics, clusterID string) float64 {
	if st, ok := states[clusterID]; ok {
		if st.State == db.StateHealthy || st.State == db.StateDegraded {
			return 1
		}
		return 0
	}
	if latest[clusterID] != nil {
		return 1
	}
	return 0
}

func (e *Exporter) descs() []*prometheus.Desc {
	return []*prometheus.Desc{
		e.clusterUp, e.connectionsActive, e.connectionsTotal, e.cacheHitRatio,
		e.transactionsPerSec, e.replicationLagMs, e.tableBloatPct,
		e.deadlockCount, e.lockWaits, e.diskIORead, e.diskIOWrite,
	}
}
