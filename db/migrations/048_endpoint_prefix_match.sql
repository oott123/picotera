-- +goose Up
-- Prefix-matching endpoints: the part of the request path after the endpoint's
-- prefix is appended verbatim to the upstream URL. Existing rows default to
-- FALSE, so their matching behaviour is unchanged.
ALTER TABLE endpoint ADD COLUMN prefix_match BOOLEAN NOT NULL DEFAULT FALSE;

-- The two Codex-only endpoint types (11 codexCompact, 12 codexSearchV1Alpha)
-- are gone: a Codex upstream is now a single `codex` (14) prefix endpoint whose
-- sub-paths are carried by the request path. Rows still carrying them fall back
-- to `general` (1).
UPDATE endpoint SET endpoint_type = 1 WHERE endpoint_type IN (11, 12);

-- completion_endpoint_path rebuilt for the codex type. Column structure is
-- unchanged, so a plain replace works; the view is only referenced by queries,
-- never by a continuous aggregate.
--
-- The endpoint-table whitelist drops 11 (deprecated) and does NOT gain 14 as a
-- bare path: a codex endpoint row records `path || suffix`, so the two chat-ish
-- sub-paths are expanded explicitly. `path || '/alpha/search'` stays OUT —
-- search responses have output_tokens = 0 by nature and would be counted as
-- empty replies.
CREATE OR REPLACE VIEW completion_endpoint_path AS
SELECT path AS path FROM endpoint WHERE endpoint_type = ANY(ARRAY[2,3,4,7,8]::int[])
UNION ALL
SELECT e.path || s.suffix AS path
FROM endpoint AS e
CROSS JOIN unnest(ARRAY['/responses', '/responses/compact']::text[]) AS s(suffix)
WHERE e.endpoint_type = 14
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
-- Note: the endpoint_type 11/12 → 1 update above is NOT reversible — which rows
-- originally carried which of the two deprecated types is no longer recorded.
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

ALTER TABLE endpoint DROP COLUMN prefix_match;
