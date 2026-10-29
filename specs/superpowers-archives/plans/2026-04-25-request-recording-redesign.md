# Request Recording Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace single-phase request INSERT with a two-phase write model: meta request on arrival, upstream request per attempt, two-stage backfill (header → complete).

**Architecture:** Every client request creates a meta request row (span_id = self). Each upstream attempt creates an upstream request row (span_id = meta.id). Meta request is backfilled when upstream header arrives (provider_id, model, etc.) and again when the gateway flow completes (status_code, time_spent_ms, etc.).

**Tech Stack:** Go 1.26, PostgreSQL 17, sqlc, goose migrations, pgx/v5, Huma v2

---

### Task 1: Add migration 003 — nullable provider_id + type/status columns

**Goal:** Alter the request table to support meta requests and lifecycle tracking.

**Files:**
- Create: `db/migrations/003_request_type_status.sql`

**Acceptance Criteria:**
- [ ] `provider_id`, `endpoint_path`, `status_code`, `time_spent_ms` are nullable (meta requests start with these as NULL)
- [ ] `type` column exists as INTEGER NOT NULL DEFAULT 1
- [ ] `status` column exists as INTEGER NOT NULL DEFAULT 0

**Verify:** `docker compose up -d && mise run server` (migrations auto-run on startup) → server starts without error

**Steps:**

- [ ] **Step 1: Create the migration file**

```sql
-- +goose Up
-- Meta requests start without a provider, endpoint, status, or time_spent
ALTER TABLE request ALTER COLUMN provider_id DROP NOT NULL;
ALTER TABLE request ALTER COLUMN endpoint_path DROP NOT NULL;
ALTER TABLE request ALTER COLUMN status_code DROP NOT NULL;
ALTER TABLE request ALTER COLUMN time_spent_ms DROP NOT NULL;
-- Type: 0=meta (client request), 1=upstream (provider request). Default 1 for backward compat.
ALTER TABLE request ADD COLUMN type INTEGER NOT NULL DEFAULT 1;
-- Status: 0=pending, 1=header_received, 2=completed, 3=failed
ALTER TABLE request ADD COLUMN status INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE request DROP COLUMN status;
ALTER TABLE request DROP COLUMN type;
ALTER TABLE request ALTER COLUMN time_spent_ms SET NOT NULL;
ALTER TABLE request ALTER COLUMN status_code SET NOT NULL;
ALTER TABLE request ALTER COLUMN endpoint_path SET NOT NULL;
ALTER TABLE request ALTER COLUMN provider_id SET NOT NULL;
```

- [ ] **Step 2: Verify migration runs**

Run: `docker compose up -d && mise run server`
Expected: server starts, logs "migrations completed"

- [ ] **Step 3: Commit**

```bash
git add db/migrations/003_request_type_status.sql
git commit -m "migration: add type/status columns and nullable provider_id to request"
```

---

### Task 2: Update sqlc queries and regenerate

**Goal:** Replace InsertRequest with full-column insert, add UpdateRequestOnHeader and UpdateRequestOnComplete, update ListRequests with type filter.

**Files:**
- Modify: `db/queries/routing.sql` (replace InsertRequest)
- Create: `db/queries/request.sql` (add update queries, modify ListRequests)
- Regenerated: `pkg/db/*.go` (via `sqlc generate`)

**Acceptance Criteria:**
- [ ] `InsertRequest` accepts all 17 columns
- [ ] `UpdateRequestOnHeader` updates provider_id, model, endpoint_path, api_key_id, status
- [ ] `UpdateRequestOnComplete` updates status_code, error_message, time_spent_ms, status
- [ ] `ListRequests` includes `type` and `status` in SELECT and supports optional `type` filter

**Verify:** `sqlc generate` succeeds with no errors, `go build ./...` compiles

**Steps:**

- [ ] **Step 1: Replace InsertRequest in routing.sql**

Edit `db/queries/routing.sql`. Remove the old `InsertRequest` query and replace with:

```sql
-- name: InsertRequest :exec
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

- [ ] **Step 2: Add update queries and modify ListRequests in request.sql**

Edit `db/queries/request.sql`. Replace entire file with:

```sql
-- name: ListRequests :many
SELECT id, span_id, parent_span_id, type, status, provider_id, endpoint_path, api_key_id, model,
       input_tokens, cache_read_tokens, output_tokens, cache_write_tokens,
       status_code, error_message, ttft_ms, time_spent_ms, created_at
FROM request
WHERE
  (sqlc.narg('type')::int IS NULL OR type = sqlc.narg('type'))
  AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id'))
  AND (sqlc.narg('endpoint_path')::text IS NULL OR endpoint_path = sqlc.narg('endpoint_path'))
  AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model'))
  AND (
    sqlc.narg('cursor_created_at')::timestamp IS NULL
    OR (created_at, id) < (sqlc.narg('cursor_created_at')::timestamp, sqlc.narg('cursor_id')::text)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.narg('limit')::int;

-- name: GetRequest :one
SELECT * FROM request WHERE id = $1;

-- name: UpdateRequestOnHeader :exec
UPDATE request
SET provider_id = $2, model = $3, endpoint_path = $4, api_key_id = $5, status = $6
WHERE id = $1;

-- name: UpdateRequestOnComplete :exec
UPDATE request
SET status_code = $2, error_message = $3, time_spent_ms = $4, status = $5
WHERE id = $1;
```

- [ ] **Step 3: Run sqlc generate**

Run: `sqlc generate`
Expected: no errors, `pkg/db/` files updated

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: compilation errors in gateway_helpers.go and contract/request.go (expected — these will be fixed in subsequent tasks)

- [ ] **Step 5: Commit**

```bash
git add db/queries/routing.sql db/queries/request.sql pkg/db/
git commit -m "sqlc: full-column InsertRequest, UpdateRequestOnHeader/Complete, type filter"
```

---

### Task 3: Add request type/status constants and fix contract types

**Goal:** Define Go constants for request type and status, update RequestView and ToRequestView to include the new fields.

**Files:**
- Create: `pkg/db/request_constants.go`
- Modify: `pkg/contract/request.go`

**Acceptance Criteria:**
- [ ] Constants `RequestTypeMeta`, `RequestTypeUpstream`, `RequestStatusPending`, `RequestStatusHeaderReceived`, `RequestStatusCompleted`, `RequestStatusFailed` exist in `pkg/db`
- [ ] `RequestView` has `Type` and `Status` fields
- [ ] `ToRequestView` maps the new columns
- [ ] `ListRequestsRequest` has optional `Type` filter
- [ ] `go build ./...` compiles

**Verify:** `go build ./...` succeeds

**Steps:**

- [ ] **Step 1: Create constants file**

Create `pkg/db/request_constants.go`:

```go
package db

// Request type distinguishes meta (client) requests from upstream (provider) requests.
const (
	RequestTypeMeta     = 0
	RequestTypeUpstream = 1
)

// Request status tracks the lifecycle of a request record.
const (
	RequestStatusPending        = 0
	RequestStatusHeaderReceived = 1
	RequestStatusCompleted      = 2
	RequestStatusFailed         = 3
)
```

- [ ] **Step 2: Update RequestView and related types in contract/request.go**

Edit `pkg/contract/request.go`. Replace the entire file with:

```go
package contract

import (
	"net/http"
	"picotera/pkg/db"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

type RequestView struct {
	ID               string  `json:"id"`
	SpanID           string  `json:"spanId,omitempty"`
	ParentSpanID     string  `json:"parentSpanId,omitempty"`
	Type             int32   `json:"type"`
	Status           int32   `json:"status"`
	ProviderID       *int32  `json:"providerId,omitempty"`
	EndpointPath     string  `json:"endpointPath,omitempty"`
	ApiKeyID         *int32  `json:"apiKeyId,omitempty"`
	Model            string  `json:"model,omitempty"`
	InputTokens      *int32  `json:"inputTokens,omitempty"`
	CacheReadTokens  *int32  `json:"cacheReadTokens,omitempty"`
	OutputTokens     *int32  `json:"outputTokens,omitempty"`
	CacheWriteTokens *int32  `json:"cacheWriteTokens,omitempty"`
	StatusCode       *int32  `json:"statusCode,omitempty"`
	ErrorMessage     string  `json:"errorMessage,omitempty"`
	TtftMs           *int32  `json:"ttftMs,omitempty"`
	TimeSpentMs      *int32  `json:"timeSpentMs,omitempty"`
	CreatedAt        string  `json:"createdAt,omitempty"`
}

func ToRequestView(r *db.Request) *RequestView {
	view := &RequestView{
		ID:   r.ID,
		Type: r.Type,
		Status: r.Status,
	}
	if r.SpanID.Valid {
		view.SpanID = r.SpanID.String
	}
	if r.ParentSpanID.Valid {
		view.ParentSpanID = r.ParentSpanID.String
	}
	if r.ProviderID.Valid {
		v := r.ProviderID.Int32
		view.ProviderID = &v
	}
	if r.EndpointPath.Valid {
		view.EndpointPath = r.EndpointPath.String
	}
	if r.ApiKeyID.Valid {
		v := r.ApiKeyID.Int32
		view.ApiKeyID = &v
	}
	if r.Model.Valid {
		view.Model = r.Model.String
	}
	if r.InputTokens.Valid {
		v := r.InputTokens.Int32
		view.InputTokens = &v
	}
	if r.CacheReadTokens.Valid {
		v := r.CacheReadTokens.Int32
		view.CacheReadTokens = &v
	}
	if r.OutputTokens.Valid {
		v := r.OutputTokens.Int32
		view.OutputTokens = &v
	}
	if r.CacheWriteTokens.Valid {
		v := r.CacheWriteTokens.Int32
		view.CacheWriteTokens = &v
	}
	if r.StatusCode.Valid {
		v := r.StatusCode.Int32
		view.StatusCode = &v
	}
	if r.ErrorMessage.Valid {
		view.ErrorMessage = r.ErrorMessage.String
	}
	if r.TtftMs.Valid {
		v := r.TtftMs.Int32
		view.TtftMs = &v
	}
	if r.TimeSpentMs.Valid {
		v := r.TimeSpentMs.Int32
		view.TimeSpentMs = &v
	}
	if r.CreatedAt.Valid {
		view.CreatedAt = r.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return view
}

type ListRequestsRequest struct {
	PaginationRequest
	Type         *int32 `query:"type,omitempty"`
	ProviderID   int32  `query:"providerId,omitempty"`
	EndpointPath string `query:"endpointPath,omitempty"`
	Model        string `query:"model,omitempty"`
}

type ListRequestsResponse = PaginatedResponse[RequestView]

type GetRequestRequest struct {
	ID string `path:"id"`
}

type GetRequestResponse struct {
	Body RequestView
}

var OperationListRequests = huma.Operation{
	OperationID: "listRequests",
	Method:      http.MethodGet,
	Path:        "/requests",
	Summary:     "List requests",
}

var OperationGetRequest = huma.Operation{
	OperationID: "getRequest",
	Method:      http.MethodGet,
	Path:        "/requests/{id}",
	Summary:     "Get a request by ID",
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: compilation errors only in `handle_requests.go` (ListRequestsParams changed) and `gateway_helpers.go` (InsertRequestParams changed) — these are fixed in subsequent tasks

- [ ] **Step 4: Commit**

```bash
git add pkg/db/request_constants.go pkg/contract/request.go
git commit -m "contract: add request type/status, update RequestView for nullable fields"
```

---

### Task 4: Update handle_requests.go for new ListRequests params

**Goal:** Fix the request handler to use the new ListRequestsParams shape (nullable provider_id, type filter, nullable endpoint_path/model/status_code/time_spent_ms).

**Files:**
- Modify: `pkg/server/handle_requests.go`

**Acceptance Criteria:**
- [ ] `handleListRequests` passes the `Type` filter to the query
- [ ] All nullable fields handled correctly
- [ ] `go build ./...` compiles

**Verify:** `go build ./...` succeeds

**Steps:**

- [ ] **Step 1: Update handle_requests.go**

Edit `pkg/server/handle_requests.go`. Replace the entire file with:

```go
package server

import (
	"context"
	"errors"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/errorx"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Server) handleListRequests(ctx context.Context, input *contract.ListRequestsRequest) (*contract.ListRequestsResponse, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	fetchLimit := limit + 1

	var cursorCreatedAt pgtype.Timestamp
	var cursorID pgtype.Text
	if input.Cursor != "" {
		var createdAt, id string
		if err := contract.DecodeCursor(input.Cursor, "createdAt", &createdAt, "id", &id); err != nil {
			return nil, huma.Error400BadRequest("invalid cursor", err)
		}
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid cursor", err)
		}
		cursorCreatedAt = pgtype.Timestamp{Time: t.UTC(), Valid: true}
		cursorID = pgtype.Text{String: id, Valid: true}
	}

	var filterType pgtype.Int4
	if input.Type != nil {
		filterType = pgtype.Int4{Int32: *input.Type, Valid: true}
	}
	var filterProviderID pgtype.Int4
	if input.ProviderID != 0 {
		filterProviderID = pgtype.Int4{Int32: input.ProviderID, Valid: true}
	}
	var filterEndpointPath pgtype.Text
	if input.EndpointPath != "" {
		filterEndpointPath = pgtype.Text{String: input.EndpointPath, Valid: true}
	}
	var filterModel pgtype.Text
	if input.Model != "" {
		filterModel = pgtype.Text{String: input.Model, Valid: true}
	}

	rows, err := s.queries.ListRequests(ctx, db.ListRequestsParams{
		Type:            filterType,
		ProviderID:      filterProviderID,
		EndpointPath:    filterEndpointPath,
		Model:           filterModel,
		CursorCreatedAt: cursorCreatedAt,
		CursorID:        cursorID,
		Limit:           pgtype.Int4{Int32: fetchLimit, Valid: true},
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list requests", err)
	}

	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}

	items := make([]contract.RequestView, len(rows))
	for i, row := range rows {
		items[i] = *contract.ToRequestView(&row)
	}

	pagination := contract.PaginationInfo{HasMore: hasMore}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		createdAt := ""
		if last.CreatedAt.Valid {
			createdAt = last.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
		}
		cursor, err := contract.EncodeCursor("createdAt", createdAt, "id", last.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to encode cursor", err)
		}
		pagination.NextCursor = cursor
	}

	return &contract.ListRequestsResponse{
		Body: contract.PaginatedBody[contract.RequestView]{
			Items:      items,
			Pagination: pagination,
		},
	}, nil
}

func (s *Server) handleGetRequest(ctx context.Context, input *contract.GetRequestRequest) (*contract.GetRequestResponse, error) {
	req, err := s.queries.GetRequest(ctx, input.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("request not found", errorx.RequestNotFound)
		}
		return nil, huma.Error500InternalServerError("failed to get request", err)
	}
	return &contract.GetRequestResponse{Body: *contract.ToRequestView(&req)}, nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: compilation errors only in `gateway_helpers.go` (InsertRequestParams + logRequest changed) — fixed in Task 5

- [ ] **Step 3: Commit**

```bash
git add pkg/server/handle_requests.go
git commit -m "handler: update ListRequests for nullable fields and type filter"
```

---

### Task 5: Rewrite gateway recording logic — meta/upstream span model

**Goal:** Replace the old `logRequest` with the two-phase span model. Insert meta request on arrival, upstream request per attempt, backfill on header success and on completion.

**Files:**
- Modify: `pkg/server/gateway_helpers.go`
- Modify: `pkg/server/handle_gateway.go`

**Acceptance Criteria:**
- [ ] Meta request inserted immediately on client arrival (type=meta, status=pending)
- [ ] Upstream request inserted before each attempt (type=upstream, status=pending, span_id=meta.id)
- [ ] On upstream header success: UpdateRequestOnHeader called for both meta and upstream
- [ ] On upstream completion: UpdateRequestOnComplete called for upstream, then for meta
- [ ] On upstream failure: UpdateRequestOnComplete called for upstream (failed), continue failover
- [ ] On all-fail: UpdateRequestOnComplete called for meta (failed, 502)
- [ ] `go build ./...` compiles

**Verify:** `go build ./...` succeeds

**Steps:**

- [ ] **Step 1: Replace logRequest in gateway_helpers.go**

Edit `pkg/server/gateway_helpers.go`. Remove the old `logRequest` function and add three new helpers in its place:

```go
// insertRequest inserts a request record. Errors are logged but do not affect the response.
func (s *Server) insertRequest(ctx context.Context, arg db.InsertRequestParams) {
	if err := s.queries.InsertRequest(ctx, arg); err != nil {
		logx.WithContext(ctx).WithError(err).Error("failed to insert request")
	}
}

// updateRequestOnHeader backfills provider and request metadata. Errors are logged but do not affect the response.
func (s *Server) updateRequestOnHeader(ctx context.Context, arg db.UpdateRequestOnHeaderParams) {
	if err := s.queries.UpdateRequestOnHeader(ctx, arg); err != nil {
		logx.WithContext(ctx).WithError(err).Error("failed to update request on header")
	}
}

// updateRequestOnComplete backfills result fields. Errors are logged but do not affect the response.
func (s *Server) updateRequestOnComplete(ctx context.Context, arg db.UpdateRequestOnCompleteParams) {
	if err := s.queries.UpdateRequestOnComplete(ctx, arg); err != nil {
		logx.WithContext(ctx).WithError(err).Error("failed to update request on complete")
	}
}
```

- [ ] **Step 2: Rewrite handle_gateway.go**

Edit `pkg/server/handle_gateway.go`. Replace the entire file with:

```go
package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"picotera/pkg/db"
	"picotera/pkg/errorx"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/xid"
)

type gatewayHandler struct {
	*Server
}

var _ http.Handler = (*gatewayHandler)(nil)

func (h *gatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	gatewayStart := time.Now()
	bgCtx := context.Background()

	// 1. Read request body
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		writeGatewayError(w, http.StatusInternalServerError, "failed to read request body", errorx.InternalError.Error())
		return
	}

	// 2. Match endpoint by path
	endpoint, err := h.resolveEndpoint(r.Context(), r.URL.Path)
	if err != nil {
		handleGatewayErr(w, err)
		return
	}

	// 3. Check credentials_resolver (only generalApiKey = 1 is supported in v1)
	if endpoint.CredentialsResolver != credentialsResolverGeneralAPIKey {
		writeGatewayError(w, http.StatusInternalServerError,
			fmt.Sprintf("unsupported credentials resolver: %d", endpoint.CredentialsResolver),
			errorx.InternalError.Error())
		return
	}

	// 4. Resolve auth type from client headers
	authTyp, err := resolveAuthType(r)
	if err != nil {
		handleGatewayErr(w, err)
		return
	}

	// 5. Extract model name from request body
	model, err := extractModel(body, endpoint.ModelPath)
	if err != nil {
		handleGatewayErr(w, err)
		return
	}

	// 6. Resolve providers
	providers, err := h.resolveProviders(r.Context(), endpoint.Path, model)
	if err != nil {
		handleGatewayErr(w, err)
		return
	}

	// 7. Insert meta request (client/downstream)
	metaID := xid.New().String()
	h.insertRequest(bgCtx, db.InsertRequestParams{
		ID:           metaID,
		SpanID:       pgtype.Text{String: metaID, Valid: true},
		ParentSpanID: pgtype.Text{Valid: false},
		Type:         db.RequestTypeMeta,
		Status:       db.RequestStatusPending,
		ProviderID:   pgtype.Int4{Valid: false},
		EndpointPath: pgtype.Text{Valid: false},
		ApiKeyID:     pgtype.Int4{Valid: false},
		Model:        pgtype.Text{Valid: false},
		StatusCode:   pgtype.Int4{Valid: false},
		ErrorMessage: pgtype.Text{Valid: false},
		TimeSpentMs:  pgtype.Int4{Valid: false},
	})

	// 8. Try each provider with failover
	var lastErr error
	for _, provider := range providers {
		attemptStart := time.Now()
		ctx, cancel := context.WithCancel(r.Context())

		// Insert upstream request
		upstreamID := xid.New().String()
		h.insertRequest(bgCtx, db.InsertRequestParams{
			ID:           upstreamID,
			SpanID:       pgtype.Text{String: metaID, Valid: true},
			ParentSpanID: pgtype.Text{Valid: false},
			Type:         db.RequestTypeUpstream,
			Status:       db.RequestStatusPending,
			ProviderID:   pgtype.Int4{Int32: provider.ProviderID, Valid: true},
			EndpointPath: pgtype.Text{String: endpoint.Path, Valid: true},
			ApiKeyID:     pgtype.Int4{Valid: false},
			Model:        pgtype.Text{String: model, Valid: true},
			StatusCode:   pgtype.Int4{Valid: false},
			ErrorMessage: pgtype.Text{Valid: false},
			TimeSpentMs:  pgtype.Int4{Valid: false},
		})

		// Determine upstream model name
		upstreamModel := ""
		if provider.UpstreamModelName.Valid {
			upstreamModel = provider.UpstreamModelName.String
		}

		// Determine provider credentials
		creds := ""
		if provider.ProviderCredentials.Valid {
			creds = provider.ProviderCredentials.String
		}

		// Build upstream request
		req, err := buildUpstreamRequest(ctx, r, body, provider.UpstreamUrl.String, upstreamModel, creds, authTyp)
		if err != nil {
			cancel()
			timeSpent := int32(time.Since(attemptStart).Milliseconds())
			h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
				ID:          upstreamID,
				StatusCode:  pgtype.Int4{Int32: 0, Valid: true},
				ErrorMessage: pgtype.Text{String: err.Error(), Valid: true},
				TimeSpentMs: pgtype.Int4{Int32: timeSpent, Valid: true},
				Status:      db.RequestStatusFailed,
			})
			lastErr = err
			continue
		}

		// Forward request
		resp, err := h.forwardRequest(req)
		if err != nil {
			cancel()
			timeSpent := int32(time.Since(attemptStart).Milliseconds())
			h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
				ID:          upstreamID,
				StatusCode:  pgtype.Int4{Int32: 0, Valid: true},
				ErrorMessage: pgtype.Text{String: err.Error(), Valid: true},
				TimeSpentMs: pgtype.Int4{Int32: timeSpent, Valid: true},
				Status:      db.RequestStatusFailed,
			})
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusOK {
			// Backfill meta and upstream on header success
			h.updateRequestOnHeader(bgCtx, db.UpdateRequestOnHeaderParams{
				ID:           metaID,
				ProviderID:   pgtype.Int4{Int32: provider.ProviderID, Valid: true},
				Model:        pgtype.Text{String: model, Valid: true},
				EndpointPath: pgtype.Text{String: endpoint.Path, Valid: true},
				ApiKeyID:     pgtype.Int4{Valid: false},
				Status:       db.RequestStatusHeaderReceived,
			})
			h.updateRequestOnHeader(bgCtx, db.UpdateRequestOnHeaderParams{
				ID:           upstreamID,
				ProviderID:   pgtype.Int4{Int32: provider.ProviderID, Valid: true},
				Model:        pgtype.Text{String: model, Valid: true},
				EndpointPath: pgtype.Text{String: endpoint.Path, Valid: true},
				ApiKeyID:     pgtype.Int4{Valid: false},
				Status:       db.RequestStatusHeaderReceived,
			})

			// Stream response to client, stripping Content-Length since we're chunk-copying
			for key, values := range resp.Header {
				if strings.ToLower(key) == "content-length" {
					continue
				}
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(http.StatusOK)

			reader := newIdleTimeoutReader(resp.Body, h.config.GatewayReadTimeout, cancel)
			flusher, canFlush := w.(http.Flusher)
			buf := make([]byte, 32*1024)
			for {
				n, readErr := reader.Read(buf)
				if n > 0 {
					w.Write(buf[:n])
					if canFlush {
						flusher.Flush()
					}
				}
				if readErr != nil {
					break
				}
			}
			cancel()
			resp.Body.Close()

			// Complete upstream request
			upstreamTimeSpent := int32(time.Since(attemptStart).Milliseconds())
			h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
				ID:          upstreamID,
				StatusCode:  pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true},
				ErrorMessage: pgtype.Text{Valid: false},
				TimeSpentMs: pgtype.Int4{Int32: upstreamTimeSpent, Valid: true},
				Status:      db.RequestStatusCompleted,
			})

			// Complete meta request
			metaTimeSpent := int32(time.Since(gatewayStart).Milliseconds())
			h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
				ID:          metaID,
				StatusCode:  pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true},
				ErrorMessage: pgtype.Text{Valid: false},
				TimeSpentMs: pgtype.Int4{Int32: metaTimeSpent, Valid: true},
				Status:      db.RequestStatusCompleted,
			})
			return
		}

		// Non-200 response: read body, complete upstream, try next provider
		cancel()
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		timeSpent := int32(time.Since(attemptStart).Milliseconds())
		errMsg := string(respBody)
		h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
			ID:          upstreamID,
			StatusCode:  pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true},
			ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
			TimeSpentMs: pgtype.Int4{Int32: timeSpent, Valid: true},
			Status:      db.RequestStatusFailed,
		})
		lastErr = fmt.Errorf("upstream returned %d: %s", resp.StatusCode, errMsg)
	}

	// 9. All providers failed — complete meta request
	errMsg := "all providers failed"
	if lastErr != nil {
		errMsg = lastErr.Error()
	}
	metaTimeSpent := int32(time.Since(gatewayStart).Milliseconds())
	h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
		ID:          metaID,
		StatusCode:  pgtype.Int4{Int32: http.StatusBadGateway, Valid: true},
		ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
		TimeSpentMs: pgtype.Int4{Int32: metaTimeSpent, Valid: true},
		Status:      db.RequestStatusFailed,
	})
	writeGatewayError(w, http.StatusBadGateway, errMsg, errorx.UpstreamError.Error())
}
```

- [ ] **Step 3: Clean up unused imports in gateway_helpers.go**

After removing `logRequest`, the `xid` import is no longer needed in `gateway_helpers.go`. Remove it from the import block if present. Also remove any other imports that were only used by `logRequest` (the `xid` import should be removed — it was used in `logRequest` for `xid.New().String()`).

Edit `pkg/server/gateway_helpers.go`. Remove `"github.com/rs/xid"` from the import block.

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: success

- [ ] **Step 5: Commit**

```bash
git add pkg/server/gateway_helpers.go pkg/server/handle_gateway.go
git commit -m "gateway: meta/upstream span model with two-phase backfill"
```

---

### Task 6: Regenerate OpenAPI spec

**Goal:** Update openapi.yaml to reflect the new type/status fields and type filter on the list endpoint.

**Files:**
- Modified: `openapi.yaml`
- Modified: `dashboard/src/api.d.ts` (via openapi-typescript)

**Acceptance Criteria:**
- [ ] `openapi.yaml` includes `type` and `status` in RequestView schema
- [ ] `openapi.yaml` includes `type` query param on listRequests
- [ ] `dashboard/src/api.d.ts` regenerated

**Verify:** `mise run openapi && pnpm --dir dashboard type-check` both succeed

**Steps:**

- [ ] **Step 1: Regenerate openapi.yaml**

Run: `mise run openapi`
Expected: `openapi.yaml` updated with new fields

- [ ] **Step 2: Regenerate dashboard types**

Run: `pnpm --dir dashboard type-check` (or the openapi-typescript command if separate)
Expected: `dashboard/src/api.d.ts` updated

- [ ] **Step 3: Verify dashboard type-check**

Run: `pnpm --dir dashboard type-check`
Expected: success

- [ ] **Step 4: Commit**

```bash
git add openapi.yaml dashboard/src/api.d.ts
git commit -m "openapi: add type/status fields to request schema"
```

---

### Task 7: Smoke test end-to-end

**Goal:** Verify the full flow works: server starts, gateway request creates meta + upstream rows, backfill updates happen correctly.

**Files:** None (testing only)

**Acceptance Criteria:**
- [ ] Server starts without error
- [ ] Gateway request creates a meta request (type=0) and at least one upstream request (type=1)
- [ ] Successful request: meta row has status=2, upstream row has status=2
- [ ] Failed request: upstream row has status=3, meta row has status=3

**Verify:** Manual test via curl + database query

**Steps:**

- [ ] **Step 1: Start infrastructure and server**

Run: `docker compose up -d && mise run server`
Expected: server listening on port 9898

- [ ] **Step 2: Send a gateway request (will likely fail if no providers configured, which is fine)**

Run: `curl -s -X POST http://localhost:9898/v1/chat/completions -H "Authorization: Bearer test" -H "Content-Type: application/json" -d '{"model":"test","messages":[{"role":"user","content":"hi"}]}'`
Expected: 404 or 502 response (no route/provider configured)

- [ ] **Step 3: Check request rows in database**

Run: `docker compose exec postgres psql -U picotera -d picotera -c "SELECT id, span_id, type, status, provider_id, status_code, time_spent_ms FROM request ORDER BY created_at DESC LIMIT 10;"`
Expected: see rows with type=0 (meta) and type=1 (upstream) if a provider was attempted, or just a meta row with status=3 if resolution failed before attempting

- [ ] **Step 4: Verify span relationship**

Run: `docker compose exec postgres psql -U picotera -d picotera -c "SELECT r1.id AS meta_id, r2.id AS upstream_id, r2.span_id AS upstream_span FROM request r1 LEFT JOIN request r2 ON r2.span_id = r1.id WHERE r1.type = 0;"`
Expected: upstream rows have span_id matching their meta request's id

- [ ] **Step 5: Final commit (if any fixes were needed)**

```bash
git add -A
git commit -m "fix: address smoke test issues"
```
