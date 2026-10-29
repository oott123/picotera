# 设计

## 总览

用一条标志位驱动的通用 `UpdateRequest` 查询替换 `request` 行的全部 5 条局部更新查询。每个可变列在 SQL 里写成 `col = CASE WHEN sqlc.arg('set_col')::bool THEN <value> ELSE col END`：标志为真才写入新值，否则保持原值。配合一个 Go 侧链式 builder，调用点只声明它要改的字段。

不引入第三方库，不保留兼容层。被替换的 5 条查询、5 个 Go wrapper 全部删除。

## 数据库查询

### 可变列清单（24 列）

| 列 | 类型 | 取值方式 |
|---|---|---|
| provider_id | int4 | narg |
| model | text | narg |
| upstream_model | text | narg |
| endpoint_path | text | narg |
| api_key_id | int4 | narg |
| user_id | int8 | narg |
| project_id | int4 | narg |
| status_code | int4 | narg |
| error_message | text | narg |
| time_spent_ms | int4 | narg |
| ttft_ms | int4 | narg |
| input_tokens | int4 | narg |
| output_tokens | int4 | narg |
| cache_read_tokens | int4 | narg |
| cache_write_tokens | int4 | narg |
| cache_write_1h_tokens | int4 | narg |
| model_cost | numeric | narg |
| model_cost_currency | text | narg |
| finish_reason | int4 | narg |
| inferred_provider | text | narg |
| inferred_model | text | narg |
| user_message_preview | text | narg |
| status | int (NOT NULL) | arg |
| inferred_model_source | smallint (NOT NULL) | arg |

`status` 与 `inferred_model_source` 列为 NOT NULL，取值用 `sqlc.arg`（非空类型），即使标志为假也需传一个被 `CASE` 忽略的占位值；其余可空列用 `sqlc.narg`，使标志为真时可写入真实值或显式 NULL。

主键定位用 `id` + `created_at`（hypertable 复合主键，见 CLAUDE.md）。

### 查询形态

`db/queries/request.sql` 中删除 `UpdateRequestOnHeader`/`UpdateRequestModel`/`UpdateRequestUserMessagePreview`/`UpdateRequestMetrics`/`UpdateRequestOnComplete`，新增：

```sql
-- name: UpdateRequest :exec
UPDATE request SET
  provider_id = CASE WHEN sqlc.arg('set_provider_id')::bool THEN sqlc.narg('provider_id')::int ELSE provider_id END,
  model = CASE WHEN sqlc.arg('set_model')::bool THEN sqlc.narg('model')::text ELSE model END,
  upstream_model = CASE WHEN sqlc.arg('set_upstream_model')::bool THEN sqlc.narg('upstream_model')::text ELSE upstream_model END,
  endpoint_path = CASE WHEN sqlc.arg('set_endpoint_path')::bool THEN sqlc.narg('endpoint_path')::text ELSE endpoint_path END,
  api_key_id = CASE WHEN sqlc.arg('set_api_key_id')::bool THEN sqlc.narg('api_key_id')::int ELSE api_key_id END,
  user_id = CASE WHEN sqlc.arg('set_user_id')::bool THEN sqlc.narg('user_id')::bigint ELSE user_id END,
  project_id = CASE WHEN sqlc.arg('set_project_id')::bool THEN sqlc.narg('project_id')::int ELSE project_id END,
  status = CASE WHEN sqlc.arg('set_status')::bool THEN sqlc.arg('status')::int ELSE status END,
  status_code = CASE WHEN sqlc.arg('set_status_code')::bool THEN sqlc.narg('status_code')::int ELSE status_code END,
  error_message = CASE WHEN sqlc.arg('set_error_message')::bool THEN sqlc.narg('error_message')::text ELSE error_message END,
  time_spent_ms = CASE WHEN sqlc.arg('set_time_spent_ms')::bool THEN sqlc.narg('time_spent_ms')::int ELSE time_spent_ms END,
  ttft_ms = CASE WHEN sqlc.arg('set_ttft_ms')::bool THEN sqlc.narg('ttft_ms')::int ELSE ttft_ms END,
  input_tokens = CASE WHEN sqlc.arg('set_input_tokens')::bool THEN sqlc.narg('input_tokens')::int ELSE input_tokens END,
  output_tokens = CASE WHEN sqlc.arg('set_output_tokens')::bool THEN sqlc.narg('output_tokens')::int ELSE output_tokens END,
  cache_read_tokens = CASE WHEN sqlc.arg('set_cache_read_tokens')::bool THEN sqlc.narg('cache_read_tokens')::int ELSE cache_read_tokens END,
  cache_write_tokens = CASE WHEN sqlc.arg('set_cache_write_tokens')::bool THEN sqlc.narg('cache_write_tokens')::int ELSE cache_write_tokens END,
  cache_write_1h_tokens = CASE WHEN sqlc.arg('set_cache_write_1h_tokens')::bool THEN sqlc.narg('cache_write_1h_tokens')::int ELSE cache_write_1h_tokens END,
  model_cost = CASE WHEN sqlc.arg('set_model_cost')::bool THEN sqlc.narg('model_cost')::numeric ELSE model_cost END,
  model_cost_currency = CASE WHEN sqlc.arg('set_model_cost_currency')::bool THEN sqlc.narg('model_cost_currency')::text ELSE model_cost_currency END,
  finish_reason = CASE WHEN sqlc.arg('set_finish_reason')::bool THEN sqlc.narg('finish_reason')::int ELSE finish_reason END,
  inferred_provider = CASE WHEN sqlc.arg('set_inferred_provider')::bool THEN sqlc.narg('inferred_provider')::text ELSE inferred_provider END,
  inferred_model = CASE WHEN sqlc.arg('set_inferred_model')::bool THEN sqlc.narg('inferred_model')::text ELSE inferred_model END,
  inferred_model_source = CASE WHEN sqlc.arg('set_inferred_model_source')::bool THEN sqlc.arg('inferred_model_source')::smallint ELSE inferred_model_source END,
  user_message_preview = CASE WHEN sqlc.arg('set_user_message_preview')::bool THEN sqlc.narg('user_message_preview')::text ELSE user_message_preview END
WHERE id = sqlc.arg('id')::text AND created_at = sqlc.arg('created_at')::timestamp;
```

`sqlc generate` 会生成 `UpdateRequestParams`（约 48 字段：24 个 `Set*` 布尔 + 24 个值字段）及 `Querier.UpdateRequest`。

## Go 侧 builder

新增 `pkg/server/request_update.go`，提供链式 builder 隐藏 48 字段结构体；每个 setter 置位对应标志并写值。`*Server` 上一个 `updateRequest` 方法执行查询并沿用现有"出错只记日志、不影响响应"的语义。

```go
type requestUpdate struct{ p db.UpdateRequestParams }

func newRequestUpdate(id string, createdAt time.Time) *requestUpdate {
	return &requestUpdate{p: db.UpdateRequestParams{
		ID:        id,
		CreatedAt: pgtype.Timestamp{Time: createdAt, Valid: true},
	}}
}

func (u *requestUpdate) ProjectID(v pgtype.Int4) *requestUpdate { u.p.SetProjectID = true; u.p.ProjectID = v; return u }
func (u *requestUpdate) ProviderID(v pgtype.Int4) *requestUpdate { u.p.SetProviderID = true; u.p.ProviderID = v; return u }
func (u *requestUpdate) Status(v int32) *requestUpdate { u.p.SetStatus = true; u.p.Status = v; return u }
// …每个可变列一个 setter

func (s *Server) updateRequest(ctx context.Context, u *requestUpdate) {
	if err := s.queries.UpdateRequest(ctx, u.p); err != nil {
		logx.WithContext(ctx).WithError(err).Error("failed to update request")
	}
}
```

调用点改为按时机只链接需要的 setter，例如收到响应头时不再链接 `UserID`/`ProjectID`，bug 由构造方式消除：

```go
// 认证后回填（meta 行）
h.updateRequest(ctx, newRequestUpdate(meta.ID, meta.CreatedAt).
	ApiKeyID(apiKeyID).UserID(userID).ProjectID(projectID))

// 收到响应头（meta / upstream 行）
h.updateRequest(ctx, newRequestUpdate(id, createdAt).
	ProviderID(pid).Model(m).UpstreamModel(um).EndpointPath(ep).Status(db.RequestStatusHeaderReceived))
```

`gateway_helpers.go` 中的 `updateRequestOnHeader`/`updateRequestModel`/`updateRequestUserMessagePreview`/`updateRequestOnComplete` 四个 wrapper 删除，全部替换为 builder + `updateRequest`。
