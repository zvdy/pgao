package collector

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v4"
)

// newMockPool returns a fresh pgxmock pool. Tests register expectations
// on it before calling the collector helpers; ExpectationsWereMet at
// the end asserts every queued response was consumed.
func newMockPool(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mp, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(func() {
		if err := mp.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet pgxmock expectations: %v", err)
		}
	})
	return mp
}

func TestCollectQueryMetrics_ReturnsRows(t *testing.T) {
	mp := newMockPool(t)

	rows := mp.NewRows([]string{
		"queryid", "query", "calls", "total_exec_time", "mean_exec_time",
		"stddev_exec_time", "rows", "shared_blks_hit", "shared_blks_read",
		"temp_blks_read", "temp_blks_written",
	}).
		AddRow("1", "SELECT 1", int64(10), float64(20), float64(2),
			float64(0.5), int64(10), int64(100), int64(0), int64(0), int64(0)).
		AddRow("2", "SELECT 2", int64(5), float64(50), float64(10),
			float64(1.0), int64(5), int64(50), int64(5), int64(0), int64(0))

	mp.ExpectQuery("SELECT(.|\n)+FROM pg_stat_statements").
		WithArgs(100).
		WillReturnRows(rows)

	got, err := collectQueryMetrics(context.Background(), mp, "c1", SlowQueryOptions{})
	if err != nil {
		t.Fatalf("collectQueryMetrics: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}
	if got[0].QueryID != "1" || got[0].CallCount != 10 || got[0].MeanExecTime != 2 {
		t.Errorf("row 0 wrong: %+v", got[0])
	}
	if got[0].ClusterID != "c1" {
		t.Errorf("expected ClusterID=c1, got %q", got[0].ClusterID)
	}
}

func TestCollectQueryMetrics_ClampsLimit(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero defaults to 100", 0, 100},
		{"negative defaults to 100", -5, 100},
		{"in range passes through", 50, 50},
		{"over 500 clamped", 99999, 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mp := newMockPool(t)
			mp.ExpectQuery("FROM pg_stat_statements").
				WithArgs(c.want).
				WillReturnRows(mp.NewRows([]string{
					"queryid", "query", "calls", "total_exec_time", "mean_exec_time",
					"stddev_exec_time", "rows", "shared_blks_hit", "shared_blks_read",
					"temp_blks_read", "temp_blks_written",
				}))
			if _, err := collectQueryMetrics(context.Background(), mp, "c", SlowQueryOptions{Limit: c.in}); err != nil {
				t.Fatalf("collectQueryMetrics: %v", err)
			}
		})
	}
}

func TestCollectQueryMetrics_OrderByAllowlist(t *testing.T) {
	cases := []struct {
		opt  string
		want string
	}{
		{"mean_exec_time", "ORDER BY mean_exec_time"},
		{"total_exec_time", "ORDER BY total_exec_time"},
		{"calls", "ORDER BY calls"},
		{"injection; DROP TABLE foo", "ORDER BY mean_exec_time"}, // unknown → default
		{"", "ORDER BY mean_exec_time"},
	}
	for _, c := range cases {
		t.Run(c.opt, func(t *testing.T) {
			mp := newMockPool(t)
			mp.ExpectQuery(regexpEscape(c.want)).
				WithArgs(100).
				WillReturnRows(mp.NewRows([]string{
					"queryid", "query", "calls", "total_exec_time", "mean_exec_time",
					"stddev_exec_time", "rows", "shared_blks_hit", "shared_blks_read",
					"temp_blks_read", "temp_blks_written",
				}))
			_, err := collectQueryMetrics(context.Background(), mp, "c", SlowQueryOptions{OrderBy: c.opt})
			if err != nil {
				t.Fatalf("collectQueryMetrics: %v", err)
			}
		})
	}
}

func TestCollectQueryMetrics_MissingExtension(t *testing.T) {
	mp := newMockPool(t)
	mp.ExpectQuery("FROM pg_stat_statements").
		WithArgs(100).
		WillReturnError(&pgconn.PgError{Code: "42P01", Message: "relation \"pg_stat_statements\" does not exist"})

	_, err := collectQueryMetrics(context.Background(), mp, "c", SlowQueryOptions{})
	if !errors.Is(err, ErrPgStatStatementsMissing) {
		t.Errorf("expected ErrPgStatStatementsMissing, got %v", err)
	}
}

func TestCollectQueryMetrics_OtherErrorIsWrapped(t *testing.T) {
	mp := newMockPool(t)
	mp.ExpectQuery("FROM pg_stat_statements").
		WithArgs(100).
		WillReturnError(errors.New("connection refused"))

	_, err := collectQueryMetrics(context.Background(), mp, "c", SlowQueryOptions{})
	if err == nil || errors.Is(err, ErrPgStatStatementsMissing) {
		t.Errorf("expected wrapped error, got %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected wrapped error to surface cause, got %v", err)
	}
}

func TestCollectQueryMetrics_PassesDatabaseLabel(t *testing.T) {
	mp := newMockPool(t)
	mp.ExpectQuery("FROM pg_stat_statements").
		WithArgs(100).
		WillReturnRows(mp.NewRows([]string{
			"queryid", "query", "calls", "total_exec_time", "mean_exec_time",
			"stddev_exec_time", "rows", "shared_blks_hit", "shared_blks_read",
			"temp_blks_read", "temp_blks_written",
		}).
			AddRow("1", "SELECT 1", int64(1), float64(1), float64(1),
				float64(0), int64(1), int64(0), int64(0), int64(0), int64(0)))

	got, err := collectQueryMetrics(context.Background(), mp, "c1", SlowQueryOptions{Database: "billing"})
	if err != nil {
		t.Fatalf("collectQueryMetrics: %v", err)
	}
	if got[0].Database != "billing" {
		t.Errorf("Database label not propagated; got %q", got[0].Database)
	}
}

func TestCollectTableMetrics_ReturnsRows(t *testing.T) {
	mp := newMockPool(t)
	cols := []string{
		"schemaname", "relname", "seq_scan", "seq_tup_read", "idx_scan",
		"idx_tup_fetch", "n_tup_ins", "n_tup_upd", "n_tup_del", "n_tup_hot_upd",
		"n_live_tup", "n_dead_tup", "vacuum_count", "autovacuum_count",
		"analyze_count", "last_vacuum", "last_autovacuum", "last_analyze",
	}
	rows := mp.NewRows(cols).
		AddRow("public", "users", int64(10), int64(100), int64(5), int64(50),
			int64(1), int64(2), int64(3), int64(0), int64(100), int64(2),
			int64(1), int64(1), int64(1), nil, nil, nil)

	mp.ExpectQuery("FROM pg_stat_user_tables").
		WithArgs(100).
		WillReturnRows(rows)

	got, err := collectTableMetrics(context.Background(), mp, "c1", TableMetricsOptions{})
	if err != nil {
		t.Fatalf("collectTableMetrics: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	if got[0].Schema != "public" || got[0].Table != "users" {
		t.Errorf("schema/table wrong: %+v", got[0])
	}
	if got[0].SeqScan != 10 || got[0].DeadTuples != 2 {
		t.Errorf("counter row wrong: %+v", got[0])
	}
}

func TestCollectTableMetrics_QueryError(t *testing.T) {
	mp := newMockPool(t)
	mp.ExpectQuery("FROM pg_stat_user_tables").
		WithArgs(100).
		WillReturnError(errors.New("connection refused"))

	_, err := collectTableMetrics(context.Background(), mp, "c", TableMetricsOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pg_stat_user_tables") {
		t.Errorf("expected wrapped error context, got %v", err)
	}
}

// regexpEscape escapes a literal SQL fragment for use in pgxmock's
// regex-style ExpectQuery matcher.
func regexpEscape(s string) string {
	// pgxmock uses regexp.MatchString; the SQL fragments we test against
	// only contain alphanumerics + spaces + underscores so a simple
	// replace of regex meta-characters is enough.
	r := strings.NewReplacer(".", "\\.", "(", "\\(", ")", "\\)", "+", "\\+")
	return r.Replace(s)
}
