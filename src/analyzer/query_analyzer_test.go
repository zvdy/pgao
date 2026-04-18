package analyzer

import (
	"strings"
	"sync"
	"testing"
)

func TestAnalyze_SelectStar(t *testing.T) {
	qa := NewQueryAnalyzer()

	got, err := qa.Analyze("SELECT * FROM users WHERE id = 1")
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	if got.QueryType != "SELECT" {
		t.Errorf("expected QueryType SELECT, got %s", got.QueryType)
	}
	if len(got.Tables) == 0 || got.Tables[0] != "users" {
		t.Errorf("expected table 'users', got %+v", got.Tables)
	}

	foundStarWarning := false
	for _, w := range got.Warnings {
		if strings.Contains(w, "SELECT *") {
			foundStarWarning = true
		}
	}
	if !foundStarWarning {
		t.Errorf("expected SELECT * warning, got warnings=%+v", got.Warnings)
	}
}

func TestAnalyze_UpdateWithoutWhereWarns(t *testing.T) {
	qa := NewQueryAnalyzer()

	a, err := qa.Analyze("UPDATE users SET name = 'x'")
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if a.QueryType != "UPDATE" {
		t.Errorf("expected UPDATE, got %s", a.QueryType)
	}

	found := false
	for _, w := range a.Warnings {
		if strings.Contains(w, "UPDATE without WHERE") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about UPDATE without WHERE, got %+v", a.Warnings)
	}
}

func TestAnalyze_DeleteWithoutWhereWarns(t *testing.T) {
	qa := NewQueryAnalyzer()

	a, err := qa.Analyze("DELETE FROM users")
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	found := false
	for _, w := range a.Warnings {
		if strings.Contains(w, "DELETE without WHERE") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about DELETE without WHERE, got %+v", a.Warnings)
	}
}

func TestAnalyze_JoinDetection(t *testing.T) {
	qa := NewQueryAnalyzer()

	a, err := qa.Analyze("SELECT u.id, o.total FROM users u JOIN orders o ON u.id = o.user_id")
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !a.HasJoin {
		t.Error("expected HasJoin=true")
	}
	if a.JoinType != "INNER" {
		t.Errorf("expected INNER join, got %s", a.JoinType)
	}
}

func TestAnalyze_FullOuterJoinSuggestion(t *testing.T) {
	qa := NewQueryAnalyzer()

	a, err := qa.Analyze("SELECT * FROM a FULL OUTER JOIN b ON a.id = b.id")
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if a.JoinType != "FULL" {
		t.Errorf("expected FULL join, got %s", a.JoinType)
	}

	found := false
	for _, s := range a.Suggestions {
		if s.Type == "join" && strings.Contains(s.Message, "FULL OUTER") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected FULL OUTER JOIN suggestion, got %+v", a.Suggestions)
	}
}

func TestAnalyze_ComplexityClassification(t *testing.T) {
	qa := NewQueryAnalyzer()

	simple, _ := qa.Analyze("SELECT id FROM users WHERE id = 1")
	if simple.Complexity != "simple" {
		t.Errorf("expected simple, got %s", simple.Complexity)
	}

	complex, _ := qa.Analyze(`
		WITH recent AS (SELECT user_id, sum(total) OVER (PARTITION BY user_id) FROM orders)
		SELECT u.id, r.sum FROM users u
		JOIN recent r ON r.user_id = u.id
		JOIN addresses a ON a.user_id = u.id
		JOIN payments p ON p.user_id = u.id
		GROUP BY u.id, r.sum
	`)
	if complex.Complexity == "simple" {
		t.Errorf("complex query misclassified: got %s", complex.Complexity)
	}
}

func TestAnalyze_InvalidSQLReturnsError(t *testing.T) {
	qa := NewQueryAnalyzer()
	if _, err := qa.Analyze("SELEKT * FROM"); err == nil {
		t.Error("expected error on invalid SQL, got nil")
	}
}

func TestAnalyze_CacheHits(t *testing.T) {
	qa := NewQueryAnalyzer()

	first, err := qa.Analyze("SELECT id FROM users")
	if err != nil {
		t.Fatalf("first analyze error: %v", err)
	}
	second, err := qa.Analyze("SELECT id FROM users")
	if err != nil {
		t.Fatalf("second analyze error: %v", err)
	}
	if first != second {
		t.Error("expected cached pointer to be returned on second Analyze call")
	}
}

// TestAnalyze_ConcurrentSafe guards the fix for issue #2: the cache must not
// panic under concurrent read/write. This test currently races on the
// underlying map and will expose the issue under `go test -race`.
// Keeping it in a build tag so we can run it once the fix lands.
func TestAnalyze_ConcurrentSafe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent cache test in short mode")
	}
	qa := NewQueryAnalyzer()
	queries := []string{
		"SELECT 1",
		"SELECT id FROM users",
		"SELECT * FROM orders WHERE total > 100",
		"UPDATE users SET name='x' WHERE id=1",
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := qa.Analyze(queries[i%len(queries)]); err != nil {
				t.Errorf("concurrent Analyze error: %v", err)
			}
		}(i)
	}
	wg.Wait()
}
