// Package metrics exposes pgao internals as Prometheus gauges.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/zvdy/pgao/src/models"
)

type MetricsSource interface {
	LatestMetrics() map[string]*models.Metrics
}

type Exporter struct {
	source MetricsSource

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
		clusterUp:          desc("pgao_cluster_up", "1 if pgao has a cached metrics sample for the cluster"),
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

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range e.descs() {
		ch <- d
	}
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	for clusterID, m := range e.source.LatestMetrics() {
		if m == nil {
			ch <- prometheus.MustNewConstMetric(e.clusterUp, prometheus.GaugeValue, 0, clusterID)
			continue
		}
		ch <- prometheus.MustNewConstMetric(e.clusterUp, prometheus.GaugeValue, 1, clusterID)
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

func (e *Exporter) descs() []*prometheus.Desc {
	return []*prometheus.Desc{
		e.clusterUp, e.connectionsActive, e.connectionsTotal, e.cacheHitRatio,
		e.transactionsPerSec, e.replicationLagMs, e.tableBloatPct,
		e.deadlockCount, e.lockWaits, e.diskIORead, e.diskIOWrite,
	}
}
