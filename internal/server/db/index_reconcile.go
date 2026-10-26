package db

import (
	"context"
	"database/sql"

	"entgo.io/ent/dialect"

	"github.com/looplj/axonhub/internal/log"
)

// The dashboard status aggregation is backed by an index on requests.status.
// Early 0.9.43 deployments created it by hand as "idx_requests_status"; the ent
// schema declares it as "requests_by_status".
const (
	legacyRequestStatusIndex    = "idx_requests_status"
	canonicalRequestStatusIndex = "requests_by_status"
)

// reconcileLegacyIndexes renames hand-created indexes to the names declared in the
// ent schema.
//
// It must run after the connection is ready and BEFORE client.Schema.Create: the
// automatic schema migration only skips an index when one with the same name
// already exists. Renaming the legacy index first lets the migration reuse it;
// otherwise it would build a duplicate index, and a plain CREATE INDEX blocks
// writes for the whole build on large tables.
//
// The reconciliation is best effort: any failure is logged and startup continues,
// because the schema migration can still create the index on its own.
func reconcileLegacyIndexes(ctx context.Context, sqlDB *sql.DB, dbDialect string) {
	if dbDialect != dialect.Postgres {
		// The legacy index only exists on the PostgreSQL instances that were
		// patched by hand; other dialects have nothing to reconcile.
		return
	}

	rename := `
DO $$
BEGIN
	IF to_regclass('public.` + legacyRequestStatusIndex + `') IS NOT NULL
		AND to_regclass('public.` + canonicalRequestStatusIndex + `') IS NULL THEN
		ALTER INDEX public.` + legacyRequestStatusIndex + ` RENAME TO ` + canonicalRequestStatusIndex + `;
	END IF;
END $$;`

	if _, err := sqlDB.ExecContext(ctx, rename); err != nil {
		log.Warn(ctx, "failed to rename legacy request status index, continuing",
			log.String("legacy_index", legacyRequestStatusIndex), log.Cause(err))

		return
	}

	// When the canonical index is already in place, drop the redundant legacy one.
	if _, err := sqlDB.ExecContext(ctx, "DROP INDEX IF EXISTS public."+legacyRequestStatusIndex); err != nil {
		log.Warn(ctx, "failed to drop redundant legacy request status index, continuing",
			log.String("legacy_index", legacyRequestStatusIndex), log.Cause(err))
	}
}
