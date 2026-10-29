# API 变更

## 一、新增网关路由（`/api/unified`，API Key 鉴权）

三条路由与现有 5 条 unified 路由同组注册：先于 catch-all 网关挂载、带 `corsMiddleware`、同时注册 `POST` 与 `OPTIONS`（预检由中间件回 204）。鉴权方式与现有 unified 路由一致（请求头里的 PicoTera API Key），不受 `/api/picotera` 的用户鉴权中间件管辖。

### `POST /api/unified/codex/responses`

OpenAI Responses 源格式，与 `POST /api/unified/v1/responses` 行为完全一致（同样的候选类型集合、同样的跨格式转换、同样的 web search 模拟规则），仅路径不同，便于 Codex 把 base_url 配成 `…/api/unified/codex`。

- 请求体：OpenAI Responses 请求，必须带 `model`。
- 候选上游类型：`anthropicMessages` / `openaiChatCompletions` / `openaiResponses` / `geminiGenerateContent` 或 `geminiStreamGenerateContent`（按流式标志二选一）。
- `request` 行的 `endpoint_path` 记为 `/api/unified/codex/responses`。

### `POST /api/unified/codex/responses/compact`

Codex 压缩，纯透传。

- 请求体：Codex 压缩请求，必须带 `model`；缺失或为空串返回 `400`，错误码 `model_not_found`。
- 候选上游类型：仅 `codexCompact`。无匹配渠道返回 `404`，错误码 `no_provider_available`。
- 请求体与响应体字节原样转发（模型名改写除外：`rewriteModel` 钩子与 `beforeRequest` 的 `upstreamModel` 覆写会写回 body 的 `model` 字段）。
- `endpoint_path` 记为 `/api/unified/codex/responses/compact`；计入补全端点统计范围。

### `POST /api/unified/v1/alpha/search`

Codex 搜索 v1alpha，纯透传。

- 请求体：搜索请求，必须带 `model`；缺失或为空串返回 `400`，错误码 `model_not_found`。
- 候选上游类型：仅 `codexSearchV1Alpha`。无匹配渠道返回 `404`，错误码 `no_provider_available`。
- 响应体（形如 `{"encrypted_output": "…", "output": "…"}`）原样转发；请求详情页沿用已有的 search alpha 渲染（按响应体形状识别）。
- `endpoint_path` 记为 `/api/unified/v1/alpha/search`；**不**计入补全端点统计范围。

三条路由的失败语义、重试与钩子链路与既有 unified 路由相同，透传路由不执行 `beforeTransform`。

## 二、管理 API 变更

仅枚举扩容，无新增操作、无请求/响应结构变化。

`EndpointView.endpointType`（`GET/PUT /api/picotera/endpoints`）与 `EndpointLabel.endpointType`（`GET /api/picotera/labels/endpoints`）的枚举各增两项：

```
general | openaiChatCompletions | openaiResponses | anthropicMessages |
anthropicCountTokens | geminiGenerateContent | geminiStreamGenerateContent |
exaSearch | modelList | codexCompact | codexSearchV1Alpha | unknown
```

`GET /api/picotera/labels/endpoints` 追加的合成条目（unified 路由不是端点表里的行）新增三条，且这些合成条目的 `endpointType` 改由路由的 `SourceType` 决定（对既有五条路由输出不变）：

| path | name | endpointType |
| --- | --- | --- |
| `/api/unified/codex/responses` | Unified Codex Responses | `openaiResponses` |
| `/api/unified/codex/responses/compact` | Unified Codex Compact | `codexCompact` |
| `/api/unified/v1/alpha/search` | Unified Codex Search v1alpha | `codexSearchV1Alpha` |

变更后必须 `mise run openapi` + `pnpm --dir dashboard generate-openapi`。

## 三、脚本可见面（`pkg/jsx`）

- `ctx.endpoint.endpointType`（int32）新增取值 `11` / `12`。
- `candidate.providerModel.upstreamFormat` 改由端点类型字符串给出：既有五种生成类型的取值不变（`anthropicMessages` / `openaiChatCompletions` / `openaiResponses` / `geminiGenerateContent` / `geminiStreamGenerateContent`），新增取值 `codexCompact` / `codexSearchV1Alpha`，其它类型为该类型自身的字符串。
- `beforeTransform` 在两条透传路由上不执行。
