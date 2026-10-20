package db_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/looplj/axonhub/internal/ent/enttest"

	// Register the "sqlite3" driver for the verification connection below.
	_ "github.com/looplj/axonhub/internal/pkg/sqlite"
)

// TestRequestsStatusIndexIsCreated guards the dashboard performance fix: the
// status aggregation over the whole requests table must be backed by an index,
// otherwise it falls back to a full heap scan on large installations.
func TestRequestsStatusIndexIsCreated(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "requests_status_index.db") + "?_fk=0"

	client := enttest.NewEntClient(t, "sqlite3", dsn)
	client.Close()

	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()

	var name string
	if err := sqlDB.QueryRow(
		"SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'requests_by_status'").
		Scan(&name); err != nil {
		t.Fatalf("expected index requests_by_status to exist after schema migration: %v", err)
	}
}
