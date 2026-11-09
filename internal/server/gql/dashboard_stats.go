package gql

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent/request"
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
    WHERE day < (now() AT TIME ZONE 'UTC')::date
      AND channel_id <> 0
      AND ($1::timestamptz IS NULL OR day >= ($1::timestamptz)::date)
    UNION ALL
    SELECT channel_id,
           COUNT(*) FILTER (WHERE status = 'completed')::bigint,
           COUNT(*) FILTER (WHERE status = 'failed')::bigint
    FROM request_executions
    WHERE channel_id IS NOT NULL
      AND created_at >= (now() AT TIME ZONE 'UTC')::date
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

// apiKeyStats is the per-API-key request counter.
type apiKeyStats struct {
	APIKeyID int `json:"api_key_id"`
	Count    int `json:"request_count"`
}

// apiKeyCostStats is the per-API-key cost sum.
type apiKeyCostStats struct {
	APIKeyID int     `json:"api_key_id"`
	Cost     float64 `json:"total_cost"`
}

func (r *queryResolver) requestStatsByAPIKeyFromViews(ctx context.Context, since time.Time, applyFilter bool) ([]apiKeyStats, error) {
	db, ok := r.statsDatabase()
	if !ok {
		return nil, errStatsDatabaseUnavailable
	}

	query := `
SELECT api_key_id, COALESCE(SUM(request_count), 0)::bigint AS request_count
FROM (
    SELECT api_key_id, request_count
    FROM usage_stats_daily
    WHERE day < (now() AT TIME ZONE 'UTC')::date AND api_key_id <> 0
      AND ($1::timestamptz IS NULL OR day >= ($1::timestamptz)::date)
    UNION ALL
    SELECT api_key_id, COUNT(*)::bigint
    FROM usage_logs
    WHERE api_key_id IS NOT NULL AND created_at >= (now() AT TIME ZONE 'UTC')::date
      AND ($1::timestamptz IS NULL OR created_at >= $1::timestamptz)
    GROUP BY api_key_id
) x
GROUP BY api_key_id`

	var arg any
	if applyFilter {
		arg = since
	}

	rows, err := db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]apiKeyStats, 0, 16)

	for rows.Next() {
		var stat apiKeyStats
		if scanErr := rows.Scan(&stat.APIKeyID, &stat.Count); scanErr != nil {
			return nil, scanErr
		}

		results = append(results, stat)
	}

	return results, rows.Err()
}

func (r *queryResolver) costStatsByAPIKeyFromViews(ctx context.Context, since time.Time, applyFilter bool) ([]apiKeyCostStats, error) {
	db, ok := r.statsDatabase()
	if !ok {
		return nil, errStatsDatabaseUnavailable
	}

	query := `
SELECT api_key_id, COALESCE(SUM(total_cost), 0) AS total_cost
FROM (
    SELECT api_key_id, total_cost
    FROM usage_stats_daily
    WHERE day < (now() AT TIME ZONE 'UTC')::date AND api_key_id <> 0
      AND ($1::timestamptz IS NULL OR day >= ($1::timestamptz)::date)
    UNION ALL
    SELECT api_key_id, total_cost
    FROM usage_logs
    WHERE api_key_id IS NOT NULL AND created_at >= (now() AT TIME ZONE 'UTC')::date
      AND ($1::timestamptz IS NULL OR created_at >= $1::timestamptz)
) x
GROUP BY api_key_id
ORDER BY total_cost DESC
LIMIT 10`

	var arg any
	if applyFilter {
		arg = since
	}

	rows, err := db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]apiKeyCostStats, 0, 10)

	for rows.Next() {
		var stat apiKeyCostStats
		if scanErr := rows.Scan(&stat.APIKeyID, &stat.Cost); scanErr != nil {
			return nil, scanErr
		}

		results = append(results, stat)
	}

	return results, rows.Err()
}

// channelStats is the per-channel request counter with the channel display name.
type channelStats struct {
	ChannelName string `json:"channel_name"`
	Count       int    `json:"count"`
}

// requestStatsByChannelFromViews reads per-channel request counts from the daily
// aggregate plus today's detail, joining channels for the display name.
func (r *queryResolver) requestStatsByChannelFromViews(ctx context.Context, since time.Time, applyFilter bool) ([]channelStats, error) {
	db, ok := r.statsDatabase()
	if !ok {
		return nil, errStatsDatabaseUnavailable
	}

	query := `
SELECT c.name AS channel_name, COALESCE(SUM(x.request_count), 0)::bigint AS count
FROM (
    SELECT channel_id, request_count
    FROM usage_stats_daily
    WHERE day < (now() AT TIME ZONE 'UTC')::date AND channel_id <> 0
      AND ($1::timestamptz IS NULL OR day >= ($1::timestamptz)::date)
    UNION ALL
    SELECT channel_id, COUNT(*)::bigint
    FROM usage_logs
    WHERE channel_id IS NOT NULL AND created_at >= (now() AT TIME ZONE 'UTC')::date
      AND ($1::timestamptz IS NULL OR created_at >= $1::timestamptz)
    GROUP BY channel_id
) x
JOIN channels c ON c.id = x.channel_id AND c.deleted_at = 0
GROUP BY c.name
ORDER BY count DESC
LIMIT 10`

	var arg any
	if applyFilter {
		arg = since
	}

	rows, err := db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]channelStats, 0, 10)

	for rows.Next() {
		var stat channelStats
		if scanErr := rows.Scan(&stat.ChannelName, &stat.Count); scanErr != nil {
			return nil, scanErr
		}

		results = append(results, stat)
	}

	return results, rows.Err()
}

// channelTokenStats is the per-channel token totals.
type channelTokenStats struct {
	ChannelName     string `json:"channel_name"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
}

// channelCostStats is the per-channel cost sum.
type channelCostStats struct {
	ChannelName string  `json:"channel_name"`
	Cost        float64 `json:"total_cost"`
}

const channelStatsUnion = `
    SELECT channel_id, prompt_tokens, completion_tokens, prompt_cached_tokens, completion_reasoning_tokens, total_cost
    FROM usage_stats_daily
    WHERE day < (now() AT TIME ZONE 'UTC')::date AND channel_id <> 0
      AND ($1::timestamptz IS NULL OR day >= ($1::timestamptz)::date)
    UNION ALL
    SELECT channel_id, prompt_tokens, completion_tokens, prompt_cached_tokens, completion_reasoning_tokens, total_cost
    FROM usage_logs
    WHERE channel_id IS NOT NULL AND created_at >= (now() AT TIME ZONE 'UTC')::date
      AND ($1::timestamptz IS NULL OR created_at >= $1::timestamptz)`

func (r *queryResolver) tokenStatsByChannelFromViews(ctx context.Context, since time.Time, applyFilter bool) ([]channelTokenStats, error) {
	db, ok := r.statsDatabase()
	if !ok {
		return nil, errStatsDatabaseUnavailable
	}

	query := `
SELECT c.name AS channel_name,
       COALESCE(SUM(x.prompt_tokens), 0)::bigint                 AS input_tokens,
       COALESCE(SUM(x.completion_tokens), 0)::bigint             AS output_tokens,
       COALESCE(SUM(x.prompt_cached_tokens), 0)::bigint          AS cached_tokens,
       COALESCE(SUM(x.completion_reasoning_tokens), 0)::bigint   AS reasoning_tokens
FROM (` + channelStatsUnion + `
) x
JOIN channels c ON c.id = x.channel_id AND c.deleted_at = 0
GROUP BY c.name
ORDER BY COALESCE(SUM(x.prompt_tokens), 0) + COALESCE(SUM(x.completion_tokens), 0) + COALESCE(SUM(x.completion_reasoning_tokens), 0) DESC
LIMIT 10`

	var arg any
	if applyFilter {
		arg = since
	}

	rows, err := db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]channelTokenStats, 0, 10)

	for rows.Next() {
		var stat channelTokenStats
		if scanErr := rows.Scan(&stat.ChannelName, &stat.InputTokens, &stat.OutputTokens,
			&stat.CachedTokens, &stat.ReasoningTokens); scanErr != nil {
			return nil, scanErr
		}

		results = append(results, stat)
	}

	return results, rows.Err()
}

func (r *queryResolver) costStatsByChannelFromViews(ctx context.Context, since time.Time, applyFilter bool) ([]channelCostStats, error) {
	db, ok := r.statsDatabase()
	if !ok {
		return nil, errStatsDatabaseUnavailable
	}

	query := `
SELECT c.name AS channel_name, COALESCE(SUM(x.total_cost), 0) AS total_cost
FROM (` + channelStatsUnion + `
) x
JOIN channels c ON c.id = x.channel_id AND c.deleted_at = 0
GROUP BY c.name
ORDER BY total_cost DESC
LIMIT 10`

	var arg any
	if applyFilter {
		arg = since
	}

	rows, err := db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]channelCostStats, 0, 10)

	for rows.Next() {
		var stat channelCostStats
		if scanErr := rows.Scan(&stat.ChannelName, &stat.Cost); scanErr != nil {
			return nil, scanErr
		}

		results = append(results, stat)
	}

	return results, rows.Err()
}

// tokenStats is the per-API-key token totals.
type tokenStats struct {
	APIKeyID        int   `json:"api_key_id"`
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// requestStatusCount is the per-status request counter.
type requestStatusCount struct {
	Status request.Status `json:"status"`
	Count  int            `json:"count"`
}

func (r *queryResolver) dashboardOverviewFromViews(ctx context.Context) ([]requestStatusCount, error) {
	db, ok := r.statsDatabase()
	if !ok {
		return nil, errStatsDatabaseUnavailable
	}

	query := `
SELECT status, COALESCE(SUM(request_count), 0)::bigint AS count
FROM (
    SELECT status, request_count
    FROM request_stats_daily
    WHERE day < (now() AT TIME ZONE 'UTC')::date
    UNION ALL
    SELECT status, COUNT(*)::bigint
    FROM requests
    WHERE created_at >= (now() AT TIME ZONE 'UTC')::date
    GROUP BY status
) x
GROUP BY status`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]requestStatusCount, 0, 8)

	for rows.Next() {
		var stat requestStatusCount

		var status string
		if scanErr := rows.Scan(&status, &stat.Count); scanErr != nil {
			return nil, scanErr
		}

		stat.Status = request.Status(status)
		results = append(results, stat)
	}

	return results, rows.Err()
}

func (r *queryResolver) tokenStatsByAPIKeyFromViews(ctx context.Context, since time.Time, applyFilter bool) ([]tokenStats, error) {
	db, ok := r.statsDatabase()
	if !ok {
		return nil, errStatsDatabaseUnavailable
	}

	query := `
SELECT api_key_id,
       COALESCE(SUM(prompt_tokens), 0)::bigint               AS input_tokens,
       COALESCE(SUM(completion_tokens), 0)::bigint           AS output_tokens,
       COALESCE(SUM(prompt_cached_tokens), 0)::bigint        AS cached_tokens,
       COALESCE(SUM(completion_reasoning_tokens), 0)::bigint AS reasoning_tokens
FROM (
    SELECT api_key_id, prompt_tokens, completion_tokens, prompt_cached_tokens, completion_reasoning_tokens
    FROM usage_stats_daily
    WHERE day < (now() AT TIME ZONE 'UTC')::date AND api_key_id <> 0
      AND ($1::timestamptz IS NULL OR day >= ($1::timestamptz)::date)
    UNION ALL
    SELECT api_key_id, prompt_tokens, completion_tokens, prompt_cached_tokens, completion_reasoning_tokens
    FROM usage_logs
    WHERE api_key_id IS NOT NULL AND created_at >= (now() AT TIME ZONE 'UTC')::date
      AND ($1::timestamptz IS NULL OR created_at >= $1::timestamptz)
) x
GROUP BY api_key_id`

	var arg any
	if applyFilter {
		arg = since
	}

	rows, err := db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]tokenStats, 0, 16)

	for rows.Next() {
		var stat tokenStats
		if scanErr := rows.Scan(&stat.APIKeyID, &stat.InputTokens, &stat.OutputTokens,
			&stat.CachedTokens, &stat.ReasoningTokens); scanErr != nil {
			return nil, scanErr
		}

		results = append(results, stat)
	}

	return results, rows.Err()
}
