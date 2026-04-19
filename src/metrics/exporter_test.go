package metrics

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/zvdy/pgao/src/models"
)

type fakeSource struct {
	snap map[string]*models.Metrics
}

func (f *fakeSource) LatestMetrics() map[string]*models.Metrics { return f.snap }

func gather(t *testing.T, exp *Exporter) string {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	if err := reg.Register(exp); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := testutil.GatherAndCount(reg); err != nil {
		t.Fatalf("gather count: %v", err)
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var sb strings.Builder
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			fmt.Fprintf(&sb, "%s", mf.GetName())
			for _, l := range m.GetLabel() {
				fmt.Fprintf(&sb, " %s=%s", l.GetName(), l.GetValue())
			}
			if g := m.GetGauge(); g != nil {
				fmt.Fprintf(&sb, " =%s", strconv.FormatFloat(g.GetValue(), 'f', -1, 64))
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func TestExporter_EmitsExpectedSeries(t *testing.T) {
	src := &fakeSource{snap: map[string]*models.Metrics{
		"c1": {
			ClusterID: "c1", ConnectionsActive: 12, ConnectionsTotal: 100,
			CacheHitRatio: 99.4, TransactionsPerSec: 250,
			ReplicationLag: 75, TableBloat: 5.5,
			DeadlockCount: 3, LockWaits: 1,
			DiskIORead: 800, DiskIOWrite: 1600,
		},
	}}
	out := gather(t, NewExporter(src))

	for _, want := range []string{
		`pgao_cluster_up cluster=c1 =1`,
		`pgao_connections_active cluster=c1 =12`,
		`pgao_cache_hit_ratio cluster=c1 =99.4`,
		`pgao_transactions_per_sec cluster=c1 =250`,
		`pgao_deadlock_count cluster=c1 =3`,
		`pgao_disk_io_read_kbps cluster=c1 =800`,
		`pgao_disk_io_write_kbps cluster=c1 =1600`,
		`pgao_replication_lag_ms cluster=c1 =75`,
		`pgao_table_bloat_pct cluster=c1 =5.5`,
		`pgao_lock_waits cluster=c1 =1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestExporter_NilSnapshotReportsClusterDown(t *testing.T) {
	src := &fakeSource{snap: map[string]*models.Metrics{"c1": nil}}
	out := gather(t, NewExporter(src))

	if !strings.Contains(out, `pgao_cluster_up cluster=c1 =0`) {
		t.Errorf("expected cluster_up=0 for nil snapshot, got:\n%s", out)
	}
	if strings.Contains(out, "pgao_connections_active") {
		t.Errorf("nil snapshot should not emit other gauges, got:\n%s", out)
	}
}

func TestExporter_DescribeReturnsAllDescs(t *testing.T) {
	exp := NewExporter(&fakeSource{snap: map[string]*models.Metrics{}})
	ch := make(chan *prometheus.Desc, 32)
	exp.Describe(ch)
	close(ch)
	got := 0
	for range ch {
		got++
	}
	if got != len(exp.descs()) {
		t.Errorf("expected %d descs, got %d", len(exp.descs()), got)
	}
}

func TestExporter_PerClusterIsolation(t *testing.T) {
	src := &fakeSource{snap: map[string]*models.Metrics{
		"a": {ClusterID: "a", ConnectionsActive: 1},
		"b": {ClusterID: "b", ConnectionsActive: 2},
	}}
	out := gather(t, NewExporter(src))
	if !strings.Contains(out, `pgao_connections_active cluster=a =1`) ||
		!strings.Contains(out, `pgao_connections_active cluster=b =2`) {
		t.Errorf("expected both cluster series, got:\n%s", out)
	}
}
