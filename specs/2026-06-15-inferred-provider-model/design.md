# 设计：推测供应商 / 推测服务模型

## 总体思路

推测信息全部来源于**上游原生响应字节**。`pkg/server/response_extractor.go` 的 `ResponseExtractor` 已经在两条网关路径（path-based 与 unified）上以流式方式解析上游 SSE/JSON 字节并提取 TTFT、token usage、stream error。推测供应商/模型在性质上与这些指标完全一致，因此直接扩展 `ResponseExtractor`，复用其已有的 SSE 事件解析与 JSON body 累积逻辑，无需新增解析管线。

- Path-based 路径：`gateway_flow_success.go` 的 `pipePathResponse` 创建 extractor，`completeGatewaySuccess` 将 `extractor.Metrics()` 写入两行。
- Unified 路径：`gateway_unified_helpers.go` 在上游原生字节上创建 extractor（在 llmbridge 转换之前），`extractor.Metrics()` 同样写入两行。

两条路径的 extractor 都包裹上游原生字节，因此 OpenRouter 的 `provider` 字段、Bedrock 的 `msg_bdrk_` 前缀与 `amazon-bedrock-invocationMetrics`、Anthropic 的 `signature_delta` 都能被看到。

## ResponseExtractor 扩展

在 `ResponseMetrics` 增加两个字段：

```go
InferredProvider string
InferredModel    string
```

在 `ResponseExtractor` 内部增加三个累积字段（不直接暴露）：

```go
inferredProvider string // 一旦非空即锁定，先命中者为准
sigModel         string // 思维签名 protobuf 解码出的模型
respModel        string // 响应 model 字段
```

`Metrics()` 在返回时计算最终推测模型：`sigModel` 非空则取之，否则取 `respModel`。`InferredProvider` 直接取 `inferredProvider`。空字符串表示未推测出。

### 供应商提取（先命中锁定）

在 `processSSEEvent`（SSE）与 `extractJSONMetrics`（非流式 JSON）中，对每个 payload 调用一个新的 `inferProvider(payload)`：

1. 顶层 `provider` 为非空字符串 → 取之。
2. 否则 message id（SSE message_start 的 `message.id`；非流式 Anthropic 的顶层 `id`）以 `msg_bdrk_` 前缀 → `Amazon Bedrock`。
3. 否则 payload 含 `amazon-bedrock-invocationMetrics` 字段 → `Amazon Bedrock`。

`inferredProvider` 一旦非空便不再覆盖。

### 模型提取

- `respModel`：payload 顶层 `model` 为非空字符串时记录（OpenAI chunk、Anthropic message_start 等均带 `model`）。已记录后不覆盖。
- `sigModel`：见下节签名解码。已记录后不覆盖。

### 思维签名解码

签名来源：

- SSE：`content_block_delta` 事件中 `delta.type == "signature_delta"` 的 `delta.signature`。
- 非流式 Anthropic JSON：content 数组中 thinking block 的 `signature` 字段。

取第一个非空 signature，执行：

1. base64 标准解码（`encoding/base64` StdEncoding）。
2. protobuf 无 schema 解析：沿字段号路径 `[2, 1, 6]` 逐层下钻——字段 2（length-delimited 嵌套消息）→ 字段 1（length-delimited 嵌套消息）→ 字段 6（length-delimited，按 string 处理）。
3. 校验字段 6 的字节全为可打印 ASCII（`0x20`–`0x7E`）。通过则作为 `sigModel`，否则丢弃。

任一步失败（base64 解码失败、路径缺失、字段类型不符、非 ASCII）都静默跳过，不影响其他指标。

## 第三方库 / 算法

- **protobuf 无 schema 解析**：使用已有依赖 `google.golang.org/protobuf/encoding/protowire`（`go.mod` 已含 `google.golang.org/protobuf v1.36.11`）。不引入新依赖。

  实现一个 `protoNavigate(data []byte, path []int) ([]byte, bool)`：在每一层用 `protowire.ConsumeTag` + `protowire.ConsumeBytes` 遍历，取路径上指定字段号的**第一个** length-delimited（`BytesType`）出现项作为下一层；最后一层返回其字节。遇到非 `BytesType` 字段或 `ConsumeXxx` 返回负长度（解析错误）即返回 `false`。

## 数据库

新增 migration `db/migrations/031_request_inferred_provider_model.sql`：

```sql
-- +goose Up
ALTER TABLE request ADD COLUMN inferred_provider TEXT;
ALTER TABLE request ADD COLUMN inferred_model TEXT;

-- +goose Down
ALTER TABLE request DROP COLUMN IF EXISTS inferred_model;
ALTER TABLE request DROP COLUMN IF EXISTS inferred_provider;
```

两列均可空（NULL = 未推测出）。

### 查询改动（`db/queries/request.sql`，随后 `sqlc generate`）

- `UpdateRequestOnComplete`：`SET` 子句追加 `inferred_provider = $15, inferred_model = $16`。两条网关路径的 `UpdateRequestOnCompleteParams` 填入推测值（meta 与 upstream 都写同一组值）。
- `ListRequests`、`ListRequestsBySpan`：显式列清单追加 `r.inferred_provider, r.inferred_model`。
- `GetRequest`：`SELECT *` 自动带出新列。

## API / Contract

`pkg/contract/request.go`：

- `requestLike` 增加 `InferredProvider pgtype.Text`、`InferredModel pgtype.Text`。
- `RequestView` 增加 `InferredProvider string json:"inferredProvider,omitempty"`、`InferredModel string json:"inferredModel,omitempty"`。
- `toRequestView` 增加两段 `if r.InferredX.Valid` 赋值。

随后 `mise run openapi` + `pnpm --dir dashboard generate-openapi` 重新生成类型。

## 前端

`dashboard/src/components/RequestDetailsContent.vue` 概览区在「模型」字段附近新增两个 `Field`：

- 「推测渠道」→ `selected.inferredProvider || '—'`
- 「推测模型」→ `selected.inferredModel || '—'`

仅当字段存在时展示（与现有 `userMessagePreview` 的 `v-if` 模式一致，或始终展示并以 `—` 占位，跟随该区其他字段风格）。

## 写入范围与一致性

推测值随 `completeGatewaySuccess`（path）与 unified 完成逻辑一次性写入。失败/重试场景下沿用现有 `UpdateRequestOnComplete` 调用点；只在成功完成路径填值，未推测出时写 NULL。两行（meta + upstream）写入相同的推测值，与现有 token/finish_reason 行为一致。
