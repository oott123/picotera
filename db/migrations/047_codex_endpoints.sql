-- +goose Up
-- Widen the completion / chat endpoint scope for the Codex routes added in
-- pkg/server/unified_routes.go. codexCompact (11) belongs to the scope — its
-- responses carry output tokens, so success-rate / empty-reply stats are
-- meaningful. codexSearchV1Alpha (12) and /api/unified/v1/alpha/search stay
-- OUT: search responses have output_tokens = 0 by nature and would be counted
-- as empty replies. Column structure is unchanged, so a plain replace works;
-- the view is only referenced by queries, never by a continuous aggregate.
CREATE OR REPLACE VIEW completion_endpoint_path AS
SELECT path AS path FROM endpoint WHERE endpoint_type = ANY(ARRAY[2,3,4,7,8,11]::int[])
UNION ALL
SELECT unnest(ARRAY[
  '/api/unified/v1/messages',
  '/api/unified/v1/responses',
  '/api/unified/v1/chat/completions',
  '/api/unified/v1beta/models/{model}:generateContent',
  '/api/unified/v1beta/models/{model}:streamGenerateContent',
  '/api/unified/codex/responses',
  '/api/unified/codex/responses/compact'
]::text[]) AS path;

-- +goose Down
CREATE OR REPLACE VIEW completion_endpoint_path AS
SELECT path AS path FROM endpoint WHERE endpoint_type = ANY(ARRAY[2,3,4,7,8]::int[])
UNION ALL
SELECT unnest(ARRAY[
  '/api/unified/v1/messages',
  '/api/unified/v1/responses',
  '/api/unified/v1/chat/completions',
  '/api/unified/v1beta/models/{model}:generateContent',
  '/api/unified/v1beta/models/{model}:streamGenerateContent'
]::text[]) AS path;
