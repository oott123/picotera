# API — 请求页面 Tokens 列「空回」筛选

## GET /api/picotera/requests

### New Query Parameter

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `emptyResponse` | boolean | no | `true` 时只返回「空回」请求：端点为补全端点且 `output_tokens` 为 `0` 或 `NULL`。缺省或 `false` 时不生效。 |

补全端点定义：

- `endpoint` 表中 `endpoint_type` ∈ `{2,3,4,7,8}`（OpenAI Chat Completions、OpenAI Responses、Anthropic Messages、Gemini GenerateContent、Gemini StreamGenerateContent）。
- 统一网关路由模式 `/api/unified/v1/messages`、`/api/unified/v1/responses`、`/api/unified/v1/chat/completions`、`/api/unified/v1beta/models/{model}:generateContent`、`/api/unified/v1beta/models/{model}:streamGenerateContent`。

### Validation

- `emptyResponse` 由 Huma 按 bool query 参数解析；非 `true`/`false` 的值由框架以 400 拒绝。
- 服务端不做大小写修正或近义值猜测。

### Interaction

- 与 `type`、`providerId`、`endpointPath`、`model`、`upstreamModel`、`traceId`、`projectId`、`startAt`、`endAt` 及 cursor 全部正交，可叠加使用。
- 分页与排序不变（`ORDER BY created_at DESC, id DESC`），cursor 格式不变。

### Example

```http
GET /api/picotera/requests?type=1&emptyResponse=true&limit=30
```

## Dashboard

`emptyResponse` 不持久化到 URL query，与 `providerId`/`endpointPath`/`model` 等列内筛选一致。切换时重置 cursor 并重新加载列表。
