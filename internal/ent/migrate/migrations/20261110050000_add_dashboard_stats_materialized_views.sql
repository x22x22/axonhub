-- Dashboard aggregations (dashboard.resolvers.go) scan the detail tables:
--   usage_logs        8M rows / ~3.7GB
--   requests        600K rows / ~1.2TB (bytea + toast)
-- A single dashboard page renders ten of them, and on a multi-terabyte database
-- each one takes seconds and can exceed the GraphQL deadline.
--
-- The detail is 380:1 compressible at day granularity:
--   usage_logs        8,046,956 rows ->  21,161 rows (4.4MB)
--   requests            608,369 rows ->     723 rows (208KB)
-- so aggregate into two materialized views and let the resolvers read those.
--
-- Apply this BEFORE rolling out the binary that reads them; the resolvers fall
-- back to the detail tables when a view is missing, so an early rollout is safe
-- but not faster.
--
-- Build cost measured on the production gateway: 10.7s (usage) + 17.8s (requests).
--
-- The views are NOT created by the ent auto-migration (ent has no materialized
-- view support), so keep this in sync with the resolvers by hand.

CREATE MATERIALIZED VIEW IF NOT EXISTS usage_stats_daily AS
SELECT
  date_trunc('day', created_at)::date                    AS day,
  COALESCE(project_id, 0)                                AS project_id,
  COALESCE(api_key_id, 0)                                AS api_key_id,
  COALESCE(channel_id, 0)                                AS channel_id,
  COALESCE(model_id, '')                                 AS model_id,
  COALESCE(SUM(prompt_tokens), 0)::bigint                AS prompt_tokens,
  COALESCE(SUM(completion_tokens), 0)::bigint            AS completion_tokens,
  COALESCE(SUM(total_tokens), 0)::bigint                 AS total_tokens,
  COALESCE(SUM(prompt_cached_tokens), 0)::bigint         AS prompt_cached_tokens,
  COALESCE(SUM(completion_reasoning_tokens), 0)::bigint  AS completion_reasoning_tokens,
  COUNT(*)::bigint                                       AS request_count,
  COALESCE(SUM(total_cost), 0)                           AS total_cost
FROM usage_logs
GROUP BY 1,2,3,4,5;

-- REFRESH MATERIALIZED VIEW CONCURRENTLY requires a unique index.
CREATE UNIQUE INDEX IF NOT EXISTS usage_stats_daily_pk
  ON usage_stats_daily (day, project_id, api_key_id, channel_id, model_id);

CREATE MATERIALIZED VIEW IF NOT EXISTS request_stats_daily AS
SELECT
  date_trunc('day', created_at)::date                       AS day,
  COALESCE(project_id, 0)                                   AS project_id,
  COALESCE(channel_id, 0)                                   AS channel_id,
  COALESCE(model_id, '')                                    AS model_id,
  status,
  COUNT(*)::bigint                                          AS request_count,
  COUNT(*) FILTER (WHERE status = 'failed')::bigint         AS failed_count,
  COALESCE(SUM(metrics_latency_ms), 0)::bigint              AS latency_ms_sum,
  COUNT(metrics_latency_ms)::bigint                         AS latency_count,
  COALESCE(SUM(metrics_first_token_latency_ms), 0)::bigint  AS ttft_ms_sum,
  COUNT(metrics_first_token_latency_ms)::bigint             AS ttft_count
FROM requests
GROUP BY 1,2,3,4,5;

CREATE UNIQUE INDEX IF NOT EXISTS request_stats_daily_pk
  ON request_stats_daily (day, project_id, channel_id, model_id, status);

-- channelSuccessRates reads request_executions (1.2TB of toast). Aggregating the
-- light columns of a 7-day window measured 28.1s against the detail table and
-- 0.34ms against this view.
CREATE MATERIALIZED VIEW IF NOT EXISTS execution_stats_daily AS
SELECT
  date_trunc('day', created_at)::date               AS day,
  COALESCE(project_id, 0)                           AS project_id,
  COALESCE(channel_id, 0)                           AS channel_id,
  COALESCE(model_id, '')                            AS model_id,
  status,
  COUNT(*)::bigint                                  AS request_count,
  COUNT(*) FILTER (WHERE status = 'failed')::bigint  AS failed_count
FROM request_executions
GROUP BY 1,2,3,4,5;

CREATE UNIQUE INDEX IF NOT EXISTS execution_stats_daily_pk
  ON execution_stats_daily (day, project_id, channel_id, model_id, status);

-- Refresh policy: history rows never change once the day is over, so only the
-- recent window needs recomputing. CONCURRENTLY keeps reads unblocked.
--
--   REFRESH MATERIALIZED VIEW CONCURRENTLY usage_stats_daily;
--   REFRESH MATERIALIZED VIEW CONCURRENTLY request_stats_daily;
--
-- The gateway refreshes both itself (see internal/server/biz/stats_view.go).
