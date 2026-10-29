# API 设计

本特性不涉及对外 HTTP 接口变更，仅改动内部数据层。下列为新的查询签名与 Go builder 表面。

## 生成的查询

```go
func (q *Queries) UpdateRequest(ctx context.Context, arg UpdateRequestParams) error
```

`UpdateRequestParams`（sqlc 生成）：`ID string`、`CreatedAt pgtype.Timestamp`，外加 24 组「`Set<Col> bool` + `<Col>` 值」。`Status`/`InferredModelSource` 值为非空类型（`int32`/`int16`），其余为对应 `pgtype.*`。

## Go builder（`pkg/server/request_update.go`）

构造：

```go
func newRequestUpdate(id string, createdAt time.Time) *requestUpdate
func (s *Server) updateRequest(ctx context.Context, u *requestUpdate)
```

链式 setter（每个置位对应标志并写值，返回 `*requestUpdate`）：

| Setter | 参数类型 | 列 |
|---|---|---|
| `ProviderID` | `pgtype.Int4` | provider_id |
| `Model` | `pgtype.Text` | model |
| `UpstreamModel` | `pgtype.Text` | upstream_model |
| `EndpointPath` | `pgtype.Text` | endpoint_path |
| `ApiKeyID` | `pgtype.Int4` | api_key_id |
| `UserID` | `pgtype.Int8` | user_id |
| `ProjectID` | `pgtype.Int4` | project_id |
| `Status` | `int32` | status |
| `StatusCode` | `pgtype.Int4` | status_code |
| `ErrorMessage` | `pgtype.Text` | error_message |
| `TimeSpentMs` | `pgtype.Int4` | time_spent_ms |
| `TtftMs` | `pgtype.Int4` | ttft_ms |
| `InputTokens` | `pgtype.Int4` | input_tokens |
| `OutputTokens` | `pgtype.Int4` | output_tokens |
| `CacheReadTokens` | `pgtype.Int4` | cache_read_tokens |
| `CacheWriteTokens` | `pgtype.Int4` | cache_write_tokens |
| `CacheWrite1hTokens` | `pgtype.Int4` | cache_write_1h_tokens |
| `ModelCost` | `pgtype.Numeric` | model_cost |
| `ModelCostCurrency` | `pgtype.Text` | model_cost_currency |
| `FinishReason` | `pgtype.Int4` | finish_reason |
| `InferredProvider` | `pgtype.Text` | inferred_provider |
| `InferredModel` | `pgtype.Text` | inferred_model |
| `InferredModelSource` | `int16` | inferred_model_source |
| `UserMessagePreview` | `pgtype.Text` | user_message_preview |

## 各调用点的 setter 组合

| 调用点 | 时机 | setter |
|---|---|---|
| `gateway_flow.go` `authenticateAndBackfill` | 认证后回填 meta 行 | `ApiKeyID` `UserID` `ProjectID` |
| `gateway_flow.go` `updateMetaModel` | 模型重写后 | `Model` |
| `gateway_flow.go` 预览回填 | 认证后 | `UserMessagePreview` |
| `gateway_flow_success.go` `markPathHeadersReceived`（meta + upstream 两次） | 收到响应头 | `ProviderID` `Model` `UpstreamModel` `EndpointPath` `Status` |
| `gateway_unified_helpers.go` `unifiedStreamSuccess`（meta + upstream 两次） | 收到响应头 | `ProviderID` `Model` `UpstreamModel` `EndpointPath` `Status` |
| 各 `updateRequestOnComplete` 调用点（`gateway_flow_attempts.go`、`gateway_flow_success.go` ×3、`gateway_flow_errors.go`、`gateway_helpers.go`、`gateway_unified_helpers.go` ×4） | 请求完成/失败 | `StatusCode` `ErrorMessage` `TimeSpentMs` `Status` `TtftMs` `InputTokens` `OutputTokens` `CacheReadTokens` `CacheWriteTokens` `CacheWrite1hTokens` `ModelCost` `ModelCostCurrency` `FinishReason` `InferredProvider` `InferredModel` `InferredModelSource` |
| 原 `UpdateRequestMetrics` | —— | 当前无任何调用方（死代码），随查询一并删除，无调用点迁移 |

收到响应头的 4 处不再链接 `UserID`/`ProjectID`，项目被清空的 bug 由此从结构上消除。
