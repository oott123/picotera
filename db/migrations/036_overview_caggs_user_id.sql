-- +goose NO TRANSACTION
-- +goose Up
SELECT remove_continuous_aggregate_policy('request_overview_hourly', if_exists => true);
DROP MATERIALIZED VIEW IF EXISTS request_overview_hourly;

CREATE MATERIALIZED VIEW request_overview_hourly
WITH (timescaledb.continuous) AS
SELECT
  time_bucket(INTERVAL '1 hour', created_at) AS bucket_at,
  api_key_id,
  model,
  upstream_model,
  provider_id,
  project_id,
  user_id,
  COALESCE(NULLIF(model_cost_currency, ''), '') AS cost_currency,
  COUNT(*)::bigint AS request_count,
  SUM(COALESCE(input_tokens, 0))::bigint AS input_tokens,
  SUM(COALESCE(cache_read_tokens, 0))::bigint AS cache_read_tokens,
  SUM(COALESCE(output_tokens, 0))::bigint AS output_tokens,
  SUM(COALESCE(cache_write_tokens, 0))::bigint AS cache_write_tokens,
  SUM(COALESCE(cache_write_1h_tokens, 0))::bigint AS cache_write_1h_tokens,
  SUM(COALESCE(model_cost, 0))::numeric(20, 6) AS cost
FROM request
WHERE type = 1
GROUP BY bucket_at, api_key_id, model, upstream_model, provider_id, project_id, user_id, cost_currency
WITH NO DATA;

ALTER MATERIALIZED VIEW request_overview_hourly
  SET (timescaledb.materialized_only = false);

SELECT add_continuous_aggregate_policy(
  'request_overview_hourly',
  start_offset      => INTERVAL '35 days',
  end_offset        => INTERVAL '5 minutes',
  schedule_interval => INTERVAL '5 minutes'
);

SELECT remove_continuous_aggregate_policy('request_speed_hourly', if_exists => true);
DROP MATERIALIZED VIEW IF EXISTS request_speed_hourly;

CREATE MATERIALIZED VIEW request_speed_hourly
WITH (timescaledb.continuous) AS
SELECT
  time_bucket(INTERVAL '1 hour', created_at) AS bucket_at,
  model,
  upstream_model,
  provider_id,
  api_key_id,
  project_id,
  user_id,
  SUM(CASE
    WHEN input_tokens >= 50 AND ttft_ms >= 500
    THEN input_tokens::float8
  END) AS prefill_token_sum,
  SUM(CASE
    WHEN input_tokens >= 50 AND ttft_ms >= 500
    THEN ttft_ms::float8
  END) AS prefill_time_sum,
  COUNT(CASE
    WHEN input_tokens >= 50 AND ttft_ms >= 500
    THEN 1
  END) AS prefill_request_count,
  SUM(CASE
    WHEN output_tokens >= 50
      AND ttft_ms IS NOT NULL
      AND time_spent_ms IS NOT NULL
      AND (time_spent_ms - ttft_ms) >= 500
    THEN output_tokens::float8
  END) AS decode_token_sum,
  SUM(CASE
    WHEN output_tokens >= 50
      AND ttft_ms IS NOT NULL
      AND time_spent_ms IS NOT NULL
      AND (time_spent_ms - ttft_ms) >= 500
    THEN (time_spent_ms - ttft_ms)::float8
  END) AS decode_time_sum
FROM request
WHERE type = 1
GROUP BY bucket_at, model, upstream_model, provider_id, api_key_id, project_id, user_id
WITH NO DATA;

ALTER MATERIALIZED VIEW request_speed_hourly
  SET (timescaledb.materialized_only = false);

SELECT add_continuous_aggregate_policy(
  'request_speed_hourly',
  start_offset      => INTERVAL '35 days',
  end_offset        => INTERVAL '5 minutes',
  schedule_interval => INTERVAL '5 minutes'
);

-- +goose Down
SELECT remove_continuous_aggregate_policy('request_speed_hourly', if_exists => true);
DROP MATERIALIZED VIEW IF EXISTS request_speed_hourly;

CREATE MATERIALIZED VIEW request_speed_hourly
WITH (timescaledb.continuous) AS
SELECT
  time_bucket(INTERVAL '1 hour', created_at) AS bucket_at,
  model,
  upstream_model,
  provider_id,
  api_key_id,
  project_id,
  SUM(CASE
    WHEN input_tokens >= 50 AND ttft_ms >= 500
    THEN input_tokens::float8
  END) AS prefill_token_sum,
  SUM(CASE
    WHEN input_tokens >= 50 AND ttft_ms >= 500
    THEN ttft_ms::float8
  END) AS prefill_time_sum,
  COUNT(CASE
    WHEN input_tokens >= 50 AND ttft_ms >= 500
    THEN 1
  END) AS prefill_request_count,
  SUM(CASE
    WHEN output_tokens >= 50
      AND ttft_ms IS NOT NULL
      AND time_spent_ms IS NOT NULL
      AND (time_spent_ms - ttft_ms) >= 500
    THEN output_tokens::float8
  END) AS decode_token_sum,
  SUM(CASE
    WHEN output_tokens >= 50
      AND ttft_ms IS NOT NULL
      AND time_spent_ms IS NOT NULL
      AND (time_spent_ms - ttft_ms) >= 500
    THEN (time_spent_ms - ttft_ms)::float8
  END) AS decode_time_sum
FROM request
WHERE type = 1
GROUP BY bucket_at, model, upstream_model, provider_id, api_key_id, project_id
WITH NO DATA;

ALTER MATERIALIZED VIEW request_speed_hourly
  SET (timescaledb.materialized_only = false);

SELECT add_continuous_aggregate_policy(
  'request_speed_hourly',
  start_offset      => INTERVAL '35 days',
  end_offset        => INTERVAL '5 minutes',
  schedule_interval => INTERVAL '5 minutes'
);

SELECT remove_continuous_aggregate_policy('request_overview_hourly', if_exists => true);
DROP MATERIALIZED VIEW IF EXISTS request_overview_hourly;

CREATE MATERIALIZED VIEW request_overview_hourly
WITH (timescaledb.continuous) AS
SELECT
  time_bucket(INTERVAL '1 hour', created_at) AS bucket_at,
  api_key_id,
  model,
  upstream_model,
  provider_id,
  project_id,
  COALESCE(NULLIF(model_cost_currency, ''), '') AS cost_currency,
  COUNT(*)::bigint AS request_count,
  SUM(COALESCE(input_tokens, 0))::bigint AS input_tokens,
  SUM(COALESCE(cache_read_tokens, 0))::bigint AS cache_read_tokens,
  SUM(COALESCE(output_tokens, 0))::bigint AS output_tokens,
  SUM(COALESCE(cache_write_tokens, 0))::bigint AS cache_write_tokens,
  SUM(COALESCE(cache_write_1h_tokens, 0))::bigint AS cache_write_1h_tokens,
  SUM(COALESCE(model_cost, 0))::numeric(20, 6) AS cost
FROM request
WHERE type = 1
GROUP BY bucket_at, api_key_id, model, upstream_model, provider_id, project_id, cost_currency
WITH NO DATA;

ALTER MATERIALIZED VIEW request_overview_hourly
  SET (timescaledb.materialized_only = false);

SELECT add_continuous_aggregate_policy(
  'request_overview_hourly',
  start_offset      => INTERVAL '35 days',
  end_offset        => INTERVAL '5 minutes',
  schedule_interval => INTERVAL '5 minutes'
);
