# 设计：推测模型来源字段

在已有的 `inferred_model`（推测服务模型）基础上，新增 `inferred_model_source` 列，记录该推测值的来源。这是对 spec `2026-06-15-inferred-provider-model` 的直接扩展，沿用其全部写入路径与展示位置。

## 数据库

迁移 `032_request_inferred_model_source.sql`：

```sql
-- +goose Up
ALTER TABLE request ADD COLUMN inferred_model_source SMALLINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE request DROP COLUMN IF EXISTS inferred_model_source;
```

`SMALLINT`（pgtype.Int2 → Go int16）足够容纳枚举；`NOT NULL DEFAULT 0` 使历史行与未推测出模型的行统一为「未知」。

## Golang 枚举

在 `pkg/db/request_constants.go` 新增：

```go
const (
	InferredModelSourceUnknown   = 0 // 未推测出模型
	InferredModelSourceSignature = 1 // 思维链签名
	InferredModelSourceResponse  = 2 // 响应结构（model 字段）
)
```

## 推测来源判定

来源由 `pkg/server/response_extractor.go` 现有的 `sigModel` / `respModel` 累加器决定，与 `Metrics()` 中选取 `InferredModel` 的逻辑保持一致：

- `sigModel != ""` → `InferredModelSourceSignature`（签名优先）。
- 否则 `respModel != ""` → `InferredModelSourceResponse`。
- 都为空 → `InferredModelSourceUnknown`。

`ResponseMetrics` 增加字段 `InferredModelSource int32`，在 `Metrics()` 中与 `InferredModel` 一同计算填充，保证来源与值始终匹配。

## 写入路径

`UpdateRequestOnComplete` 查询新增 `inferred_model_source` 参数。`pkg/server/gateway_flow_success.go` 的 `completeGatewaySuccess` 在写 upstream 行与 meta 行时各带上 `m.InferredModelSource`，与 `InferredProvider` / `InferredModel` 并列。

## 契约与前端

- `pkg/contract/request.go`：`RequestView` 增加 `InferredModelSource *int32 json:"inferredModelSource,omitempty"`，`requestLike` 增加对应 `pgtype.Int2` 字段；三个 `To*View` 转换函数透传。仅在来源非 0 时填充指针（未知时省略，前端不渲染 tag）。
- 通过 `mise run openapi` + `pnpm --dir dashboard generate-openapi` 再生 TS 类型。
- `RequestDetailsContent.vue`：在「推测模型」`Field` 内、值之后渲染一个小 tag。来源 → 文案映射在组件内用一个小函数完成（`1 → 思维链`，`2 → 响应`，其余 → 不渲染）。tag 复用 `src/ui/` 中既有的 Tailwind 原语样式，不引入第三方组件。
