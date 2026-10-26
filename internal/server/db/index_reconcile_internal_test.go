package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"entgo.io/ent/dialect"

	_ "github.com/looplj/axonhub/internal/pkg/sqlite"
)

// reconcileLegacyIndexes is only meaningful on PostgreSQL, where a hand-created
// index may exist. This test pins the non-PostgreSQL path: it must be a silent
// no-op that never fails startup.
func TestReconcileLegacyIndexes_SkipsNonPostgres(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "reconcile.db") + "?_fk=0"

	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()

	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping sqlite: %v", err)
	}

	reconcileLegacyIndexes(context.Background(), sqlDB, dialect.SQLite)
}
