-- name: GetOverviewTotals :one
WITH filtered AS (
  SELECT
    cost_currency,
    request_count,
    input_tokens,
    cache_read_tokens,
    output_tokens,
    cache_write_tokens,
    cache_write_1h_tokens,
    cost
  FROM request_overview_bucketed
  WHERE bucket_at >= sqlc.arg('start_at')::timestamp
    AND bucket_at < sqlc.arg('end_at')::timestamp
    AND user_id = sqlc.arg('user_id')::bigint
    AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
    AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
    AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
    AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
    AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int)
), totals AS (
  SELECT
    COALESCE(SUM(
      input_tokens + cache_read_tokens + output_tokens + cache_write_tokens + cache_write_1h_tokens
    ), 0)::bigint AS total_tokens,
    COALESCE(SUM(request_count), 0)::bigint AS total_requests
  FROM filtered
), cost_groups AS (
  SELECT cost_currency AS currency, SUM(cost)::float8 AS amount
  FROM filtered
  WHERE cost_currency IS NOT NULL AND cost_currency <> ''
  GROUP BY cost_currency
), cost_json AS (
  SELECT COALESCE(
    jsonb_agg(
      jsonb_build_object('currency', currency, 'amount', amount)
      ORDER BY currency
    ),
    '[]'::jsonb
  )::jsonb AS costs
  FROM cost_groups
)
SELECT
  totals.total_tokens,
  totals.total_requests,
  cost_json.costs::jsonb AS costs
FROM totals CROSS JOIN cost_json;

-- name: CountTraces :one
SELECT COUNT(*)::bigint AS trace_count
FROM traces
WHERE last_request_at >= sqlc.arg('start_at')::timestamp
  AND last_request_at < sqlc.arg('end_at')::timestamp
  AND user_id = sqlc.arg('user_id')::bigint;

-- name: CountTracesFiltered :one
SELECT COUNT(*)::bigint AS trace_count
FROM traces t
WHERE t.last_request_at >= sqlc.arg('start_at')::timestamp
  AND t.last_request_at < sqlc.arg('end_at')::timestamp
  AND t.user_id = sqlc.arg('user_id')::bigint
  AND EXISTS (
    SELECT 1
    FROM request r
    WHERE r.parent_span_id = t.parent_span_id
      AND r.user_id = t.user_id
      AND r.created_at >= t.first_request_at
      AND r.created_at <= t.last_request_at
      AND r.type = 1
      AND (sqlc.narg('api_key_id')::int IS NULL OR r.api_key_id = sqlc.narg('api_key_id')::int)
      AND (sqlc.narg('model')::text IS NULL OR r.model = sqlc.narg('model')::text)
      AND (sqlc.narg('upstream_model')::text IS NULL OR r.upstream_model = sqlc.narg('upstream_model')::text)
      AND (sqlc.narg('provider_id')::int IS NULL OR r.provider_id = sqlc.narg('provider_id')::int)
      AND (sqlc.narg('project_id')::int IS NULL OR r.project_id = sqlc.narg('project_id')::int)
  );

-- name: ListOverviewDistribution :many
SELECT
  CASE sqlc.arg('dimension')::text
    WHEN 'apiKey' THEN COALESCE(api_key_id::text, '')
    WHEN 'model' THEN COALESCE(model, '')
    WHEN 'upstreamModel' THEN COALESCE(upstream_model, '')
    WHEN 'provider' THEN COALESCE(provider_id::text, '')
    WHEN 'project' THEN COALESCE(project_id::text, '')
    ELSE ''
  END AS key,
  SUM(
    input_tokens + cache_read_tokens + output_tokens + cache_write_tokens + cache_write_1h_tokens
  )::bigint AS total_tokens,
  SUM(request_count)::bigint AS request_count
FROM request_overview_bucketed
WHERE bucket_at >= sqlc.arg('start_at')::timestamp
  AND bucket_at < sqlc.arg('end_at')::timestamp
  AND user_id = sqlc.arg('user_id')::bigint
  AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int)
GROUP BY key
ORDER BY total_tokens DESC, key ASC;

-- name: ListOverviewDistributionCosts :many
SELECT
  CASE sqlc.arg('dimension')::text
    WHEN 'apiKey' THEN COALESCE(api_key_id::text, '')
    WHEN 'model' THEN COALESCE(model, '')
    WHEN 'upstreamModel' THEN COALESCE(upstream_model, '')
    WHEN 'provider' THEN COALESCE(provider_id::text, '')
    WHEN 'project' THEN COALESCE(project_id::text, '')
    ELSE ''
  END AS key,
  cost_currency::text AS currency,
  SUM(cost)::float8 AS amount
FROM request_overview_bucketed
WHERE bucket_at >= sqlc.arg('start_at')::timestamp
  AND bucket_at < sqlc.arg('end_at')::timestamp
  AND user_id = sqlc.arg('user_id')::bigint
  AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int)
  AND cost_currency IS NOT NULL
  AND cost_currency <> ''
GROUP BY key, currency
ORDER BY key ASC, currency ASC;

-- name: ListOverviewTraceCountsByDimension :many
SELECT
  CASE sqlc.arg('dimension')::text
    WHEN 'apiKey' THEN COALESCE(r.api_key_id::text, '')
    WHEN 'model' THEN COALESCE(r.model, '')
    WHEN 'upstreamModel' THEN COALESCE(r.upstream_model, '')
    WHEN 'provider' THEN COALESCE(r.provider_id::text, '')
    WHEN 'project' THEN COALESCE(r.project_id::text, '')
    ELSE ''
  END AS key,
  COUNT(DISTINCT r.parent_span_id)::bigint AS trace_count
FROM request r
WHERE r.created_at >= sqlc.arg('start_at')::timestamp
  AND r.created_at < sqlc.arg('end_at')::timestamp
  AND r.user_id = sqlc.arg('user_id')::bigint
  AND r.type = 1
  AND r.parent_span_id IS NOT NULL
  AND r.parent_span_id <> ''
  AND (sqlc.narg('api_key_id')::int IS NULL OR r.api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR r.model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR r.upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR r.provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR r.project_id = sqlc.narg('project_id')::int)
GROUP BY key;

-- name: ListOverviewSeriesMetrics :many
SELECT
  bucket_at::timestamp AS bucket_at,
  CASE sqlc.arg('dimension')::text
    WHEN 'apiKey' THEN COALESCE(api_key_id::text, '')
    WHEN 'model' THEN COALESCE(model, '')
    WHEN 'upstreamModel' THEN COALESCE(upstream_model, '')
    WHEN 'provider' THEN COALESCE(provider_id::text, '')
    WHEN 'project' THEN COALESCE(project_id::text, '')
    ELSE ''
  END AS group_key,
  COALESCE(cost_currency, '')::text AS currency,
  SUM(input_tokens + cache_read_tokens + output_tokens + cache_write_tokens + cache_write_1h_tokens)::bigint AS tokens,
  SUM(request_count)::bigint AS requests,
  SUM(cost)::float8 AS cost
FROM request_overview_bucketed
WHERE bucket_at >= sqlc.arg('start_at')::timestamp
  AND bucket_at < sqlc.arg('end_at')::timestamp
  AND user_id = sqlc.arg('user_id')::bigint
  AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int)
GROUP BY bucket_at, group_key, currency
ORDER BY bucket_at ASC, group_key ASC, currency ASC;

-- name: ListOverviewSeriesTraces :many
SELECT
  time_bucket(sqlc.arg('bucket_width')::text::interval, r.created_at, sqlc.arg('bucket_origin')::timestamp)::timestamp AS bucket_at,
  CASE sqlc.arg('dimension')::text
    WHEN 'apiKey' THEN COALESCE(r.api_key_id::text, '')
    WHEN 'model' THEN COALESCE(r.model, '')
    WHEN 'upstreamModel' THEN COALESCE(r.upstream_model, '')
    WHEN 'provider' THEN COALESCE(r.provider_id::text, '')
    WHEN 'project' THEN COALESCE(r.project_id::text, '')
    ELSE ''
  END AS group_key,
  COUNT(DISTINCT r.parent_span_id)::bigint AS trace_count
FROM request r
WHERE r.created_at >= sqlc.arg('start_at')::timestamp
  AND r.created_at < sqlc.arg('end_at')::timestamp
  AND r.user_id = sqlc.arg('user_id')::bigint
  AND r.type = 1
  AND r.parent_span_id IS NOT NULL
  AND r.parent_span_id <> ''
  AND (sqlc.narg('api_key_id')::int IS NULL OR r.api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR r.model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR r.upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR r.provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR r.project_id = sqlc.narg('project_id')::int)
GROUP BY bucket_at, group_key
ORDER BY bucket_at ASC, group_key ASC;

-- name: ListOverviewCacheHitRateSeries :many
SELECT
  bucket_at::timestamp AS bucket_at,
  CASE sqlc.arg('dimension')::text
    WHEN 'apiKey' THEN COALESCE(api_key_id::text, '')
    WHEN 'model' THEN COALESCE(model, '')
    WHEN 'upstreamModel' THEN COALESCE(upstream_model, '')
    WHEN 'provider' THEN COALESCE(provider_id::text, '')
    WHEN 'project' THEN COALESCE(project_id::text, '')
    ELSE ''
  END AS group_key,
  SUM(cache_read_tokens)::float8 AS cache_read_token_sum,
  SUM(input_tokens + cache_read_tokens + cache_write_tokens + cache_write_1h_tokens)::float8 AS input_token_sum
FROM request_overview_bucketed
WHERE bucket_at >= sqlc.arg('start_at')::timestamp
  AND bucket_at < sqlc.arg('end_at')::timestamp
  AND user_id = sqlc.arg('user_id')::bigint
  AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int)
GROUP BY bucket_at, group_key
HAVING SUM(input_tokens + cache_read_tokens + cache_write_tokens + cache_write_1h_tokens) > 0
ORDER BY bucket_at ASC, group_key ASC;

-- name: GetOverviewTokenBreakdown :one
SELECT
  COALESCE(SUM(input_tokens), 0)::bigint           AS input_tokens,
  COALESCE(SUM(cache_read_tokens), 0)::bigint      AS cache_read_tokens,
  COALESCE(SUM(cache_write_tokens), 0)::bigint     AS cache_write_tokens,
  COALESCE(SUM(cache_write_1h_tokens), 0)::bigint  AS cache_write_1h_tokens,
  COALESCE(SUM(output_tokens), 0)::bigint          AS output_tokens
FROM request_overview_bucketed
WHERE bucket_at >= sqlc.arg('start_at')::timestamp
  AND bucket_at < sqlc.arg('end_at')::timestamp
  AND user_id = sqlc.arg('user_id')::bigint
  AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int);

-- name: ListOverviewBreakdownTokens :many
SELECT
  COALESCE(api_key_id, 0)::int          AS api_key_id,
  COALESCE(model, '')::text             AS model,
  COALESCE(upstream_model, '')::text    AS upstream_model,
  COALESCE(provider_id, 0)::int         AS provider_id,
  COALESCE(project_id, 0)::int          AS project_id,
  SUM(
    input_tokens + cache_read_tokens + output_tokens + cache_write_tokens + cache_write_1h_tokens
  )::bigint AS total_tokens
FROM request_overview_bucketed
WHERE bucket_at >= sqlc.arg('start_at')::timestamp
  AND bucket_at < sqlc.arg('end_at')::timestamp
  AND user_id = sqlc.arg('user_id')::bigint
  AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int)
GROUP BY 1, 2, 3, 4, 5
HAVING SUM(
  input_tokens + cache_read_tokens + output_tokens + cache_write_tokens + cache_write_1h_tokens
) > 0;

-- name: ListOverviewBreakdownCosts :many
SELECT
  COALESCE(api_key_id, 0)::int          AS api_key_id,
  COALESCE(model, '')::text             AS model,
  COALESCE(upstream_model, '')::text    AS upstream_model,
  COALESCE(provider_id, 0)::int         AS provider_id,
  COALESCE(project_id, 0)::int          AS project_id,
  cost_currency::text                    AS currency,
  SUM(cost)::float8                      AS amount
FROM request_overview_bucketed
WHERE bucket_at >= sqlc.arg('start_at')::timestamp
  AND bucket_at < sqlc.arg('end_at')::timestamp
  AND user_id = sqlc.arg('user_id')::bigint
  AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int)
  AND cost_currency IS NOT NULL
  AND cost_currency <> ''
GROUP BY 1, 2, 3, 4, 5, 6;

-- name: ListOverviewSpeedSeries :many
SELECT
  bucket_at::timestamp AS bucket_at,
  CASE sqlc.arg('dimension')::text
    WHEN 'model' THEN COALESCE(model, '')
    WHEN 'upstreamModel' THEN COALESCE(upstream_model, '')
    WHEN 'provider' THEN COALESCE(provider_id::text, '')
    WHEN 'apiKey' THEN COALESCE(api_key_id::text, '')
    WHEN 'project' THEN COALESCE(project_id::text, '')
    ELSE ''
  END AS group_key,
  COALESCE(SUM(prefill_token_sum), 0)::float8 AS prefill_token_sum,
  COALESCE(SUM(prefill_time_sum), 0)::float8 AS prefill_time_sum,
  COALESCE(SUM(prefill_request_count), 0)::bigint AS prefill_request_count,
  COALESCE(SUM(decode_token_sum), 0)::float8 AS decode_token_sum,
  COALESCE(SUM(decode_time_sum), 0)::float8 AS decode_time_sum
FROM request_speed_bucketed
WHERE bucket_at >= sqlc.arg('start_at')::timestamp
  AND bucket_at < sqlc.arg('end_at')::timestamp
  AND user_id = sqlc.arg('user_id')::bigint
  AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int)
GROUP BY bucket_at, group_key
HAVING SUM(prefill_time_sum) > 0 OR SUM(decode_time_sum) > 0
ORDER BY bucket_at ASC, group_key ASC;

-- name: ListOverviewOutcomeSeries :many
SELECT
  bucket_at::timestamp AS bucket_at,
  CASE sqlc.arg('dimension')::text
    WHEN 'apiKey' THEN COALESCE(api_key_id::text, '')
    WHEN 'model' THEN COALESCE(model, '')
    WHEN 'upstreamModel' THEN COALESCE(upstream_model, '')
    WHEN 'provider' THEN COALESCE(provider_id::text, '')
    WHEN 'project' THEN COALESCE(project_id::text, '')
    ELSE ''
  END AS group_key,
  type::int AS request_type,
  finish_reason::int AS finish_reason,
  empty_response::bool AS empty_response,
  SUM(request_count)::bigint AS request_count
FROM request_outcome_bucketed
WHERE bucket_at >= sqlc.arg('start_at')::timestamp
  AND bucket_at < sqlc.arg('end_at')::timestamp
  AND user_id = sqlc.arg('user_id')::bigint
  AND endpoint_path IN (SELECT path FROM completion_endpoint_path)
  AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int)
GROUP BY bucket_at, group_key, request_type, finish_reason, empty_response
ORDER BY bucket_at ASC, group_key ASC;

-- name: GetOverviewUpstreamSuccessTotals :one
SELECT
  -- success = finish_reason 3 (db.FinishReasonEOF, 正常结束) with non-zero output tokens.
  COALESCE(SUM(request_count) FILTER (
    WHERE finish_reason = 3 AND NOT empty_response
  ), 0)::bigint AS successful,
  COALESCE(SUM(request_count), 0)::bigint AS total
FROM request_outcome_bucketed
WHERE bucket_at >= sqlc.arg('start_at')::timestamp
  AND bucket_at < sqlc.arg('end_at')::timestamp
  AND user_id = sqlc.arg('user_id')::bigint
  AND type = 1
  AND endpoint_path IN (SELECT path FROM completion_endpoint_path)
  AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int);

-- name: GetOverviewSpeedBoxplot :many
WITH speeds AS (
  SELECT
    CASE sqlc.arg('dimension')::text
      WHEN 'model' THEN COALESCE(model, '')
      WHEN 'upstreamModel' THEN COALESCE(upstream_model, '')
      WHEN 'provider' THEN COALESCE(provider_id::text, '')
      WHEN 'apiKey' THEN COALESCE(api_key_id::text, '')
      WHEN 'project' THEN COALESCE(project_id::text, '')
      ELSE ''
    END AS group_key,
    output_tokens::float8 / ((time_spent_ms - ttft_ms)::float8 / 1000.0) AS decode_speed
  FROM request
  WHERE type = 1
    AND status_code = 200 AND finish_reason IN (2, 3, 5)
    AND created_at >= sqlc.arg('start_at')::timestamp
    AND created_at < sqlc.arg('end_at')::timestamp
    AND user_id = sqlc.arg('user_id')::bigint
    AND output_tokens >= 50
    AND ttft_ms IS NOT NULL
    AND time_spent_ms IS NOT NULL
    AND (time_spent_ms - ttft_ms) >= 500
    AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
    AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
    AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
    AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
    AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int)
)
SELECT
  group_key,
  MIN(decode_speed)::float8 AS min_speed,
  percentile_cont(0.25) WITHIN GROUP (ORDER BY decode_speed)::float8 AS p25_speed,
  percentile_cont(0.5) WITHIN GROUP (ORDER BY decode_speed)::float8 AS median_speed,
  percentile_cont(0.95) WITHIN GROUP (ORDER BY decode_speed)::float8 AS p95_speed,
  GREATEST(
    percentile_cont(0.99) WITHIN GROUP (ORDER BY decode_speed),
    percentile_cont(0.5) WITHIN GROUP (ORDER BY decode_speed) * 3
  )::float8 AS max_speed,
  COUNT(*)::bigint AS request_count
FROM speeds
GROUP BY group_key
ORDER BY median_speed DESC, max_speed DESC, group_key ASC;
