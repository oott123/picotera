-- name: GetEndpointByPath :one
SELECT * FROM endpoint WHERE path = $1 LIMIT 1;

-- name: ListAvailableModelNames :many
SELECT DISTINCT m.name
FROM model AS m
WHERE m.disabled = FALSE
  AND EXISTS (
    SELECT 1
    FROM provider AS p
    CROSS JOIN LATERAL jsonb_array_elements(p.provider_models) AS elem
    JOIN provider_endpoint AS pe ON pe.provider_id = p.id
    WHERE p.provider_models @> jsonb_build_array(jsonb_build_object('model', m.name))
      AND elem ->> 'model' = m.name
      AND p.disabled = FALSE
      AND COALESCE((elem ->> 'disabled')::boolean, false) = false
      AND pe.upstream_url <> ''
      AND p.credentials <> ''
      AND (
        elem -> 'endpoints' IS NULL
        OR jsonb_typeof(elem -> 'endpoints') <> 'array'
        OR jsonb_array_length(elem -> 'endpoints') = 0
        OR elem -> 'endpoints' @> to_jsonb(ARRAY[pe.endpoint_path])
      )
  )
ORDER BY m.name;

-- name: GetProvidersByEndpointAndModel :many
SELECT
  sqlc.arg('model_name')::text AS model_name,
  p.id AS provider_id,
  pe.endpoint_path,
  COALESCE(elem ->> 'upstreamModelName', '')::text AS upstream_model_name,
  COALESCE((elem ->> 'priority')::int, 0)::int AS priority,
  (COALESCE(elem -> 'annotations', '{}'::jsonb))::jsonb AS annotations,
  p.name AS provider_name,
  p.credentials AS provider_credentials,
  p.priority AS provider_priority,
  pe.upstream_url,
  pe.credentials_resolver AS send_credentials_resolver,
  p.proxy_url,
  p.insecure_tls,
  p.annotations AS provider_annotations,
  m.annotations AS model_annotations
FROM provider AS p
JOIN provider_endpoint AS pe ON pe.provider_id = p.id
JOIN model AS m ON m.name = sqlc.arg('model_name')::text
CROSS JOIN LATERAL jsonb_array_elements(p.provider_models) AS elem
WHERE pe.endpoint_path = sqlc.arg('endpoint_path')::text
  AND p.provider_models @> jsonb_build_array(jsonb_build_object('model', sqlc.arg('model_name')::text))
  AND elem ->> 'model' = sqlc.arg('model_name')::text
  AND p.disabled = FALSE
  AND m.disabled = FALSE
  AND COALESCE((elem ->> 'disabled')::boolean, false) = false
  AND (
    elem -> 'endpoints' IS NULL
    OR jsonb_typeof(elem -> 'endpoints') <> 'array'
    OR jsonb_array_length(elem -> 'endpoints') = 0
    OR elem -> 'endpoints' @> to_jsonb(ARRAY[pe.endpoint_path])
  );

-- name: GetProvidersByEndpointTypesAndModel :many
-- Sister query to GetProvidersByEndpointAndModel that selects across a SET of
-- endpoint types instead of a single endpoint path. The unified gateway
-- routes (/v1/messages, /v1/responses, /v1/chat/completions, and the two
-- Gemini variants) compute the type set from (source format, stream flag)
-- and call this. Returns the same column shape as the path-based query plus
-- e.endpoint_type so the handler can pick the right transformer per row.
SELECT
  sqlc.arg('model_name')::text AS model_name,
  p.id AS provider_id,
  pe.endpoint_path,
  e.endpoint_type AS endpoint_type,
  COALESCE(elem ->> 'upstreamModelName', '')::text AS upstream_model_name,
  COALESCE((elem ->> 'priority')::int, 0)::int AS priority,
  (COALESCE(elem -> 'annotations', '{}'::jsonb))::jsonb AS annotations,
  p.name AS provider_name,
  p.credentials AS provider_credentials,
  p.priority AS provider_priority,
  pe.upstream_url,
  pe.credentials_resolver AS send_credentials_resolver,
  p.proxy_url,
  p.insecure_tls,
  p.annotations AS provider_annotations,
  m.annotations AS model_annotations,
  p.supports_native_web_search
FROM provider AS p
JOIN provider_endpoint AS pe ON pe.provider_id = p.id
JOIN endpoint AS e ON e.path = pe.endpoint_path
JOIN model AS m ON m.name = sqlc.arg('model_name')::text
CROSS JOIN LATERAL jsonb_array_elements(p.provider_models) AS elem
WHERE e.endpoint_type = ANY(sqlc.arg('endpoint_types')::int[])
  AND p.provider_models @> jsonb_build_array(jsonb_build_object('model', sqlc.arg('model_name')::text))
  AND elem ->> 'model' = sqlc.arg('model_name')::text
  AND p.disabled = FALSE
  AND m.disabled = FALSE
  AND COALESCE((elem ->> 'disabled')::boolean, false) = false
  AND (
    elem -> 'endpoints' IS NULL
    OR jsonb_typeof(elem -> 'endpoints') <> 'array'
    OR jsonb_array_length(elem -> 'endpoints') = 0
    OR elem -> 'endpoints' @> to_jsonb(ARRAY[pe.endpoint_path])
  );

-- name: GetProvidersByEndpoint :many
-- Sister query to GetProvidersByEndpointAndModel for "no-model" endpoints
-- (endpoint.model_path = ''). Returns every non-disabled provider bound to the
-- given endpoint_path, with model-related columns flattened to constants so the
-- consuming Go code can treat both shapes uniformly.
SELECT
  ''::text AS model_name,
  p.id AS provider_id,
  pe.endpoint_path,
  ''::text AS upstream_model_name,
  0::int AS priority,
  '{}'::jsonb AS annotations,
  p.name AS provider_name,
  p.credentials AS provider_credentials,
  p.priority AS provider_priority,
  pe.upstream_url,
  pe.credentials_resolver AS send_credentials_resolver,
  p.proxy_url,
  p.insecure_tls,
  p.annotations AS provider_annotations,
  '{}'::jsonb AS model_annotations
FROM provider AS p
JOIN provider_endpoint AS pe ON pe.provider_id = p.id
WHERE pe.endpoint_path = sqlc.arg('endpoint_path')::text
  AND p.disabled = FALSE;

-- name: InsertRequest :one
INSERT INTO request (
  id, span_id, parent_span_id, type,
  provider_id, endpoint_path, api_key_id, model, upstream_model,
  input_tokens, cache_read_tokens, output_tokens, cache_write_tokens, cache_write_1h_tokens,
  status_code, error_message, ttft_ms, time_spent_ms,
  user_message_preview, project_id, created_at, user_id, external_request_id, external_response_id
) VALUES (
  $1, $2, $3, $4,
  $5, $6, $7, $8, $9,
  $10, $11, $12, $13, $14,
  $15, $16, $17, $18,
  $19, $20, $21, $22, $23, $24
)
RETURNING created_at;
