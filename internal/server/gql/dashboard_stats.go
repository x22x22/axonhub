package gql

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/log"
)

const (
	// statsViewsRecheckInterval bounds how often the probe runs. The views are
	// created by an operator (see the migration SQL), so a long-lived process has
	// to notice them appearing without a restart, but probing on every request
	// would be silly.
	statsViewsRecheckInterval = time.Minute

	statsViewsProbeQuery = `SELECT count(*) FROM pg_matviews WHERE matviewname IN ('usage_stats_daily','request_stats_daily','execution_stats_daily')`
)

// errStatsDatabaseUnavailable guards the (unlikely) case of a non-entsql driver.
var errStatsDatabaseUnavailable = errors.New("stats database unavailable")

var (
	statsViewsMu        sync.Mutex
	statsViewsCheckedAt time.Time
	statsViewsPresent   bool
)

// statsDatabase exposes the underlying *sql.DB so the dashboard resolvers can read
// the materialized views, which ent does not model.
func (r *queryResolver) statsDatabase() (*sql.DB, bool) {
	driver, ok := r.client.Driver().(*entsql.Driver)
	if !ok {
		return nil, false
	}

	return driver.DB(), true
}

// dashboardStatsViewsAvailable reports whether all three stats views exist.
//
// A fresh install that has not run the migration SQL still needs a working
// dashboard, so every caller falls back to the detail tables when this is false.
func (r *queryResolver) dashboardStatsViewsAvailable(ctx context.Context) bool {
	statsViewsMu.Lock()
	defer statsViewsMu.Unlock()

	if time.Since(statsViewsCheckedAt) < statsViewsRecheckInterval {
		return statsViewsPresent
	}

	db, ok := r.statsDatabase()
	if !ok {
		return false
	}

	var count int
	err := db.QueryRowContext(ctx, statsViewsProbeQuery).Scan(&count)

	statsViewsCheckedAt = time.Now()
	statsViewsPresent = err == nil && count == 3

	if err != nil {
		log.Warn(ctx, "Failed to probe dashboard stats views, falling back to detail tables", log.Cause(err))
	}

	return statsViewsPresent
}

// channelExecutionStats is the aggregated per-channel execution counters.
type channelExecutionStats struct {
	ChannelID    int `json:"channel_id"`
	SuccessCount int `json:"success_count"`
	FailedCount  int `json:"failed_count"`
}

// channelSuccessRatesFromViews reads channel execution counters from the daily
// aggregate plus today's detail rows.
//
// applyFilter=false means "all time", which is passed as a NULL lower bound so the
// same SQL serves both cases.
func (r *queryResolver) channelSuccessRatesFromViews(ctx context.Context, since time.Time, applyFilter bool) ([]channelExecutionStats, error) {
	db, ok := r.statsDatabase()
	if !ok {
		return nil, errStatsDatabaseUnavailable
	}

	query := `
SELECT channel_id,
       COALESCE(SUM(success_count), 0)::bigint AS success_count,
       COALESCE(SUM(failed_count), 0)::bigint  AS failed_count
FROM (
    SELECT channel_id, success_count, failed_count
    FROM execution_stats_daily
    WHERE day < current_date
      AND channel_id <> 0
      AND ($1::timestamptz IS NULL OR day >= ($1::timestamptz)::date)
    UNION ALL
    SELECT channel_id,
           COUNT(*) FILTER (WHERE status = 'completed')::bigint,
           COUNT(*) FILTER (WHERE status = 'failed')::bigint
    FROM request_executions
    WHERE channel_id IS NOT NULL
      AND created_at >= current_date
      AND ($1::timestamptz IS NULL OR created_at >= $1::timestamptz)
    GROUP BY channel_id
) x
GROUP BY channel_id`

	var arg any
	if applyFilter {
		arg = since
	}

	rows, err := db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]channelExecutionStats, 0, 16)

	for rows.Next() {
		var stat channelExecutionStats
		if scanErr := rows.Scan(&stat.ChannelID, &stat.SuccessCount, &stat.FailedCount); scanErr != nil {
			return nil, scanErr
		}

		results = append(results, stat)
	}

	return results, rows.Err()
}
