-- +goose NO TRANSACTION
-- +goose Up
CREATE MATERIALIZED VIEW request_outcome_bucketed
WITH (timescaledb.continuous) AS
SELECT
  time_bucket(INTERVAL '10 minutes', created_at) AS bucket_at,
  type,
  endpoint_path,
  api_key_id,
  model,
  upstream_model,
  provider_id,
  project_id,
  user_id,
  -- finish_reason values are 1..7; 0 uniquely means "in flight / not recorded".
  COALESCE(finish_reason, 0)::int AS finish_reason,
  (COALESCE(output_tokens, 0) = 0) AS empty_response,
  COUNT(*)::bigint AS request_count
FROM request
-- The last two keys are spelled out rather than referenced by output alias:
-- `finish_reason` names both an input column and the alias, and a bare name in
-- GROUP BY binds to the input column.
GROUP BY bucket_at, type, endpoint_path, api_key_id, model, upstream_model,
         provider_id, project_id, user_id,
         COALESCE(finish_reason, 0)::int, (COALESCE(output_tokens, 0) = 0)
WITH NO DATA;

ALTER MATERIALIZED VIEW request_outcome_bucketed
  SET (timescaledb.materialized_only = false);

SELECT add_continuous_aggregate_policy(
  'request_outcome_bucketed',
  start_offset      => INTERVAL '35 days',
  end_offset        => INTERVAL '5 minutes',
  schedule_interval => INTERVAL '5 minutes'
);

-- completion / chat endpoint paths: the shared scope for the requests list
-- 'emptyResponse' filter and the overview outcome-rate queries. Endpoint types
-- come from pkg/contract/endpoint.go; the unified routes from
-- pkg/server/unified_routes.go.
CREATE VIEW completion_endpoint_path AS
SELECT path AS path FROM endpoint WHERE endpoint_type = ANY(ARRAY[2,3,4,7,8]::int[])
UNION ALL
SELECT unnest(ARRAY[
  '/api/unified/v1/messages',
  '/api/unified/v1/responses',
  '/api/unified/v1/chat/completions',
  '/api/unified/v1beta/models/{model}:generateContent',
  '/api/unified/v1beta/models/{model}:streamGenerateContent'
]::text[]) AS path;

-- +goose Down
DROP VIEW IF EXISTS completion_endpoint_path;

SELECT remove_continuous_aggregate_policy('request_outcome_bucketed', if_exists => true);
DROP MATERIALIZED VIEW IF EXISTS request_outcome_bucketed;
