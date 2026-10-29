-- name: ListTraceBackfillCandidates :many
WITH request_traces AS (
  SELECT parent_span_id, user_id, MIN(created_at)::timestamp AS first_request_at, MAX(created_at)::timestamp AS last_request_at
  FROM request
  WHERE parent_span_id IS NOT NULL AND parent_span_id <> '' AND user_id IS NOT NULL
  GROUP BY parent_span_id, user_id
)
SELECT request_traces.parent_span_id, request_traces.user_id, request_traces.first_request_at, request_traces.last_request_at
FROM request_traces
LEFT JOIN traces ON traces.parent_span_id = request_traces.parent_span_id
  AND traces.user_id = request_traces.user_id
WHERE traces.parent_span_id IS NULL
  OR traces.first_request_at > request_traces.first_request_at
  OR traces.last_request_at < request_traces.last_request_at
ORDER BY request_traces.first_request_at, request_traces.parent_span_id;

-- name: BackfillTrace :exec
INSERT INTO traces (id, parent_span_id, user_id, first_request_at, last_request_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (parent_span_id, user_id) DO UPDATE
SET first_request_at = LEAST(traces.first_request_at, EXCLUDED.first_request_at),
    last_request_at = GREATEST(traces.last_request_at, EXCLUDED.last_request_at),
    updated_at = CURRENT_TIMESTAMP;

-- name: UpsertTrace :one
INSERT INTO traces (id, parent_span_id, user_id, first_request_at, last_request_at)
VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (parent_span_id, user_id) DO UPDATE
SET first_request_at = LEAST(traces.first_request_at, EXCLUDED.first_request_at),
    last_request_at = GREATEST(traces.last_request_at, EXCLUDED.last_request_at),
    updated_at = CURRENT_TIMESTAMP
RETURNING id, parent_span_id, first_request_at, last_request_at;
