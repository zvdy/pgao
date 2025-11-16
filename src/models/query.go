package models

import "time"

// QueryAnalysis represents the result of analyzing a SQL query
type QueryAnalysis struct {
	Query             string                 `json:"query"`
	Normalized        string                 `json:"normalized"`
	ParsedTree        map[string]interface{} `json:"parsed_tree,omitempty"`
	QueryType         string                 `json:"query_type"`
	Tables            []string               `json:"tables"`
	Indexes           []string               `json:"indexes_used"`
	Columns           []string               `json:"columns"`
	HasSubquery       bool                   `json:"has_subquery"`
	HasJoin           bool                   `json:"has_join"`
	JoinType          string                 `json:"join_type,omitempty"`
	HasAggregate      bool                   `json:"has_aggregate"`
	HasWindowFunction bool                   `json:"has_window_function"`
	Complexity        string                 `json:"complexity"`
	EstimatedCost     float64                `json:"estimated_cost"`
	Suggestions       []QuerySuggestion      `json:"suggestions"`
	Warnings          []string               `json:"warnings"`
	Timestamp         time.Time              `json:"timestamp"`
}

// QuerySuggestion represents an optimization suggestion
type QuerySuggestion struct {
	Type        string  `json:"type"`
	Severity    string  `json:"severity"`
	Message     string  `json:"message"`
	Impact      string  `json:"impact"`
	Confidence  float64 `json:"confidence"`
	Recommended string  `json:"recommended,omitempty"`
}

// NewQueryAnalysis creates a new QueryAnalysis instance
func NewQueryAnalysis(query string) *QueryAnalysis {
	return &QueryAnalysis{
		Query:       query,
		Suggestions: make([]QuerySuggestion, 0),
		Warnings:    make([]string, 0),
		Tables:      make([]string, 0),
		Indexes:     make([]string, 0),
		Columns:     make([]string, 0),
		Timestamp:   time.Now(),
	}
}

// AddSuggestion adds an optimization suggestion
func (qa *QueryAnalysis) AddSuggestion(suggType, severity, message, impact string, confidence float64) {
	qa.Suggestions = append(qa.Suggestions, QuerySuggestion{
		Type:       suggType,
		Severity:   severity,
		Message:    message,
		Impact:     impact,
		Confidence: confidence,
	})
}

// AddWarning adds a warning to the analysis
func (qa *QueryAnalysis) AddWarning(warning string) {
	qa.Warnings = append(qa.Warnings, warning)
}

// ExplainPlan represents a PostgreSQL EXPLAIN plan
type ExplainPlan struct {
	QueryID           string                 `json:"query_id"`
	Query             string                 `json:"query"`
	Plan              map[string]interface{} `json:"plan"`
	TotalCost         float64                `json:"total_cost"`
	PlanningTime      float64                `json:"planning_time_ms"`
	ExecutionTime     float64                `json:"execution_time_ms"`
	ActualRows        int64                  `json:"actual_rows"`
	PlannedRows       int64                  `json:"planned_rows"`
	NodeType          string                 `json:"node_type"`
	SequentialScans   int                    `json:"sequential_scans"`
	IndexScans        int                    `json:"index_scans"`
	BuffersSharedHit  int64                  `json:"buffers_shared_hit"`
	BuffersSharedRead int64                  `json:"buffers_shared_read"`
	Timestamp         time.Time              `json:"timestamp"`
}

// NewExplainPlan creates a new ExplainPlan instance
func NewExplainPlan(queryID, query string) *ExplainPlan {
	return &ExplainPlan{
		QueryID:   queryID,
		Query:     query,
		Timestamp: time.Now(),
	}
}

// SlowQuery represents a slow query that needs attention
type SlowQuery struct {
	QueryID     string         `json:"query_id"`
	Query       string         `json:"query"`
	ClusterID   string         `json:"cluster_id"`
	Database    string         `json:"database"`
	User        string         `json:"user"`
	Duration    float64        `json:"duration_ms"`
	Timestamp   time.Time      `json:"timestamp"`
	Frequency   int            `json:"frequency"`
	AvgDuration float64        `json:"avg_duration_ms"`
	MaxDuration float64        `json:"max_duration_ms"`
	Analysis    *QueryAnalysis `json:"analysis,omitempty"`
	ExplainPlan *ExplainPlan   `json:"explain_plan,omitempty"`
}

// NewSlowQuery creates a new SlowQuery instance
func NewSlowQuery(queryID, query, clusterID, database, user string, duration float64) *SlowQuery {
	return &SlowQuery{
		QueryID:   queryID,
		Query:     query,
		ClusterID: clusterID,
		Database:  database,
		User:      user,
		Duration:  duration,
		Timestamp: time.Now(),
		Frequency: 1,
	}
}
