# Request Recording Redesign

## Problem

Current request recording is a single-phase INSERT after the upstream response completes. It writes only a subset of columns (id, provider_id, endpoint_path, model, status_code, error_message, time_spent_ms) and has no concept of span relationships or request lifecycle. If the process crashes mid-request, the record is lost entirely.

## Design

### Core Model: Meta Request + Upstream Requests

Every client (downstream) request produces a **meta request** row. Each upstream attempt to a provider produces an **upstream request** row. Both share the `request` table, distinguished by a `type` column. The meta request's `span_id` equals its own `id`; upstream requests' `span_id` equals the meta request's `id`. `parent_span_id` is reserved for future use (NULL for now).

### Lifecycle

Requests progress through states: `pending` → `header_received` → `completed`/`failed`. The meta request is updated in two phases: first when an upstream header succeeds, then when the full gateway flow finishes.

### Schema Changes (Migration 003)

```sql
-- Make provider_id nullable (meta requests start without a provider)
ALTER TABLE request ALTER COLUMN provider_id DROP NOT NULL;

-- Add type column: 0=meta, 1=upstream (default 1 for backward compat)
ALTER TABLE request ADD COLUMN type INTEGER NOT NULL DEFAULT 1;

-- Add status column: 0=pending, 1=header_received, 2=completed, 3=failed
ALTER TABLE request ADD COLUMN status INTEGER NOT NULL DEFAULT 0;
```

### Go Constants

```go
// Request type
const (
    RequestTypeMeta     = 0 // Client/downstream request
    RequestTypeUpstream = 1 // Upstream/provider request
)

// Request status
const (
    RequestStatusPending        = 0 // Written, awaiting processing
    RequestStatusHeaderReceived = 1 // Upstream header returned
    RequestStatusCompleted      = 2 // Request finished successfully
    RequestStatusFailed         = 3 // Request failed
)
```

### sqlc Query Changes

Replace `InsertRequest` with a full-column insert, add two update queries:

**InsertRequest** — inserts all columns (unknowns passed as NULL/defaults):
```sql
INSERT INTO request (
  id, span_id, parent_span_id, type, status,
  provider_id, endpoint_path, api_key_id, model,
  input_tokens, cache_read_tokens, output_tokens, cache_write_tokens,
  status_code, error_message, ttft_ms, time_spent_ms
) VALUES (
  $1, $2, $3, $4, $5,
  $6, $7, $8, $9,
  $10, $11, $12, $13,
  $14, $15, $16, $17
);
```

**UpdateRequestOnHeader** — backfills provider and request metadata:
```sql
UPDATE request
SET provider_id = $2, model = $3, endpoint_path = $4, api_key_id = $5, status = $6
WHERE id = $1;
```

**UpdateRequestOnComplete** — backfills result fields:
```sql
UPDATE request
SET status_code = $2, error_message = $3, time_spent_ms = $4, status = $5
WHERE id = $1;
```

**ListRequests** — add `type` and `status` columns to SELECT; add optional filter on `type`.

### Gateway Flow (handle_gateway.go)

1. **Client request arrives** → `InsertRequest` meta request:
   - `id` = xid, `span_id` = same as `id`, `parent_span_id` = NULL
   - `type` = 0 (meta), `status` = 0 (pending)
   - `provider_id` = NULL, other unknowns = NULL/defaults

2. **Before each upstream attempt** → `InsertRequest` upstream request:
   - `id` = xid, `span_id` = meta.id, `parent_span_id` = NULL
   - `type` = 1 (upstream), `status` = 0 (pending)
   - `provider_id` = current provider's ID

3. **Upstream header succeeds (200)** → `UpdateRequestOnHeader`:
   - Meta request: set `provider_id`, `model`, `endpoint_path`, `api_key_id`, `status=1`
   - Upstream request: set `status=1`

4. **Upstream request completes** → `UpdateRequestOnComplete`:
   - Upstream request: set `status_code`, `error_message`, `time_spent_ms`, `status=2` or `3`

5. **Upstream fails (non-200/error/timeout)** → `UpdateRequestOnComplete`:
   - Upstream request: `status=3` with error details
   - Continue to next provider (go to step 2)

6. **Gateway flow ends (success)** → `UpdateRequestOnComplete`:
   - Meta request: `status=2`, `status_code` from successful upstream, `time_spent_ms` = wall time since client request arrived (not copied from upstream)

7. **All providers failed** → `UpdateRequestOnComplete`:
   - Meta request: `status=3`, status_code=502

### API Changes

- `RequestView` gains `type` and `status` integer fields
- `ListRequests` supports optional `type` filter parameter
- `ToRequestView` maps the new columns

### Out of Scope

- Token count extraction (input_tokens, output_tokens, cache_read/write_tokens)
- TTFT extraction (ttft_ms)
- parent_span_id usage
- Dashboard UI changes
