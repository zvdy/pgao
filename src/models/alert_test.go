package models

import (
	"testing"
)

func TestAlertLifecycle(t *testing.T) {
	a := NewAlert(AlertTypePerformance, AlertSeverityHigh, "c1", "CPU High", "CPU at 95%")

	if a.Status != "active" {
		t.Errorf("new alert should be active, got %q", a.Status)
	}
	if a.AcknowledgedAt != nil {
		t.Error("new alert should not have acknowledged_at set")
	}

	a.AddAction("scale up")
	if len(a.Actions) != 1 || a.Actions[0] != "scale up" {
		t.Errorf("AddAction failed, actions=%+v", a.Actions)
	}

	a.Acknowledge("ops")
	if a.Status != "acknowledged" || a.AcknowledgedBy != "ops" || a.AcknowledgedAt == nil {
		t.Errorf("Acknowledge did not update alert correctly: %+v", a)
	}

	a.Resolve()
	if a.Status != "resolved" || a.ResolvedAt == nil {
		t.Errorf("Resolve did not mark alert resolved: %+v", a)
	}
}

func TestHealthStatus_ScoreAndStatus(t *testing.T) {
	hs := NewHealthStatus("c1")
	if hs.Status != "unknown" || hs.Score != 0 {
		t.Errorf("initial HealthStatus wrong: %+v", hs)
	}

	hs.AddCheck(HealthCheck{Name: "db", Status: "ok"})
	hs.AddCheck(HealthCheck{Name: "cache", Status: "ok"})
	hs.AddCheck(HealthCheck{Name: "mem", Status: "ok"})
	hs.AddCheck(HealthCheck{Name: "disk", Status: "ok"})

	if hs.Score != 100 {
		t.Errorf("all-ok score should be 100, got %d", hs.Score)
	}
	if hs.Status != "healthy" {
		t.Errorf("all-ok status should be healthy, got %s", hs.Status)
	}

	hs.AddCheck(HealthCheck{Name: "cpu", Status: "warning"})
	if hs.Score >= 100 {
		t.Errorf("score should drop after failing check, got %d", hs.Score)
	}

	for i := 0; i < 10; i++ {
		hs.AddCheck(HealthCheck{Name: "x", Status: "warning"})
	}
	if hs.Status != "critical" {
		t.Errorf("many failing checks should be critical, got %s", hs.Status)
	}
}
