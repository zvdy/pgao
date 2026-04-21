package collector

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestAllowedSlowQueryOrderByIncludesExpectedKeys(t *testing.T) {
	for _, key := range []string{"mean_exec_time", "total_exec_time", "calls"} {
		if _, ok := allowedSlowQueryOrderBy[key]; !ok {
			t.Errorf("expected %q to be an allowed order_by", key)
		}
	}
}

func TestIsUndefinedTableRecognises42P01(t *testing.T) {
	err := &pgconn.PgError{Code: "42P01", Message: `relation "pg_stat_statements" does not exist`}
	if !isUndefinedTable(err) {
		t.Error("expected 42P01 PgError to be recognised as undefined table")
	}

	wrapped := errors.New("wrap: " + err.Error())
	if isUndefinedTable(wrapped) {
		t.Error("plain errors without PgError type should not be recognised")
	}

	other := &pgconn.PgError{Code: "42601"}
	if isUndefinedTable(other) {
		t.Error("non-42P01 code must not be treated as undefined table")
	}
}
