-- name: ListRequests :many
SELECT r.id, r.span_id, r.parent_span_id, r.type, r.provider_id, r.endpoint_path, r.api_key_id, r.model,
       r.upstream_model, r.input_tokens, r.cache_read_tokens, r.output_tokens, r.cache_write_tokens, r.cache_write_1h_tokens,
       r.status_code, r.error_message, r.ttft_ms, r.time_spent_ms, r.created_at,
       r.model_cost, r.model_cost_currency,
       r.user_message_preview, r.project_id, r.finish_reason,
       r.inferred_provider, r.inferred_model, r.inferred_model_source,
       r.user_id,
       r.external_request_id, r.external_response_id,
       r.annotations
FROM request r
LEFT JOIN traces selected_trace ON selected_trace.id = sqlc.narg('trace_id')::text
WHERE
  r.user_id = sqlc.arg('user_id')::bigint
  AND (sqlc.narg('type')::int IS NULL OR r.type = sqlc.narg('type'))
  AND (sqlc.narg('provider_id')::int IS NULL OR r.provider_id = sqlc.narg('provider_id'))
  AND (sqlc.narg('endpoint_path')::text IS NULL OR r.endpoint_path = sqlc.narg('endpoint_path'))
  AND (sqlc.narg('model')::text IS NULL OR r.model = sqlc.narg('model'))
  AND (sqlc.narg('upstream_model')::text IS NULL OR r.upstream_model = sqlc.narg('upstream_model'))
  AND (sqlc.narg('project_id')::int IS NULL OR r.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('start_at')::timestamp IS NULL OR r.created_at >= sqlc.narg('start_at')::timestamp)
  AND (sqlc.narg('end_at')::timestamp IS NULL OR r.created_at <= sqlc.narg('end_at')::timestamp)
  AND (
    sqlc.narg('trace_id')::text IS NULL
    OR (
      r.parent_span_id = selected_trace.parent_span_id
      AND r.created_at >= selected_trace.first_request_at
      AND r.created_at <= selected_trace.last_request_at
    )
  )
  -- emptyResponse filter: 'empty response' = completion endpoint with output_tokens 0/NULL.
  -- Completion endpoint scope comes from the completion_endpoint_path view
  -- (db/migrations/045_request_outcome_cagg.sql).
  AND (
    sqlc.narg('empty_response')::bool IS NULL
    OR NOT sqlc.narg('empty_response')::bool
    OR (
      (r.output_tokens IS NULL OR r.output_tokens = 0)
      AND r.endpoint_path IN (SELECT path FROM completion_endpoint_path)
    )
  )
  -- finishReason filter: exact match on finish-reason values 1..7, or sentinel -1
  -- for "失败" (all failures = finish_reason IS NOT NULL AND <> 3 正常结束).
  -- NULL narg = no filter; "pending" (NULL finish_reason) is never a filter value.
  AND (
    sqlc.narg('finish_reason')::int IS NULL
    OR (sqlc.narg('finish_reason')::int = -1 AND r.finish_reason IS NOT NULL AND r.finish_reason <> 3)
    OR r.finish_reason = sqlc.narg('finish_reason')::int
  )
  AND (
    sqlc.narg('request_id')::text IS NULL
    OR r.id = sqlc.narg('request_id')::text
    OR r.parent_span_id = sqlc.narg('request_id')::text
    OR r.external_request_id = sqlc.narg('request_id')::text
    OR r.external_response_id = sqlc.narg('request_id')::text
  )
  AND (
    sqlc.narg('annotations')::jsonb IS NULL
    OR r.annotations @> sqlc.narg('annotations')::jsonb
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
    AND request.user_id = traces.user_id
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
      AND request.user_id = traces.user_id
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
    AND request.user_id = traces.user_id
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
    AND request.user_id = traces.user_id
    AND created_at >= traces.first_request_at
    AND created_at <= traces.last_request_at
    AND type = 0
    AND project_id IS NOT NULL
  ORDER BY created_at DESC, id DESC
  LIMIT 1
) trace_project ON true
WHERE
  traces.user_id = sqlc.arg('user_id')::bigint
  AND (sqlc.narg('start_at')::timestamp IS NULL OR traces.last_request_at >= sqlc.narg('start_at')::timestamp)
  AND (sqlc.narg('end_at')::timestamp IS NULL OR traces.last_request_at <= sqlc.narg('end_at')::timestamp)
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
SELECT r.*, t.id AS trace_id
FROM request r
LEFT JOIN traces t ON t.parent_span_id = r.parent_span_id AND t.user_id = r.user_id
WHERE r.id = $1
  AND r.created_at = sqlc.arg('id_created_at')::timestamp
  AND r.user_id = sqlc.arg('user_id')::bigint;

-- name: ListRequestsBySpan :many
WITH anchor AS (
  SELECT request.span_id
  FROM request
  WHERE request.id = sqlc.arg('id')::text
    AND request.created_at = sqlc.arg('id_created_at')::timestamp
    AND request.user_id = sqlc.arg('user_id')::bigint
)
SELECT r.id, r.span_id, r.parent_span_id, r.type, r.provider_id, r.endpoint_path,
       r.api_key_id, r.model, r.upstream_model, r.input_tokens, r.cache_read_tokens, r.output_tokens,
       r.cache_write_tokens, r.cache_write_1h_tokens, r.status_code, r.error_message, r.ttft_ms, r.time_spent_ms,
       r.created_at,
       r.model_cost, r.model_cost_currency,
       r.user_message_preview, r.project_id, r.finish_reason,
       r.inferred_provider, r.inferred_model, r.inferred_model_source,
       r.user_id,
       r.external_request_id, r.external_response_id,
       r.annotations,
       t.id AS trace_id
FROM request r CROSS JOIN anchor
LEFT JOIN traces t ON t.parent_span_id = r.parent_span_id AND t.user_id = r.user_id
WHERE r.span_id = anchor.span_id
  AND r.user_id = sqlc.arg('user_id')::bigint
ORDER BY r.created_at ASC, r.id ASC;

-- name: UpdateRequest :exec
UPDATE request SET
  provider_id = CASE WHEN sqlc.arg('set_provider_id')::bool THEN sqlc.narg('provider_id')::int ELSE provider_id END,
  model = CASE WHEN sqlc.arg('set_model')::bool THEN sqlc.narg('model')::text ELSE model END,
  upstream_model = CASE WHEN sqlc.arg('set_upstream_model')::bool THEN sqlc.narg('upstream_model')::text ELSE upstream_model END,
  endpoint_path = CASE WHEN sqlc.arg('set_endpoint_path')::bool THEN sqlc.narg('endpoint_path')::text ELSE endpoint_path END,
  api_key_id = CASE WHEN sqlc.arg('set_api_key_id')::bool THEN sqlc.narg('api_key_id')::int ELSE api_key_id END,
  user_id = CASE WHEN sqlc.arg('set_user_id')::bool THEN sqlc.narg('user_id')::bigint ELSE user_id END,
  project_id = CASE WHEN sqlc.arg('set_project_id')::bool THEN sqlc.narg('project_id')::int ELSE project_id END,
  status_code = CASE WHEN sqlc.arg('set_status_code')::bool THEN sqlc.narg('status_code')::int ELSE status_code END,
  error_message = CASE WHEN sqlc.arg('set_error_message')::bool THEN sqlc.narg('error_message')::text ELSE error_message END,
  time_spent_ms = CASE WHEN sqlc.arg('set_time_spent_ms')::bool THEN sqlc.narg('time_spent_ms')::int ELSE time_spent_ms END,
  ttft_ms = CASE WHEN sqlc.arg('set_ttft_ms')::bool THEN sqlc.narg('ttft_ms')::int ELSE ttft_ms END,
  input_tokens = CASE WHEN sqlc.arg('set_input_tokens')::bool THEN sqlc.narg('input_tokens')::int ELSE input_tokens END,
  output_tokens = CASE WHEN sqlc.arg('set_output_tokens')::bool THEN sqlc.narg('output_tokens')::int ELSE output_tokens END,
  cache_read_tokens = CASE WHEN sqlc.arg('set_cache_read_tokens')::bool THEN sqlc.narg('cache_read_tokens')::int ELSE cache_read_tokens END,
  cache_write_tokens = CASE WHEN sqlc.arg('set_cache_write_tokens')::bool THEN sqlc.narg('cache_write_tokens')::int ELSE cache_write_tokens END,
  cache_write_1h_tokens = CASE WHEN sqlc.arg('set_cache_write_1h_tokens')::bool THEN sqlc.narg('cache_write_1h_tokens')::int ELSE cache_write_1h_tokens END,
  model_cost = CASE WHEN sqlc.arg('set_model_cost')::bool THEN sqlc.narg('model_cost')::numeric ELSE model_cost END,
  model_cost_currency = CASE WHEN sqlc.arg('set_model_cost_currency')::bool THEN sqlc.narg('model_cost_currency')::text ELSE model_cost_currency END,
  finish_reason = CASE WHEN sqlc.arg('set_finish_reason')::bool THEN sqlc.narg('finish_reason')::int ELSE finish_reason END,
  inferred_provider = CASE WHEN sqlc.arg('set_inferred_provider')::bool THEN sqlc.narg('inferred_provider')::text ELSE inferred_provider END,
  inferred_model = CASE WHEN sqlc.arg('set_inferred_model')::bool THEN sqlc.narg('inferred_model')::text ELSE inferred_model END,
  inferred_model_source = CASE WHEN sqlc.arg('set_inferred_model_source')::bool THEN sqlc.arg('inferred_model_source')::smallint ELSE inferred_model_source END,
  user_message_preview = CASE WHEN sqlc.arg('set_user_message_preview')::bool THEN sqlc.narg('user_message_preview')::text ELSE user_message_preview END,
  external_response_id = CASE WHEN sqlc.arg('set_external_response_id')::bool THEN sqlc.narg('external_response_id')::text ELSE external_response_id END,
  annotations = CASE WHEN sqlc.arg('set_annotations')::bool THEN sqlc.narg('annotations')::jsonb ELSE annotations END
WHERE id = sqlc.arg('id')::text AND created_at = sqlc.arg('created_at')::timestamp;
