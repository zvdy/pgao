package models

import "time"

// AlertSeverity represents the severity level of an alert
type AlertSeverity string

const (
	AlertSeverityCritical AlertSeverity = "critical"
	AlertSeverityHigh     AlertSeverity = "high"
	AlertSeverityMedium   AlertSeverity = "medium"
	AlertSeverityLow      AlertSeverity = "low"
	AlertSeverityInfo     AlertSeverity = "info"
)

// AlertType represents the type of alert
type AlertType string

const (
	AlertTypePerformance   AlertType = "performance"
	AlertTypeAvailability  AlertType = "availability"
	AlertTypeCapacity      AlertType = "capacity"
	AlertTypeSecurity      AlertType = "security"
	AlertTypeConfiguration AlertType = "configuration"
	AlertTypeReplication   AlertType = "replication"
	AlertTypeConnection    AlertType = "connection"
	AlertTypeQuery         AlertType = "query"
)

// Alert represents a system alert
type Alert struct {
	ID             string                 `json:"id"`
	Type           AlertType              `json:"type"`
	Severity       AlertSeverity          `json:"severity"`
	ClusterID      string                 `json:"cluster_id"`
	Title          string                 `json:"title"`
	Description    string                 `json:"description"`
	Metric         string                 `json:"metric"`
	Threshold      float64                `json:"threshold"`
	CurrentValue   float64                `json:"current_value"`
	Timestamp      time.Time              `json:"timestamp"`
	Status         string                 `json:"status"` // active, acknowledged, resolved
	AcknowledgedAt *time.Time             `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string                 `json:"acknowledged_by,omitempty"`
	ResolvedAt     *time.Time             `json:"resolved_at,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	Actions        []string               `json:"actions,omitempty"`
}

// NewAlert creates a new Alert instance
func NewAlert(alertType AlertType, severity AlertSeverity, clusterID, title, description string) *Alert {
	return &Alert{
		Type:        alertType,
		Severity:    severity,
		ClusterID:   clusterID,
		Title:       title,
		Description: description,
		Timestamp:   time.Now(),
		Status:      "active",
		Metadata:    make(map[string]interface{}),
		Actions:     make([]string, 0),
	}
}

// Acknowledge marks the alert as acknowledged
func (a *Alert) Acknowledge(by string) {
	now := time.Now()
	a.Status = "acknowledged"
	a.AcknowledgedAt = &now
	a.AcknowledgedBy = by
}

// Resolve marks the alert as resolved
func (a *Alert) Resolve() {
	now := time.Now()
	a.Status = "resolved"
	a.ResolvedAt = &now
}

// AddAction adds a recommended action to the alert
func (a *Alert) AddAction(action string) {
	a.Actions = append(a.Actions, action)
}

// HealthStatus represents the overall health status of a cluster
type HealthStatus struct {
	ClusterID      string        `json:"cluster_id"`
	Status         string        `json:"status"` // healthy, warning, critical, unknown
	Score          int           `json:"score"`  // 0-100
	ActiveAlerts   int           `json:"active_alerts"`
	CriticalAlerts int           `json:"critical_alerts"`
	LastCheck      time.Time     `json:"last_check"`
	Checks         []HealthCheck `json:"checks"`
}

// HealthCheck represents an individual health check
type HealthCheck struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Message     string    `json:"message"`
	LastChecked time.Time `json:"last_checked"`
	Value       float64   `json:"value,omitempty"`
}

// NewHealthStatus creates a new HealthStatus instance
func NewHealthStatus(clusterID string) *HealthStatus {
	return &HealthStatus{
		ClusterID: clusterID,
		Status:    "unknown",
		Score:     0,
		LastCheck: time.Now(),
		Checks:    make([]HealthCheck, 0),
	}
}

// AddCheck adds a health check to the status
func (hs *HealthStatus) AddCheck(check HealthCheck) {
	hs.Checks = append(hs.Checks, check)
	hs.calculateScore()
}

// calculateScore calculates the overall health score
func (hs *HealthStatus) calculateScore() {
	if len(hs.Checks) == 0 {
		hs.Score = 0
		hs.Status = "unknown"
		return
	}

	passedChecks := 0
	for _, check := range hs.Checks {
		if check.Status == "ok" || check.Status == "healthy" {
			passedChecks++
		}
	}

	hs.Score = (passedChecks * 100) / len(hs.Checks)

	switch {
	case hs.Score >= 90:
		hs.Status = "healthy"
	case hs.Score >= 70:
		hs.Status = "warning"
	case hs.Score >= 50:
		hs.Status = "degraded"
	default:
		hs.Status = "critical"
	}
}
