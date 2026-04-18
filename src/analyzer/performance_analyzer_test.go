package analyzer

import (
	"testing"

	"github.com/zvdy/pgao/src/models"
)

func TestAnalyzeMetrics_NoAlertsWhenHealthy(t *testing.T) {
	pa := NewPerformanceAnalyzer()
	m := &models.Metrics{
		ClusterID:         "c1",
		ConnectionsActive: 10,
		ConnectionsTotal:  100,
		CacheHitRatio:     99.5,
		CPUUsage:          20,
		MemoryUsage:       30,
		ReplicationLag:    0,
		LockWaits:         0,
		DeadlockCount:     0,
		TableBloat:        5,
	}

	alerts := pa.AnalyzeMetrics(m)
	if len(alerts) != 0 {
		t.Errorf("healthy cluster should produce 0 alerts, got %d: %+v", len(alerts), alerts)
	}
}

func TestAnalyzeMetrics_FiresExpectedAlerts(t *testing.T) {
	pa := NewPerformanceAnalyzer()
	m := &models.Metrics{
		ClusterID:         "c1",
		ConnectionsActive: 95,
		ConnectionsTotal:  100,
		CacheHitRatio:     80,
		CPUUsage:          92,
		MemoryUsage:       90,
		ReplicationLag:    70_000,
		LockWaits:         500,
		DeadlockCount:     3,
		TableBloat:        45,
	}

	alerts := pa.AnalyzeMetrics(m)

	want := map[string]bool{
		"connections_active": false,
		"cache_hit_ratio":    false,
		"cpu_usage":          false,
		"memory_usage":       false,
		"replication_lag":    false,
		"lock_waits":         false,
		"deadlock_count":     false,
		"table_bloat":        false,
	}
	for _, a := range alerts {
		want[a.Metric] = true
	}
	for metric, seen := range want {
		if !seen {
			t.Errorf("expected alert for metric %s", metric)
		}
	}
}

func TestSeverityLadder(t *testing.T) {
	pa := NewPerformanceAnalyzer()
	cases := []struct {
		value, warning, high, critical float64
		want                           models.AlertSeverity
	}{
		{50, 80, 90, 95, models.AlertSeverityLow},
		{85, 80, 90, 95, models.AlertSeverityMedium},
		{92, 80, 90, 95, models.AlertSeverityHigh},
		{99, 80, 90, 95, models.AlertSeverityCritical},
	}
	for _, c := range cases {
		got := pa.getSeverity(c.value, c.warning, c.high, c.critical)
		if got != c.want {
			t.Errorf("getSeverity(%v) = %q, want %q", c.value, got, c.want)
		}
	}
}

func TestGenerateHealthStatus_CountsCritical(t *testing.T) {
	pa := NewPerformanceAnalyzer()
	m := &models.Metrics{ClusterID: "c1", ConnectionsTotal: 100, ConnectionsActive: 10, CacheHitRatio: 99}

	alerts := []*models.Alert{
		{Status: "active", Severity: models.AlertSeverityCritical},
		{Status: "active", Severity: models.AlertSeverityHigh},
		{Status: "resolved", Severity: models.AlertSeverityCritical},
	}
	hs := pa.GenerateHealthStatus("c1", m, alerts)
	if hs.ActiveAlerts != 2 {
		t.Errorf("expected 2 active alerts, got %d", hs.ActiveAlerts)
	}
	if hs.CriticalAlerts != 1 {
		t.Errorf("expected 1 critical alert (resolved one excluded), got %d", hs.CriticalAlerts)
	}
	if len(hs.Checks) == 0 {
		t.Error("expected health checks to be populated")
	}
}

func TestAnalyzeQueryPerformance_SlowQueryEscalates(t *testing.T) {
	pa := NewPerformanceAnalyzer()

	mild := &models.QueryMetrics{ClusterID: "c1", ExecutionTime: 1500}
	severe := &models.QueryMetrics{ClusterID: "c1", ExecutionTime: 15000}

	mildAlerts := pa.AnalyzeQueryPerformance(mild)
	severeAlerts := pa.AnalyzeQueryPerformance(severe)

	if len(mildAlerts) == 0 || len(severeAlerts) == 0 {
		t.Fatal("expected at least one alert for each slow query")
	}
	if mildAlerts[0].Severity == models.AlertSeverityCritical {
		t.Errorf("1.5s query should not be critical, got %s", mildAlerts[0].Severity)
	}
	if severeAlerts[0].Severity != models.AlertSeverityCritical {
		t.Errorf("15s query should be critical, got %s", severeAlerts[0].Severity)
	}
}

func TestCustomThresholdsOverrideDefaults(t *testing.T) {
	thresholds := DefaultThresholds()
	thresholds.MinCacheHitRatio = 50

	pa := NewPerformanceAnalyzerWithThresholds(thresholds)

	m := &models.Metrics{ClusterID: "c1", ConnectionsTotal: 100, ConnectionsActive: 10, CacheHitRatio: 60}
	alerts := pa.AnalyzeMetrics(m)

	for _, a := range alerts {
		if a.Metric == "cache_hit_ratio" {
			t.Fatal("custom low threshold should suppress cache hit ratio alert at 60%")
		}
	}
}
