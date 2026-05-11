package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCollectSettings_ParsesUnitFormatting(t *testing.T) {
	mp := newMockPool(t)
	mp.ExpectQuery("FROM pg_settings").
		WillReturnRows(mp.NewRows([]string{"name", "setting", "unit"}).
			AddRow("max_connections", "100", "").    // no unit → bare
			AddRow("shared_buffers", "8192", "8kB"). // unit suffixed
			AddRow("work_mem", "4096", "kB"))

	got, err := collectSettings(context.Background(), mp)
	if err != nil {
		t.Fatalf("collectSettings: %v", err)
	}
	if got["max_connections"] != "100" {
		t.Errorf("max_connections=%q, want \"100\"", got["max_connections"])
	}
	if got["shared_buffers"] != "81928kB" {
		t.Errorf("shared_buffers=%q, want \"81928kB\"", got["shared_buffers"])
	}
	if got["work_mem"] != "4096kB" {
		t.Errorf("work_mem=%q, want \"4096kB\"", got["work_mem"])
	}
}

func TestCollectSettings_QueryError(t *testing.T) {
	mp := newMockPool(t)
	mp.ExpectQuery("FROM pg_settings").
		WillReturnError(errors.New("permission denied for view pg_settings"))

	_, err := collectSettings(context.Background(), mp)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected error to wrap cause, got %v", err)
	}
}

func TestCollectDatabases_ReturnsSortedSlice(t *testing.T) {
	mp := newMockPool(t)
	mp.ExpectQuery("FROM pg_database").
		WillReturnRows(mp.NewRows([]string{"datname"}).
			AddRow("billing").
			AddRow("inventory").
			AddRow("postgres"))

	got, err := collectDatabases(context.Background(), mp)
	if err != nil {
		t.Fatalf("collectDatabases: %v", err)
	}
	if len(got) != 3 || got[0] != "billing" || got[2] != "postgres" {
		t.Errorf("unexpected databases: %+v", got)
	}
}

func TestCollectDatabases_ScanError(t *testing.T) {
	mp := newMockPool(t)
	// Wrong column count: scan will fail mid-iteration.
	mp.ExpectQuery("FROM pg_database").
		WillReturnRows(mp.NewRows([]string{"datname", "extra"}).
			AddRow("postgres", "x"))

	_, err := collectDatabases(context.Background(), mp)
	if err == nil || !strings.Contains(err.Error(), "scan database") {
		t.Errorf("expected scan database error, got %v", err)
	}
}

func TestCollectExtensions_OrdersByName(t *testing.T) {
	mp := newMockPool(t)
	mp.ExpectQuery("FROM pg_extension").
		WillReturnRows(mp.NewRows([]string{"extname"}).
			AddRow("pg_stat_statements").
			AddRow("pgcrypto").
			AddRow("plpgsql"))

	got, err := collectExtensions(context.Background(), mp)
	if err != nil {
		t.Fatalf("collectExtensions: %v", err)
	}
	if len(got) != 3 || got[0] != "pg_stat_statements" {
		t.Errorf("unexpected extensions: %+v", got)
	}
}

func TestCollectExtensions_EmptyResult(t *testing.T) {
	mp := newMockPool(t)
	mp.ExpectQuery("FROM pg_extension").
		WillReturnRows(mp.NewRows([]string{"extname"}))

	got, err := collectExtensions(context.Background(), mp)
	if err != nil {
		t.Fatalf("collectExtensions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %+v", got)
	}
}
