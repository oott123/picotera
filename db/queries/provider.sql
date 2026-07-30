-- name: GetProviderByID :one
SELECT * FROM provider WHERE id = $1 LIMIT 1;

-- name: GetProviders :many
SELECT * FROM provider ORDER BY priority DESC, id DESC;

-- name: CreateProvider :one
INSERT INTO provider (name, credentials, priority, provider_models, annotations, disabled, proxy_url, models_endpoint_url, models_endpoint_resolver, supports_native_web_search) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING *;

-- name: UpdateProvider :one
UPDATE provider
  SET
    name = CASE WHEN @set_name::bool THEN @name::text ELSE name END,
    credentials = CASE WHEN @set_credentials::bool THEN @credentials::text ELSE credentials END,
    priority = CASE WHEN @set_priority::bool THEN @priority::int ELSE priority END,
    provider_models = CASE WHEN @set_provider_models::bool THEN @provider_models::jsonb ELSE provider_models END,
    annotations = CASE WHEN @set_annotations::bool THEN @annotations::jsonb ELSE annotations END,
    disabled = CASE WHEN @set_disabled::bool THEN @disabled::bool ELSE disabled END,
    proxy_url = CASE WHEN @set_proxy_url::bool THEN @proxy_url::text ELSE proxy_url END,
    models_endpoint_url = CASE WHEN @set_models_endpoint_url::bool THEN @models_endpoint_url::text ELSE models_endpoint_url END,
    models_endpoint_resolver = CASE WHEN @set_models_endpoint_resolver::bool THEN @models_endpoint_resolver::int ELSE models_endpoint_resolver END,
    supports_native_web_search = CASE WHEN @set_supports_native_web_search::bool THEN @supports_native_web_search::bool ELSE supports_native_web_search END
  WHERE id = @id::int
  RETURNING *;

-- name: SetProviderAnnotation :execrows
UPDATE provider SET annotations = CASE
    WHEN sqlc.narg('value')::text IS NULL THEN annotations - sqlc.arg('key')::text
    ELSE annotations || jsonb_build_object(sqlc.arg('key')::text, sqlc.narg('value')::text)
  END
WHERE id = sqlc.arg('id')::int;

-- name: DeleteProvider :exec
DELETE FROM provider WHERE id = $1;
