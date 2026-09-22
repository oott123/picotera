# API：unified Anthropic count_tokens 端点

## 新增路由

```
POST /api/unified/v1/messages/count_tokens
OPTIONS /api/unified/v1/messages/count_tokens      （CORS 预检，由 corsMiddleware 应答 204）
```

与其它 unified 路由一样挂在 `/api/unified` 前缀下（不受 `/api/picotera` 的用户鉴权中间件管辖），用 PicoTera API Key 鉴权：`Authorization: Bearer <key>`、`X-Api-Key`、`?key=` 或 `X-Goog-Api-Key` 四选一。

该路由是**运行时常量**，不是 `endpoint` 表里的行；上游由运营者配置的 `anthropicCountTokens` 端点提供（见下）。

### 请求

Anthropic 官方 `POST /v1/messages/count_tokens` 的请求体原样透传，网关只读写 `model`：

```json
{
  "model": "claude-sonnet-4-5",
  "messages": [{ "role": "user", "content": "Hello, Claude" }],
  "system": "You are a helpful assistant.",
  "tools": [{ "name": "get_weather", "input_schema": { "type": "object" } }]
}
```

- `model` 缺失或为空串 → `400`，错误码 `model_not_found`。固定路由不做无模型降级（前缀挂载才有该降级）。
- `model` 会被 `rewriteModel` 钩子与 `beforeRequest` 的 `upstreamModel` 覆写改写后用 `sjson` 回写进 body 再发给上游。
- 其余字段（`messages`、`system`、`tools`、`tool_choice`、`thinking`、`betas` …）网关不解析、不改写；web search 改写不执行（透传路由跳过 `prepareUnifiedAttempt`）。
- 客户端查询串（如 `?beta=true`）原样合并到上游 URL，凭据参数 `key` 除外。

### 响应

上游响应字节原样转发，状态码与响应头照抄（网关固定剥离 `Content-Length` 与 `Access-Control-*` / `Alt-Svc` / `Nel` / `Report-To` / `Vary` 这类上游基础设施响应头，再补上自己的 CORS 头）：

```json
{ "input_tokens": 2095 }
```

计数结果**不写入请求行**：该响应没有 `usage` 对象，抽取器不产出 token 字段，`input_tokens` / `output_tokens` 与三个 cache 字段（`cache_read_tokens` / `cache_write_tokens` / `cache_write_1h_tokens`）以及 `model_cost` 都是 SQL NULL。这是与路径网关 `anthropicCountTokens` 端点一致的既定口径，不是缺口。

### 错误

| 状态码 | 错误码 | 触发条件 |
| --- | --- | --- |
| 400 | `model_not_found` | 请求体缺少 `model` 或为空串 |
| 401 / 403 | — | API Key 无效 / 所属用户被禁用 |
| 404 | `no_provider_available` | 没有渠道同时满足「端点类型为 `anthropicCountTokens`」+「`provider_models` 配置了该模型」 |
| 502 | `upstream_error` | 候选上游全部尝试失败 |

不做降级：候选类型集合就是 `{anthropicCountTokens}`，不会退到 `anthropicMessages`（messages 端点的 upstream_url 是 messages 接口的完整 URL，不能复用为计数接口），也不会做 llmbridge 跨格式转换（llmbridge 没有 count_tokens 格式与转换器）。

### 不支持流式

计数接口是单次 JSON 请求/响应。请求体不带 `stream`，`detectStreaming` 判定为 `false`；响应按普通 JSON 处理，不走 SSE 分支。

## 端点类型枚举：不扩充

复用已有的 `anthropicCountTokens`（内部值 5，中文名「Anthropic Tokens 计数」）。`EndpointView.endpointType` / `EndpointLabel.endpointType` 的枚举取值与 `openapi.yaml` 均不变，本次**不需要**重新生成 OpenAPI 与 TS 类型。

`GET /api/picotera/labels/endpoints` 的合成标签列表自动多出一条（该接口遍历 `unifiedRoutes`）：

```json
{ "path": "/api/unified/v1/messages/count_tokens", "name": "Unified Anthropic Count Tokens", "endpointType": "anthropicCountTokens" }
```

## 上游端点配置示例

运营者需建一条计数端点行，把渠道绑定到该端点，并在模型的 `provider_models` 里让模型对渠道可见（条目若配置了 `endpoints` 白名单，需把该端点 path 列进去）。

```json
PUT /api/picotera/endpoints
{
  "name": "Anthropic Count Tokens",
  "path": "/v1/messages/count_tokens",
  "modelPath": "model",
  "credentialsResolver": "xApiKey",
  "endpointType": "anthropicCountTokens"
}
```

```json
PUT /api/picotera/provider-endpoints
{
  "providerId": 1,
  "endpointPath": "/v1/messages/count_tokens",
  "upstreamUrl": "https://api.anthropic.com/v1/messages/count_tokens",
  "credentialsResolver": "xApiKey"
}
```

同一条端点行同时服务路径网关的 `/v1/messages/count_tokens` 与本条 unified 路由。

## 请求记录字段

| 字段 | 值 |
| --- | --- |
| meta 行 `endpoint_path` | `/api/unified/v1/messages/count_tokens` |
| upstream 行 `endpoint_path` | 所选上游端点自身的配置 path（非前缀端点不追加后缀） |
| `input_tokens` / `output_tokens` / cache 三项 | NULL（计数结果不入库） |
| `model_cost` | NULL（无 token，不计价） |
| `finish_reason` | 非流式 JSON 读到 EOF → `3`（正常结束） |
| `user_message_preview` | 默认启发式链取 `messages` 中最后一条 user 文本 |

**统计口径**：该路径与 `anthropicCountTokens`（5）类型都不在 `completion_endpoint_path` 视图内，因此计数请求不进入成功率 / 空回复 / finish_reason 统计（`output_tokens` 恒为 0，纳入会被判成空回）；请求量统计照常计入。

**端点筛选**：请求列表的 `endpointPath` 筛选是前缀感知的（为 codex 前缀端点而设），按 `/api/unified/v1/messages` 筛选时计数行会一并出现；按计数路径筛选只命中计数行。