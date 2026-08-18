# API：unified OpenAI Embeddings 端点

## 新增路由

```
POST /api/unified/v1/embeddings
OPTIONS /api/unified/v1/embeddings      （CORS 预检，由 corsMiddleware 应答 204）
```

与其它 unified 路由一样挂在 `/api/unified` 前缀下（不受 `/api/picotera` 的用户鉴权中间件管辖），用 PicoTera API Key 鉴权。

**认证**：`Authorization: Bearer <picotera-api-key>` 或 `X-Api-Key`，与其它 unified 路由一致。

### 请求

OpenAI Embeddings 请求体原样透传，唯一被网关读写的字段是 `model`：

```json
{
  "model": "text-embedding-3-small",
  "input": "The food was delicious and the waiter...",
  "encoding_format": "float"
}
```

- `model` 缺失或为空串 → `400`，错误码 `model_not_found`，消息 `model is required`。
- `model` 会被 `rewriteModel` 钩子与 `beforeRequest` 的 `upstreamModel` 覆写改写后用 `sjson` 回写进 body 再发给上游。
- 其余字段（`input`、`dimensions`、`encoding_format`、`user` …）网关不解析、不改写。

### 响应

上游响应字节原样转发，状态码、`Content-Type` 与响应头（除固定剥离的凭据类头）照抄：

```json
{
  "object": "list",
  "data": [{ "object": "embedding", "index": 0, "embedding": [0.0023, -0.0092, "…"] }],
  "model": "text-embedding-3-small",
  "usage": { "prompt_tokens": 8, "total_tokens": 8 }
}
```

### 错误

| 状态码 | 错误码 | 触发条件 |
| --- | --- | --- |
| 400 | `model_not_found` | 请求体缺少 `model` 或为空串 |
| 401 / 403 | — | API Key 无效 / 所属用户被禁用 |
| 404 | `no_provider_available` | 没有一个渠道同时满足「端点类型为 `openaiEmbedding`」+「`provider_models` 配置了该模型」 |
| 502 | `upstream_error` | 全部候选上游尝试失败 |

不做降级：候选类型集合就是 `{openaiEmbedding}`，不会退到 `openaiChatCompletions` 或 `openaiResponses`。

### 不支持流式

Embeddings 无流式形态。请求体不带 `stream`、`detectStreaming` 判定为 `false`、响应按普通 JSON 处理。

## 端点类型枚举扩充

`GET /api/picotera/endpoints`、`PUT /api/picotera/endpoints`、`GET /api/picotera/labels/endpoints` 的 `endpointType` 枚举新增一个取值：

```
general | openaiChatCompletions | openaiResponses | anthropicMessages
| anthropicCountTokens | geminiGenerateContent | geminiStreamGenerateContent
| exaSearch | modelList | codexCompact | codexSearchV1Alpha
| openaiEmbedding          ← 新增（内部值 13，中文名「OpenAI 特征提取」）
| unknown
```

`GET /api/picotera/labels/endpoints` 的合成标签列表新增一条：

```json
{ "path": "/api/unified/v1/embeddings", "name": "Unified OpenAI Embeddings", "endpointType": "openaiEmbedding" }
```

## 上游端点配置示例

运营者需在端点表建一行，再绑定到渠道，并在模型的 `provider_models` 里配置 embedding 模型名：

```json
PUT /api/picotera/endpoints
{
  "name": "OpenAI Embeddings",
  "path": "/upstream/v1/embeddings",
  "modelPath": "model",
  "credentialsResolver": "bearerToken",
  "endpointType": "openaiEmbedding"
}
```

## 请求记录字段

| 字段 | 值 |
| --- | --- |
| meta 行 `endpoint_path` | `/api/unified/v1/embeddings` |
| upstream 行 `endpoint_path` | 所选上游端点的配置 path |
| `input_tokens` | 上游 `usage.prompt_tokens` |
| `output_tokens` / `ttft_ms` | NULL（embedding 无输出、无首字延迟） |
| `model_cost` | 按模型 pricing 的 input 单价结算 |
| `user_message_preview` | 请求体 `input` 的字符串（数组时取首个非空字符串） |

**统计口径**：该路径与 `openaiEmbedding` 类型均不在 `completion_endpoint_path` 视图内，因此 embedding 请求不进入成功率 / 空回复 / finish_reason 统计；请求量、token、费用统计照常计入。
