# 执行计划

## 1. 数据库迁移

创建 `db/migrations/042_request_external_ids.sql`：

```sql
-- +goose Up
ALTER TABLE request ADD COLUMN external_request_id TEXT;
ALTER TABLE request ADD COLUMN external_response_id TEXT;

-- +goose Down
ALTER TABLE request DROP COLUMN IF EXISTS external_response_id;
ALTER TABLE request DROP COLUMN IF EXISTS external_request_id;
```

## 2. 配置

`pkg/configx/configx.go`：

- `Config` 结构体加两个字段（放在 `Auth` 之前，与其他 `Gateway*` 字段相邻）：
  ```go
  GatewayExternalRequestIDHeaders  string `mapstructure:"gateway_external_request_id_headers"`
  GatewayExternalResponseIDHeaders string `mapstructure:"gateway_external_response_id_headers"`
  ```
- `Parse()` 中 `viper.SetDefault` 加：
  ```go
  viper.SetDefault("gateway_external_request_id_headers", "X-Request-Id,X-Log-Id,Cf-Ray")
  viper.SetDefault("gateway_external_response_id_headers", "X-Request-Id,X-Log-Id,Cf-Ray")
  ```

## 3. Server：解析 header 名列表 + 匹配辅助函数

`pkg/server/server.go`：

- `Server` 结构体加：
  ```go
  externalRequestIDHeaders  []string
  externalResponseIDHeaders []string
  ```
- `NewServer` 中 config 解析后加：
  ```go
  reqHeaders, err := parseExternalIDHeaderNames(config.GatewayExternalRequestIDHeaders)
  if err != nil {
      return nil, fmt.Errorf("invalid gateway_external_request_id_headers: %w", err)
  }
  respHeaders, err := parseExternalIDHeaderNames(config.GatewayExternalResponseIDHeaders)
  if err != nil {
      return nil, fmt.Errorf("invalid gateway_external_response_id_headers: %w", err)
  }
  server.externalRequestIDHeaders = reqHeaders
  server.externalResponseIDHeaders = respHeaders
  ```

新建 `pkg/server/external_ids.go`：

```go
package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// parseExternalIDHeaderNames splits a comma-separated header name list,
// trimming whitespace from each entry. An empty string produces an empty
// slice (feature off). A non-empty string with an empty entry (e.g. "a,,b")
// is rejected.
func parseExternalIDHeaderNames(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name == "" {
			return nil, fmt.Errorf("empty header name in %q", s)
		}
		names = append(names, name)
	}
	return names, nil
}

// matchExternalIDHeader returns the first non-empty value among the named
// headers, matched in order. http.Header.Get uses canonical MIME header
// lookup so matching is case-insensitive.
func matchExternalIDHeader(header http.Header, names []string) pgtype.Text {
	for _, name := range names {
		if v := header.Get(name); v != "" {
			return pgtype.Text{String: v, Valid: true}
		}
	}
	return pgtype.Text{Valid: false}
}
```


## 4. sqlc 查询

### `db/queries/routing.sql` — `InsertRequest`

列清单末尾（`user_id` 之后）加 `external_request_id, external_response_id`，VALUES 加 `$23, $24`：

```sql
INSERT INTO request (
  id, span_id, parent_span_id, type,
  provider_id, endpoint_path, api_key_id, model, upstream_model,
  input_tokens, cache_read_tokens, output_tokens, cache_write_tokens, cache_write_1h_tokens,
  status_code, error_message, ttft_ms, time_spent_ms,
  user_message_preview, project_id, created_at, user_id,
  external_request_id, external_response_id
) VALUES (
  $1, $2, $3, $4,
  $5, $6, $7, $8, $9,
  $10, $11, $12, $13, $14,
  $15, $16, $17, $18,
  $19, $20, $21, $22,
  $23, $24
)
RETURNING created_at;
```

### `db/queries/request.sql`

- `ListRequests`：SELECT 列清单 `r.user_id` 后加 `r.external_request_id, r.external_response_id`。
- `ListRequestsBySpan`：SELECT 列清单 `r.user_id` 后加 `r.external_request_id, r.external_response_id`。
- `UpdateRequest`：`user_message_preview` 行末尾加逗号，其后加：
  ```sql
  external_response_id = CASE WHEN sqlc.arg('set_external_response_id')::bool THEN sqlc.narg('external_response_id')::text ELSE external_response_id END
  ```
  （即替换原 `user_message_preview` 行的末尾无逗号为有逗号，再插入新行。`WHERE` 子句不变。）

### 运行 sqlc generate

```bash
sqlc generate
```

确认 `pkg/db` 中：
- `InsertRequestParams` 末尾新增 `ExternalRequestID pgtype.Text`、`ExternalResponseID pgtype.Text`。
- `UpdateRequestParams` 新增 `SetExternalResponseID bool` + `ExternalResponseID pgtype.Text`。
- `Request` model、`GetRequestRow`、`ListRequestsRow`、`ListRequestsBySpanRow` 各新增两字段。

## 5. requestUpdate builder

`pkg/server/request_update.go`：

在 `UserMessagePreview` 之后加：

```go
func (u *requestUpdate) ExternalResponseID(v pgtype.Text) *requestUpdate {
	u.p.SetExternalResponseID = true
	u.p.ExternalResponseID = v
	return u
}
```

## 6. 记录 external_request_id（meta 行 insert）

`pkg/server/gateway_flow.go` `insertMetaRequest`：

`InsertRequestParams` 字面量末尾（`UserID` 之后）加：

```go
ExternalRequestID:  matchExternalIDHeader(f.r.Header, f.h.externalRequestIDHeaders),
ExternalResponseID: pgtype.Text{Valid: false},
```

`pkg/server/gateway_flow_attempts.go` `insertUpstreamAttempt`：

`InsertRequestParams` 字面量末尾加：

```go
ExternalRequestID:  pgtype.Text{Valid: false},
ExternalResponseID: pgtype.Text{Valid: false},
```

## 7. 记录 external_response_id（完成路径）

### 7a. `failMeta` 签名变更

`pkg/server/gateway_flow_errors.go`：

```go
func (f *gatewayFlow) failMeta(status int32, errMsg string, finishReason int32, respHeader http.Header) {
	if f.meta.ID == "" {
		return
	}
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	var extRespID pgtype.Text
	if respHeader != nil {
		extRespID = matchExternalIDHeader(respHeader, f.h.externalResponseIDHeaders)
	}
	f.h.updateRequest(pctx, newRequestUpdate(f.meta.ID, f.meta.CreatedAt).
		StatusCode(pgtype.Int4{Int32: status, Valid: true}).
		ErrorMessage(pgtype.Text{String: errMsg, Valid: true}).
		TimeSpentMs(pgtype.Int4{Int32: int32(time.Since(f.startedAt).Milliseconds()), Valid: true}).
		FinishReason(pgtype.Int4{Int32: finishReason, Valid: true}).
		ExternalResponseID(extRespID))
}
```

更新所有 `failMeta` 调用方（均在此文件或 `gateway_flow.go`）：

- `authenticateAndBackfill`（`gateway_flow.go`）：两处 `f.failMeta(...)` 末尾加 `, nil`。
- `failGatewayErrorWithFallback`：两处 `f.failMeta(...)` 末尾加 `, nil`。
- `failHook`：`f.failMeta(...)` 末尾加 `, nil`。
- `failInternal`：`f.failMeta(...)` 末尾加 `, nil`。
- `failAllProviders`：`f.failMeta(...)` 末尾加 `, nil`。
- `failSuccessPath`：`f.failMeta(http.StatusBadGateway, errMsg, db.FinishReasonInternal)` → 末尾加 `, input.Response.Header`。
- `respondUpstreamErrorBreak`（`gateway_flow_attempts.go`）：`f.failMeta(int32(status), errMsg, db.FinishReasonInternal)` → 末尾加 `, origHeader`。

### 7b. `completeFailedAttemptWithReason` 签名变更

`pkg/server/gateway_helpers.go`：

```go
func (s *Server) completeFailedAttemptWithReason(ctx context.Context, upstreamID string, upstreamCreatedAt time.Time, attemptStart time.Time, statusCode int32, errMsg string, finishReason int32, respHeader http.Header) {
	var extRespID pgtype.Text
	if respHeader != nil {
		extRespID = matchExternalIDHeader(respHeader, s.externalResponseIDHeaders)
	}
	s.updateRequest(ctx, newRequestUpdate(upstreamID, upstreamCreatedAt).
		StatusCode(pgtype.Int4{Int32: statusCode, Valid: true}).
		ErrorMessage(pgtype.Text{String: errMsg, Valid: true}).
		TimeSpentMs(pgtype.Int4{Int32: int32(time.Since(attemptStart).Milliseconds()), Valid: true}).
		FinishReason(pgtype.Int4{Int32: finishReason, Valid: true}).
		ExternalResponseID(extRespID))
}
```

更新调用方：

- `recordAttemptFailure`（`gateway_flow_attempts.go`）：`f.h.completeFailedAttemptWithReason(pctx, input.UpstreamID, input.UpstreamCreatedAt, input.AttemptStart, statusCode, err.Error(), finishReason)` → 末尾加 `, nil`。
- `failSuccessPath`（`gateway_flow_errors.go`）：`f.h.completeFailedAttemptWithReason(pctx, input.UpstreamID, input.UpstreamCreatedAt, input.AttemptStart, int32(input.Response.StatusCode), errMsg, db.FinishReasonInternal)` → 末尾加 `, input.Response.Header`。
- `openPathInternalReader`（`gateway_flow_success.go`）：`h.completeFailedAttemptWithReason(bgCtx, input.UpstreamID, input.UpstreamCreatedAt, input.AttemptStart, int32(resp.StatusCode), "decode upstream response: "+derr.Error(), db.FinishReasonInternal)` → 末尾加 `, resp.Header`。

### 7c. `handleUpstreamNonOK` — upstream 行

`pkg/server/gateway_flow_attempts.go` `handleUpstreamNonOK`：在 `newRequestUpdate` 链末尾加 `.ExternalResponseID(matchExternalIDHeader(resp.Header, f.h.externalResponseIDHeaders))`：

```go
f.h.updateRequest(pctx, newRequestUpdate(input.UpstreamID, input.UpstreamCreatedAt).
    StatusCode(pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true}).
    ErrorMessage(pgtype.Text{String: errMsg, Valid: true}).
    TimeSpentMs(pgtype.Int4{Int32: int32(time.Since(input.AttemptStart).Milliseconds()), Valid: true}).
    FinishReason(pgtype.Int4{Int32: db.FinishReasonInternal, Valid: true}).
    ExternalResponseID(matchExternalIDHeader(resp.Header, f.h.externalResponseIDHeaders)))
```

### 7d. `completeGatewaySuccess` — upstream + meta 行

`pkg/server/gateway_flow_success.go`：

两处 `newRequestUpdate` 链（upstream 行和 meta 行）末尾（`InferredModelSource(...)` 之后）各加：

```go
ExternalResponseID(matchExternalIDHeader(input.Response.Header, h.externalResponseIDHeaders))
```

### 7e. `openPathInternalReader` decode 失败 — meta 行

`pkg/server/gateway_flow_success.go`：meta 行的 `newRequestUpdate` 链末尾加：

```go
ExternalResponseID(matchExternalIDHeader(resp.Header, h.externalResponseIDHeaders))
```

### 7f. `unifiedStreamSuccess` 完成 — upstream + meta 行

`pkg/server/gateway_unified_helpers.go`：两处 `newRequestUpdate` 链（upstream 行 ~L591、meta 行 ~L608）末尾各加：

```go
ExternalResponseID(matchExternalIDHeader(resp.Header, h.externalResponseIDHeaders))
```

### 7g. `failUnifiedSuccess` — upstream + meta 行

`pkg/server/gateway_unified_helpers.go`：两处 `newRequestUpdate` 链（upstream 行 ~L632、meta 行 ~L638）末尾各加：

```go
ExternalResponseID(matchExternalIDHeader(a.resp.Header, h.externalResponseIDHeaders))
```

## 8. 合同与 View

`pkg/contract/request.go`：

- `RequestView`：在 `InferredModelSource` 之后、`UserID` 之前加：
  ```go
  ExternalRequestID  string `json:"externalRequestId,omitempty"`
  ExternalResponseID string `json:"externalResponseId,omitempty"`
  ```
- `requestLike`：在 `InferredModelSource` 之后、`UserID` 之前加：
  ```go
  ExternalRequestID  pgtype.Text
  ExternalResponseID pgtype.Text
  ```
- `toRequestView`：在 `InferredModelSource` 分支之后、`UserID` 分支之前加：
  ```go
  if r.ExternalRequestID.Valid {
      view.ExternalRequestID = r.ExternalRequestID.String
  }
  if r.ExternalResponseID.Valid {
      view.ExternalResponseID = r.ExternalResponseID.String
  }
  ```
- `ToRequestView`、`ToListRequestRowView`、`ToListRequestsBySpanRowView`：结构体字面量末尾（`UserID` 之后、`TraceID` 之前/之后）加：
  ```go
  ExternalRequestID:  r.ExternalRequestID,
  ExternalResponseID: r.ExternalResponseID,
  ```

## 9. 重新生成 OpenAPI 与前端类型

```bash
mise run openapi
pnpm --dir dashboard generate-openapi
```

## 10. 验证

- `go build ./cmd/picotera`（编译通过，sqlc 生成代码与手改一致）。
- `go test ./pkg/server/... ./pkg/llmbridge/...`（现有测试不回归）。
- 手动验证（需运行环境）：发一个带 `X-Request-Id: test-123` 的请求到网关，检查 meta 行 `external_request_id = 'test-123'`；若上游响应带 `X-Request-Id`，检查 upstream 和 meta 行 `external_response_id` 有值。
