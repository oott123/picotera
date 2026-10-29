# 执行计划：推测供应商 / 推测服务模型

## 1. 数据库迁移

新建 `db/migrations/031_request_inferred_provider_model.sql`：

```sql
-- +goose Up
ALTER TABLE request ADD COLUMN inferred_provider TEXT;
ALTER TABLE request ADD COLUMN inferred_model TEXT;

-- +goose Down
ALTER TABLE request DROP COLUMN IF EXISTS inferred_model;
ALTER TABLE request DROP COLUMN IF EXISTS inferred_provider;
```

## 2. sqlc 查询改动

编辑 `db/queries/request.sql`：

- `UpdateRequestOnComplete`：`SET` 末尾追加 `, inferred_provider = $15, inferred_model = $16`（紧随 `finish_reason = $14`）。
- `ListRequests` 列清单追加 `r.inferred_provider, r.inferred_model`。
- `ListRequestsBySpan` 列清单追加 `r.inferred_provider, r.inferred_model`。

运行 `sqlc generate`，确认 `pkg/db/` 中 `UpdateRequestOnCompleteParams`、`ListRequestsRow`、`ListRequestsBySpanRow`、`GetRequest` 返回类型均带出 `InferredProvider`、`InferredModel`（`pgtype.Text`）。

## 3. ResponseExtractor 推测逻辑（`pkg/server/response_extractor.go`）

1. `ResponseMetrics` 增加 `InferredProvider string`、`InferredModel string`。
2. `ResponseExtractor` 增加内部字段 `inferredProvider string`、`sigModel string`、`respModel string`。
3. `Metrics()` 计算：`InferredProvider = e.inferredProvider`；`InferredModel = e.sigModel`（非空）否则 `e.respModel`。
4. 新增 `inferProvider(payload string)`：按 proposal 三条规则填 `e.inferredProvider`（已非空则跳过）。
5. 新增 `inferModelField(payload string)`：顶层 `model` 为非空 string 时填 `e.respModel`（已非空则跳过）。
6. 新增 `inferSignatureModel(sig string)`：base64 解码 → `protoNavigate(data, []int{2,1,6})` → 校验全 ASCII → 填 `e.sigModel`（已非空则跳过）。
7. 在 `processSSEEvent` 末尾对 payload 调用 `inferProvider`、`inferModelField`；并解析 `signature_delta`（`content_block_delta` 的 `delta.signature`，且 `delta.type == "signature_delta"`）调用 `inferSignatureModel`。
8. 在 `extractJSONMetrics` 中对累积 body 调用 `inferProvider`、`inferModelField`；并扫描 Anthropic content 数组中 thinking block 的 `signature` 调用 `inferSignatureModel`。

## 4. protobuf 无 schema 解析

在 `pkg/server/` 新增 `proto_navigate.go`（或置于 `response_extractor.go` 内）：

```go
import "google.golang.org/protobuf/encoding/protowire"

// protoNavigate 沿字段号路径下钻 length-delimited 字段，返回末端字段字节。
func protoNavigate(data []byte, path []int) ([]byte, bool)
```

- 每层用 `protowire.ConsumeTag` 取 `(num, typ, n)`；`n < 0` 视为错误返回 `false`。
- 命中目标字段号且 `typ == protowire.BytesType` 时用 `protowire.ConsumeBytes` 取子字节，取**第一个**匹配项进入下一层。
- 非 `BytesType` 或字段缺失返回 `false`。

ASCII 校验：所有字节落在 `0x20`–`0x7E`。

## 5. 写入两行（path 路径）

`pkg/server/gateway_flow_success.go` 的 `completeGatewaySuccess`：

- 从 `extractor.Metrics()` 取 `InferredProvider` / `InferredModel`（已在 `m` 中）。
- 在两个 `UpdateRequestOnCompleteParams`（upstream + meta）填入：
  ```go
  InferredProvider: pgtype.Text{String: m.InferredProvider, Valid: m.InferredProvider != ""},
  InferredModel:    pgtype.Text{String: m.InferredModel, Valid: m.InferredModel != ""},
  ```

## 6. 写入两行（unified 路径）

`pkg/server/gateway_unified_helpers.go` 完成逻辑（`m := extractor.Metrics()` 之后）：在 upstream + meta 两个 `UpdateRequestOnCompleteParams` 同样填入 `InferredProvider` / `InferredModel`。

## 7. Contract / View（`pkg/contract/request.go`）

- `requestLike` 增加 `InferredProvider pgtype.Text`、`InferredModel pgtype.Text`。
- `RequestView` 增加 `InferredProvider string json:"inferredProvider,omitempty"`、`InferredModel string json:"inferredModel,omitempty"`。
- `toRequestView` 增加：
  ```go
  if r.InferredProvider.Valid { view.InferredProvider = r.InferredProvider.String }
  if r.InferredModel.Valid { view.InferredModel = r.InferredModel.String }
  ```
- 确认所有构造 `requestLike` 的调用点（ListRequests / ListRequestsBySpan / GetRequest 的转换）传入新字段。

## 8. 重新生成 OpenAPI 与 TS 类型

```bash
mise run openapi
pnpm --dir dashboard generate-openapi
```

## 9. 前端展示（`dashboard/src/components/RequestDetailsContent.vue`）

概览区「模型」`Field` 之后新增两个 `Field`：

```vue
<Field label="推测渠道" as="div">
  <span class="font-mono text-sm">{{ selected.inferredProvider || '—' }}</span>
</Field>
<Field label="推测模型" as="div">
  <span class="font-mono text-sm">{{ selected.inferredModel || '—' }}</span>
</Field>
```

## 10. 测试

- `pkg/server/response_extractor_test.go` 增加用例：
  - OpenRouter chunk → `provider:"Nvidia"`。
  - Anthropic message_start id `msg_bdrk_...` → `Amazon Bedrock`。
  - `message_stop` 带 `amazon-bedrock-invocationMetrics` → `Amazon Bedrock`。
  - `model` 字段提取。
  - 已知 base64 签名 → protobuf `[2][1][6]` 解码出 ASCII 模型字符串；签名与 `model` 字段共存时取签名结果。
  - 非 ASCII / 路径缺失 / 坏 base64 → 不产出 `sigModel`，回退到 `model` 字段。
- 运行 `go build ./...` 与 `go test ./pkg/server/...`。
- `pnpm --dir dashboard type-check`。

## 文件清单

- 新增：`db/migrations/031_request_inferred_provider_model.sql`
- 新增：`pkg/server/proto_navigate.go`
- 改：`db/queries/request.sql` → `sqlc generate` 产物 `pkg/db/*`
- 改：`pkg/server/response_extractor.go`、`pkg/server/response_extractor_test.go`
- 改：`pkg/server/gateway_flow_success.go`、`pkg/server/gateway_unified_helpers.go`
- 改：`pkg/contract/request.go`
- 改（生成）：`openapi.yaml`、`dashboard/src/openapi-types.d.ts`
- 改：`dashboard/src/components/RequestDetailsContent.vue`
