-- name: ListRequests :many
SELECT r.id, r.span_id, r.parent_span_id, r.type, r.status, r.provider_id, r.endpoint_path, r.api_key_id, r.model,
       r.upstream_model, r.input_tokens, r.cache_read_tokens, r.output_tokens, r.cache_write_tokens, r.cache_write_1h_tokens,
       r.status_code, r.error_message, r.ttft_ms, r.time_spent_ms, r.created_at,
       r.model_cost, r.model_cost_currency,
       r.user_message_preview, r.project_id
FROM request r
LEFT JOIN traces selected_trace ON selected_trace.id = sqlc.narg('trace_id')::text
WHERE
  (sqlc.narg('type')::int IS NULL OR r.type = sqlc.narg('type'))
  AND (sqlc.narg('provider_id')::int IS NULL OR r.provider_id = sqlc.narg('provider_id'))
  AND (sqlc.narg('endpoint_path')::text IS NULL OR r.endpoint_path = sqlc.narg('endpoint_path'))
  AND (sqlc.narg('model')::text IS NULL OR r.model = sqlc.narg('model'))
  AND (sqlc.narg('upstream_model')::text IS NULL OR r.upstream_model = sqlc.narg('upstream_model'))
  AND (sqlc.narg('project_id')::int IS NULL OR r.project_id = sqlc.narg('project_id'))
  AND (
    sqlc.narg('trace_id')::text IS NULL
    OR (
      r.parent_span_id = selected_trace.parent_span_id
      AND r.created_at >= selected_trace.first_request_at
      AND r.created_at <= selected_trace.last_request_at
    )
  )
  AND (
    sqlc.narg('cursor_created_at')::timestamp IS NULL
    OR (r.created_at, r.id) < (sqlc.narg('cursor_created_at')::timestamp, sqlc.narg('cursor_id')::text)
  )
ORDER BY r.created_at DESC, r.id DESC
LIMIT sqlc.narg('limit')::int;

-- name: ListRequestTraces :many
SELECT
  traces.id,
  traces.parent_span_id,
  COALESCE(metrics.meta_request_count, 0)::bigint AS meta_request_count,
  COALESCE(metrics.upstream_request_count, 0)::bigint AS upstream_request_count,
  COALESCE(metrics.total_tokens, 0)::bigint AS total_tokens,
  COALESCE(metrics.input_tokens, 0)::bigint AS input_tokens,
  COALESCE(metrics.cache_read_tokens, 0)::bigint AS cache_read_tokens,
  COALESCE(metrics.output_tokens, 0)::bigint AS output_tokens,
  COALESCE(metrics.cache_write_tokens, 0)::bigint AS cache_write_tokens,
  COALESCE(metrics.cache_write_1h_tokens, 0)::bigint AS cache_write_1h_tokens,
  COALESCE(model_costs.costs, '[]'::jsonb)::jsonb AS model_costs,
  traces.first_request_at,
  traces.last_request_at,
  preview.user_message_preview,
  trace_project.project_id AS project_id
FROM traces
LEFT JOIN LATERAL (
  SELECT
    COUNT(*) FILTER (WHERE type = 0)::bigint AS meta_request_count,
    COUNT(*) FILTER (WHERE type = 1)::bigint AS upstream_request_count,
    COALESCE(SUM(
      COALESCE(input_tokens, 0)
      + COALESCE(cache_read_tokens, 0)
      + COALESCE(output_tokens, 0)
      + COALESCE(cache_write_tokens, 0)
      + COALESCE(cache_write_1h_tokens, 0)
    ) FILTER (WHERE type = 1), 0)::bigint AS total_tokens,
    COALESCE(SUM(COALESCE(input_tokens, 0)) FILTER (WHERE type = 1), 0)::bigint AS input_tokens,
    COALESCE(SUM(COALESCE(cache_read_tokens, 0)) FILTER (WHERE type = 1), 0)::bigint AS cache_read_tokens,
    COALESCE(SUM(COALESCE(output_tokens, 0)) FILTER (WHERE type = 1), 0)::bigint AS output_tokens,
    COALESCE(SUM(COALESCE(cache_write_tokens, 0)) FILTER (WHERE type = 1), 0)::bigint AS cache_write_tokens,
    COALESCE(SUM(COALESCE(cache_write_1h_tokens, 0)) FILTER (WHERE type = 1), 0)::bigint AS cache_write_1h_tokens
  FROM request
  WHERE parent_span_id = traces.parent_span_id
    AND created_at >= traces.first_request_at
    AND created_at <= traces.last_request_at
) metrics ON true
LEFT JOIN LATERAL (
  SELECT jsonb_agg(
    jsonb_build_object('currency', grouped.currency, 'amount', grouped.amount)
    ORDER BY grouped.currency
  ) AS costs
  FROM (
    SELECT model_cost_currency AS currency, SUM(model_cost)::float8 AS amount
    FROM request
    WHERE parent_span_id = traces.parent_span_id
      AND created_at >= traces.first_request_at
      AND created_at <= traces.last_request_at
      AND type = 1
      AND model_cost IS NOT NULL
      AND model_cost_currency IS NOT NULL
    GROUP BY model_cost_currency
  ) grouped
) model_costs ON true
LEFT JOIN LATERAL (
  SELECT user_message_preview
  FROM request
  WHERE parent_span_id = traces.parent_span_id
    AND created_at >= traces.first_request_at
    AND created_at <= traces.last_request_at
    AND type = 0
    AND user_message_preview IS NOT NULL
  ORDER BY created_at DESC, id DESC
  LIMIT 1
) preview ON true
LEFT JOIN LATERAL (
  SELECT project_id
  FROM request
  WHERE parent_span_id = traces.parent_span_id
    AND created_at >= traces.first_request_at
    AND created_at <= traces.last_request_at
    AND type = 0
    AND project_id IS NOT NULL
  ORDER BY created_at DESC, id DESC
  LIMIT 1
) trace_project ON true
WHERE
  sqlc.narg('cursor_last_request_at')::timestamp IS NULL
  OR (traces.last_request_at, traces.id) < (
    sqlc.narg('cursor_last_request_at')::timestamp,
    sqlc.narg('cursor_trace_id')::text
  )
ORDER BY traces.last_request_at DESC, traces.id DESC
LIMIT sqlc.narg('limit')::int;

-- name: ListRequestTracesByAccount :many
SELECT
  traces.id,
  traces.parent_span_id,
  COALESCE(metrics.meta_request_count, 0)::bigint AS meta_request_count,
  COALESCE(metrics.upstream_request_count, 0)::bigint AS upstream_request_count,
  COALESCE(metrics.total_tokens, 0)::bigint AS total_tokens,
  COALESCE(metrics.input_tokens, 0)::bigint AS input_tokens,
  COALESCE(metrics.cache_read_tokens, 0)::bigint AS cache_read_tokens,
  COALESCE(metrics.output_tokens, 0)::bigint AS output_tokens,
  COALESCE(metrics.cache_write_tokens, 0)::bigint AS cache_write_tokens,
  COALESCE(metrics.cache_write_1h_tokens, 0)::bigint AS cache_write_1h_tokens,
  COALESCE(model_costs.costs, '[]'::jsonb)::jsonb AS model_costs,
  traces.first_request_at,
  traces.last_request_at,
  preview.user_message_preview,
  trace_project.project_id AS project_id
FROM traces
LEFT JOIN LATERAL (
  SELECT
    COUNT(*) FILTER (WHERE type = 0)::bigint AS meta_request_count,
    COUNT(*) FILTER (WHERE type = 1)::bigint AS upstream_request_count,
    COALESCE(SUM(
      COALESCE(input_tokens, 0) + COALESCE(cache_read_tokens, 0) + COALESCE(output_tokens, 0)
      + COALESCE(cache_write_tokens, 0) + COALESCE(cache_write_1h_tokens, 0)
    ) FILTER (WHERE type = 1), 0)::bigint AS total_tokens,
    COALESCE(SUM(COALESCE(input_tokens, 0)) FILTER (WHERE type = 1), 0)::bigint AS input_tokens,
    COALESCE(SUM(COALESCE(cache_read_tokens, 0)) FILTER (WHERE type = 1), 0)::bigint AS cache_read_tokens,
    COALESCE(SUM(COALESCE(output_tokens, 0)) FILTER (WHERE type = 1), 0)::bigint AS output_tokens,
    COALESCE(SUM(COALESCE(cache_write_tokens, 0)) FILTER (WHERE type = 1), 0)::bigint AS cache_write_tokens,
    COALESCE(SUM(COALESCE(cache_write_1h_tokens, 0)) FILTER (WHERE type = 1), 0)::bigint AS cache_write_1h_tokens
  FROM request
  WHERE parent_span_id = traces.parent_span_id
    AND created_at >= traces.first_request_at
    AND created_at <= traces.last_request_at
) metrics ON true
LEFT JOIN LATERAL (
  SELECT jsonb_agg(
    jsonb_build_object('currency', grouped.currency, 'amount', grouped.amount)
    ORDER BY grouped.currency
  ) AS costs
  FROM (
    SELECT model_cost_currency AS currency, SUM(model_cost)::float8 AS amount
    FROM request
    WHERE parent_span_id = traces.parent_span_id
      AND created_at >= traces.first_request_at
      AND created_at <= traces.last_request_at
      AND type = 1
      AND model_cost IS NOT NULL
      AND model_cost_currency IS NOT NULL
    GROUP BY model_cost_currency
  ) grouped
) model_costs ON true
LEFT JOIN LATERAL (
  SELECT user_message_preview FROM request
  WHERE parent_span_id = traces.parent_span_id
    AND created_at >= traces.first_request_at
    AND created_at <= traces.last_request_at
    AND type = 0
    AND user_message_preview IS NOT NULL
  ORDER BY created_at DESC, id DESC
  LIMIT 1
) preview ON true
LEFT JOIN LATERAL (
  SELECT project_id FROM request
  WHERE parent_span_id = traces.parent_span_id
    AND created_at >= traces.first_request_at
    AND created_at <= traces.last_request_at
    AND type = 0
    AND project_id IS NOT NULL
  ORDER BY created_at DESC, id DESC
  LIMIT 1
) trace_project ON true
WHERE EXISTS (
  SELECT 1 FROM request r
  JOIN api_key k ON k.id = r.api_key_id
  WHERE r.parent_span_id = traces.parent_span_id
    AND r.created_at >= traces.first_request_at
    AND r.created_at <= traces.last_request_at
    AND k.account_id = sqlc.arg('account_id')::int
)
AND (
  sqlc.narg('cursor_last_request_at')::timestamp IS NULL
  OR (traces.last_request_at, traces.id) < (
    sqlc.narg('cursor_last_request_at')::timestamp,
    sqlc.narg('cursor_trace_id')::text
  )
)
ORDER BY traces.last_request_at DESC, traces.id DESC
LIMIT sqlc.narg('limit')::int;

-- name: GetRequest :one
SELECT * FROM request WHERE id = $1 AND created_at = sqlc.arg('id_created_at')::timestamp;

-- name: GetRequestOwnedBy :one
SELECT r.* FROM request r
JOIN api_key k ON k.id = r.api_key_id
WHERE r.id = $1
  AND r.created_at = sqlc.arg('id_created_at')::timestamp
  AND k.account_id = $2;

-- name: ListRequestsBySpan :many
WITH anchor AS (
  SELECT request.span_id
  FROM request
  WHERE request.id = sqlc.arg('id')::text
    AND request.created_at = sqlc.arg('id_created_at')::timestamp
)
SELECT r.id, r.span_id, r.parent_span_id, r.type, r.status, r.provider_id, r.endpoint_path,
       r.api_key_id, r.model, r.upstream_model, r.input_tokens, r.cache_read_tokens, r.output_tokens,
       r.cache_write_tokens, r.cache_write_1h_tokens, r.status_code, r.error_message, r.ttft_ms, r.time_spent_ms,
       r.created_at,
       r.model_cost, r.model_cost_currency,
       r.user_message_preview, r.project_id
FROM request r, anchor
WHERE r.span_id = anchor.span_id
ORDER BY r.created_at ASC, r.id ASC;

-- name: ListRequestSpansOwnedBy :many
WITH anchor AS (
  SELECT request.span_id
  FROM request
  JOIN api_key k ON k.id = request.api_key_id
  WHERE request.id = sqlc.arg('id')::text
    AND request.created_at = sqlc.arg('id_created_at')::timestamp
    AND k.account_id = sqlc.arg('account_id')::int
)
SELECT r.id, r.span_id, r.parent_span_id, r.type, r.status, r.provider_id, r.endpoint_path,
       r.api_key_id, r.model, r.upstream_model, r.input_tokens, r.cache_read_tokens, r.output_tokens,
       r.cache_write_tokens, r.cache_write_1h_tokens, r.status_code, r.error_message, r.ttft_ms, r.time_spent_ms,
       r.created_at,
       r.model_cost, r.model_cost_currency,
       r.user_message_preview, r.project_id
FROM request r, anchor
WHERE r.span_id = anchor.span_id
ORDER BY r.created_at ASC, r.id ASC;

-- name: UpdateRequestOnHeader :exec
UPDATE request
SET provider_id = $2, model = $3, upstream_model = $4, endpoint_path = $5, api_key_id = $6, status = $7
WHERE id = $1 AND created_at = sqlc.arg('created_at')::timestamp;

-- name: UpdateRequestOnComplete :exec
UPDATE request
SET status_code = $2, error_message = $3, time_spent_ms = $4, status = $5,
    ttft_ms = $6, input_tokens = $7, output_tokens = $8,
    cache_read_tokens = $9, cache_write_tokens = $10,
    cache_write_1h_tokens = $11,
    model_cost = $12, model_cost_currency = $13
WHERE id = $1 AND created_at = sqlc.arg('created_at')::timestamp;

-- name: UpdateRequestModel :exec
UPDATE request SET model = $2 WHERE id = $1 AND created_at = sqlc.arg('created_at')::timestamp;

-- name: UpdateRequestProjectID :exec
-- Backfills the meta request row's project_id after authentication completes.
-- The meta InsertRequest fires before auth so audit rows always exist; the
-- account-scoped project resolution then writes the matched id here.
UPDATE request SET project_id = $2
WHERE id = $1 AND created_at = sqlc.arg('created_at')::timestamp;

-- name: UpdateRequestMetrics :exec
UPDATE request
SET ttft_ms = $2, input_tokens = $3, output_tokens = $4,
    cache_read_tokens = $5, cache_write_tokens = $6, cache_write_1h_tokens = $7
WHERE id = $1 AND created_at = sqlc.arg('created_at')::timestamp;

-- name: ListRequestsByAccount :many
SELECT r.id, r.span_id, r.parent_span_id, r.type, r.status, r.provider_id, r.endpoint_path, r.api_key_id, r.model,
       r.upstream_model, r.input_tokens, r.cache_read_tokens, r.output_tokens, r.cache_write_tokens, r.cache_write_1h_tokens,
       r.status_code, r.error_message, r.ttft_ms, r.time_spent_ms, r.created_at,
       r.model_cost, r.model_cost_currency,
       r.user_message_preview, r.project_id
FROM request r
JOIN api_key k ON k.id = r.api_key_id
WHERE k.account_id = $1
ORDER BY r.created_at DESC, r.id DESC
LIMIT $2;
