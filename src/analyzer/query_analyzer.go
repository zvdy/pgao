package analyzer

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/zvdy/pgao/src/models"
)

// QueryAnalyzer is responsible for analyzing SQL queries
type QueryAnalyzer struct {
	// Cache for parsed queries
	cache map[string]*models.QueryAnalysis
}

// NewQueryAnalyzer creates a new QueryAnalyzer instance
func NewQueryAnalyzer() *QueryAnalyzer {
	return &QueryAnalyzer{
		cache: make(map[string]*models.QueryAnalysis),
	}
}

// Analyze takes a SQL query as input and returns a comprehensive analysis
func (qa *QueryAnalyzer) Analyze(query string) (*models.QueryAnalysis, error) {
	// Create cache key
	cacheKey := qa.generateCacheKey(query)

	// Check cache
	if cached, exists := qa.cache[cacheKey]; exists {
		return cached, nil
	}

	analysis := models.NewQueryAnalysis(query)

	// Parse the SQL query
	parseResult, err := pg_query.Parse(query)
	if err != nil {
		return nil, fmt.Errorf("failed to parse query: %w", err)
	}

	// Get normalized query
	normalized, err := pg_query.Normalize(query)
	if err == nil {
		analysis.Normalized = normalized
	}

	// Analyze the parse tree
	if len(parseResult.Stmts) > 0 {
		qa.analyzeStatements(parseResult.Stmts, analysis)
	}

	// Fingerprint the query
	fingerprint, err := pg_query.Fingerprint(query)
	if err == nil {
		analysis.ParsedTree = map[string]interface{}{
			"fingerprint": fingerprint,
		}
	}

	// Determine complexity
	qa.calculateComplexity(analysis)

	// Generate optimization suggestions
	qa.generateSuggestions(analysis)

	// Cache the result
	qa.cache[cacheKey] = analysis

	return analysis, nil
}

// analyzeStatements processes parsed statements
func (qa *QueryAnalyzer) analyzeStatements(stmts []*pg_query.RawStmt, analysis *models.QueryAnalysis) {
	for _, stmt := range stmts {
		if stmt.Stmt == nil {
			continue
		}

		// Detect statement type
		switch node := stmt.Stmt.Node.(type) {
		case *pg_query.Node_SelectStmt:
			analysis.QueryType = "SELECT"
			qa.analyzeSelectStmt(node.SelectStmt, analysis)
		case *pg_query.Node_InsertStmt:
			analysis.QueryType = "INSERT"
			qa.analyzeInsertStmt(node.InsertStmt, analysis)
		case *pg_query.Node_UpdateStmt:
			analysis.QueryType = "UPDATE"
			qa.analyzeUpdateStmt(node.UpdateStmt, analysis)
		case *pg_query.Node_DeleteStmt:
			analysis.QueryType = "DELETE"
			qa.analyzeDeleteStmt(node.DeleteStmt, analysis)
		default:
			analysis.QueryType = "OTHER"
		}
	}
}

// analyzeSelectStmt analyzes SELECT statements
func (qa *QueryAnalyzer) analyzeSelectStmt(stmt *pg_query.SelectStmt, analysis *models.QueryAnalysis) {
	// Check for JOINs
	if len(stmt.FromClause) > 0 {
		qa.analyzeFromClause(stmt.FromClause, analysis)
	}

	// Check for subqueries
	if stmt.WithClause != nil {
		analysis.HasSubquery = true
	}

	// Check for aggregates
	if len(stmt.GroupClause) > 0 {
		analysis.HasAggregate = true
	}

	// Check for window functions
	if len(stmt.WindowClause) > 0 {
		analysis.HasWindowFunction = true
	}

	// Warn about SELECT *
	if qa.hasSelectAll(stmt) {
		analysis.AddWarning("SELECT * can be inefficient - consider specifying only needed columns")
	}
}

// analyzeFromClause analyzes FROM clause for tables and joins
func (qa *QueryAnalyzer) analyzeFromClause(fromClause []*pg_query.Node, analysis *models.QueryAnalysis) {
	for _, node := range fromClause {
		if node == nil {
			continue
		}

		switch from := node.Node.(type) {
		case *pg_query.Node_RangeVar:
			if from.RangeVar != nil && from.RangeVar.Relname != "" {
				analysis.Tables = append(analysis.Tables, from.RangeVar.Relname)
			}
		case *pg_query.Node_JoinExpr:
			analysis.HasJoin = true
			if from.JoinExpr != nil {
				qa.analyzeJoinExpr(from.JoinExpr, analysis)
			}
		}
	}
}

// analyzeJoinExpr analyzes JOIN expressions
func (qa *QueryAnalyzer) analyzeJoinExpr(join *pg_query.JoinExpr, analysis *models.QueryAnalysis) {
	switch join.Jointype {
	case pg_query.JoinType_JOIN_INNER:
		analysis.JoinType = "INNER"
	case pg_query.JoinType_JOIN_LEFT:
		analysis.JoinType = "LEFT"
	case pg_query.JoinType_JOIN_RIGHT:
		analysis.JoinType = "RIGHT"
	case pg_query.JoinType_JOIN_FULL:
		analysis.JoinType = "FULL"
		analysis.AddWarning("FULL OUTER JOIN can be expensive - verify it's necessary")
	}

	// Recursively analyze joined relations
	if join.Larg != nil {
		qa.analyzeFromClause([]*pg_query.Node{join.Larg}, analysis)
	}
	if join.Rarg != nil {
		qa.analyzeFromClause([]*pg_query.Node{join.Rarg}, analysis)
	}
}

// analyzeInsertStmt analyzes INSERT statements
func (qa *QueryAnalyzer) analyzeInsertStmt(stmt *pg_query.InsertStmt, analysis *models.QueryAnalysis) {
	if stmt.Relation != nil && stmt.Relation.Relname != "" {
		analysis.Tables = append(analysis.Tables, stmt.Relation.Relname)
	}
}

// analyzeUpdateStmt analyzes UPDATE statements
func (qa *QueryAnalyzer) analyzeUpdateStmt(stmt *pg_query.UpdateStmt, analysis *models.QueryAnalysis) {
	if stmt.Relation != nil && stmt.Relation.Relname != "" {
		analysis.Tables = append(analysis.Tables, stmt.Relation.Relname)
	}

	// Warn if no WHERE clause
	if stmt.WhereClause == nil {
		analysis.AddWarning("UPDATE without WHERE clause will affect all rows")
	}
}

// analyzeDeleteStmt analyzes DELETE statements
func (qa *QueryAnalyzer) analyzeDeleteStmt(stmt *pg_query.DeleteStmt, analysis *models.QueryAnalysis) {
	if stmt.Relation != nil && stmt.Relation.Relname != "" {
		analysis.Tables = append(analysis.Tables, stmt.Relation.Relname)
	}

	// Warn if no WHERE clause
	if stmt.WhereClause == nil {
		analysis.AddWarning("DELETE without WHERE clause will delete all rows")
	}
}

// hasSelectAll checks if the query uses SELECT *
func (qa *QueryAnalyzer) hasSelectAll(stmt *pg_query.SelectStmt) bool {
	if len(stmt.TargetList) == 0 {
		return false
	}

	for _, target := range stmt.TargetList {
		if target == nil {
			continue
		}
		if resTarget, ok := target.Node.(*pg_query.Node_ResTarget); ok {
			if resTarget.ResTarget != nil && resTarget.ResTarget.Val != nil {
				if columnRef, ok := resTarget.ResTarget.Val.Node.(*pg_query.Node_ColumnRef); ok {
					if columnRef.ColumnRef != nil && len(columnRef.ColumnRef.Fields) > 0 {
						if star, ok := columnRef.ColumnRef.Fields[0].Node.(*pg_query.Node_AStar); ok {
							if star.AStar != nil {
								return true
							}
						}
					}
				}
			}
		}
	}
	return false
}

// calculateComplexity determines query complexity
func (qa *QueryAnalyzer) calculateComplexity(analysis *models.QueryAnalysis) {
	score := 0

	if analysis.HasJoin {
		score += 2
	}
	if analysis.HasSubquery {
		score += 3
	}
	if analysis.HasAggregate {
		score += 1
	}
	if analysis.HasWindowFunction {
		score += 2
	}
	score += len(analysis.Tables)

	switch {
	case score <= 2:
		analysis.Complexity = "simple"
	case score <= 5:
		analysis.Complexity = "moderate"
	case score <= 8:
		analysis.Complexity = "complex"
	default:
		analysis.Complexity = "very_complex"
	}
}

// generateSuggestions generates optimization suggestions
func (qa *QueryAnalyzer) generateSuggestions(analysis *models.QueryAnalysis) {
	// Suggest indexes for tables
	if len(analysis.Tables) > 0 && !analysis.HasJoin {
		analysis.AddSuggestion(
			"index",
			"info",
			"Consider adding indexes on frequently queried columns",
			"Can significantly improve query performance",
			0.7,
		)
	}

	// Suggest for complex queries
	if analysis.Complexity == "very_complex" {
		analysis.AddSuggestion(
			"optimization",
			"medium",
			"Query is very complex - consider breaking it into smaller queries or using materialized views",
			"Can improve maintainability and performance",
			0.8,
		)
	}

	// Suggest for FULL OUTER JOIN
	if analysis.JoinType == "FULL" {
		analysis.AddSuggestion(
			"join",
			"high",
			"FULL OUTER JOIN detected - verify if LEFT or INNER JOIN would suffice",
			"Can significantly reduce query execution time",
			0.9,
		)
	}

	// Suggest for multiple joins
	if analysis.HasJoin && len(analysis.Tables) > 3 {
		analysis.AddSuggestion(
			"join",
			"medium",
			"Multiple table joins detected - ensure proper indexes exist on join columns",
			"Missing indexes on join columns can severely impact performance",
			0.85,
		)
	}

	// Suggest for subqueries
	if analysis.HasSubquery {
		analysis.AddSuggestion(
			"subquery",
			"medium",
			"Consider using JOINs instead of subqueries where possible",
			"JOINs are often more efficient than subqueries",
			0.7,
		)
	}
}

// generateCacheKey generates a cache key for the query
func (qa *QueryAnalyzer) generateCacheKey(query string) string {
	normalized := strings.TrimSpace(strings.ToLower(query))
	hash := md5.Sum([]byte(normalized))
	return hex.EncodeToString(hash[:])
}
