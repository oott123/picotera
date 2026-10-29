# 设计：端点测试功能

## 概述

两种测试共享同一前端表单与同一套「按格式构建请求体」的逻辑，区别只在请求的发送目标与处理路径：

| | 短路测试 | 网关测试 |
|---|---|---|
| 目标 | 选定的 provider + provider_endpoint | 选定的 API key + 端点（unified / 网关 path） |
| 处理路径 | 后端薄代理，**绕过** 脚本/前后置/llmbridge/MPE/模型重写 | 完整网关管线，全部生效 |
| 日志 | 不写 request 行、不写 artifact | 正常记录 |
| 凭证 | 后端用 provider.credentials 注入（前端永不可见） | 前端用 API key 的明文 `key` 作 Bearer |
| 后端改动 | 新增一个原始 chi 流式接口 | 无 |

请求体构建逻辑只在前端实现一次（TypeScript），是两种测试的单一来源；短路测试把构建好的 body 传给后端，后端不解析、不重写，仅附加凭证后转发。

## 一、短路测试后端接口

新增原始 chi handler `POST /api/picotera/test/direct`，在 `registerEndpoints()` 中、catch-all 网关挂载（`router.Mount("/", ...)`）**之前** 注册，与五条 unified 路由并列。它不是 Huma operation，因为需要把上游 SSE 实时透传给浏览器（Huma 是 JSON 请求/响应模型）。这与 unified 路由同样不进 `openapi.yaml` 的现状一致。

### 处理流程（`pkg/server/handle_test_direct.go`）

1. 解析请求 JSON：`providerId`、`endpointPath`、`body`（原始 JSON，`json.RawMessage`）、`stream`、`pathVars`（可选）。
2. `GetProviderByID(providerId)` → provider，取 `credentials`、`proxyUrl`。
3. `GetProviderEndpoint{providerId, endpointPath}` → pe，取 `upstreamUrl`、`credentialsResolver`。未找到 → 404。
4. `GetEndpointByPath(endpointPath)` → endpoint，取其 `credentialsResolver`（用于继承）。未找到 → 404。
5. `sendResolver = effectiveSendResolver(endpoint.CredentialsResolver, pe.CredentialsResolver)`（复用 `gateway_helpers.go`）。
6. 构建上游请求：
   - `substitutePathVars(pe.UpstreamUrl, pathVars)` 替换 URL 中的 `{name}` 占位（如 gemini 的 `{model}`）。
   - `http.NewRequestWithContext(POST, url, body)`，`Content-Type: application/json`。
   - `applyCredentials(req, provider.Credentials, sendResolver, nil)`（复用）。
   - 不复制任何客户端请求头（dashboard 发起，无 LLM 客户端请求）；保持请求干净。
7. `s.forwardRequest(req, provider.ProxyUrl.String, stream)`（复用，按 stream 选择 transport）。
8. 透传响应：写入上游 status；复制 `Content-Type`；用带 `http.Flusher` 的流式 `io.Copy` 把 body 逐块写回（SSE 场景每块 flush）。Go transport 默认对自身添加的 gzip 自动解压并去掉 `Content-Encoding`，故无需额外处理压缩，直接转发解压后的字节即可。
9. 错误处理：网络/上游错误返回 `502` + `{"message":...}` JSON（接口自身的错误，与上游业务错误区分）。上游返回的非 200 业务响应按原样透传（status + body），由前端展示。

**明确不做**：不写 request/artifact、不开 jsx session、不解析 MPE、不做模型重写、不跑任何 hook。允许测试 disabled 的 provider/endpoint（测试排障的正当场景）。

## 二、网关测试（纯前端）

无后端改动。前端：

1. 选 API key（来自 `listApiKeys`，含明文 `key`）。
2. 选目标：
   - **unified**：五种格式之一 → 对应固定路径（见 `unifiedRoutePath`）。
   - **网关 path 端点**：某个 `endpoint` 行 → 其 `path` + `endpointType`。`path` 含 `{name}` 占位时前端提供输入框填充（如 model）。
3. 按目标格式构建 body（与短路测试同一套构建器）。
4. `fetch(targetURL, { method:'POST', headers:{ 'Content-Type':'application/json', Authorization:'Bearer '+key }, body })`。`Authorization: Bearer` 对网关的 api_key 鉴权在所有 resolver 下都适用（`extractClientToken` 在指定位置为空时回退扫描，且 api_key 按 token 值查表，与位置无关），故统一用 Bearer。
5. stream=true 时读取 `response.body` 流喂给 `useSSEParser`；否则解析 JSON。

## 三、前端请求体构建器

新增 `dashboard/src/lib/testBody.ts`，从 `{ model, system, maxTokens, userMessage, stream }` 按格式生成 body：

- `anthropicMessages`：`{ model, max_tokens, system, messages:[{role:'user',content:userMessage}], stream }`
- `openaiChatCompletions`：`{ model, max_tokens, messages:[{role:'system',content:system},{role:'user',content:userMessage}], stream }`
- `openaiResponses`：`{ model, max_output_tokens:maxTokens, instructions:system, input:userMessage, stream }`
- `geminiGenerateContent` / `geminiStreamGenerateContent`：`{ systemInstruction:{parts:[{text:system}]}, contents:[{role:'user',parts:[{text:userMessage}]}], generationConfig:{maxOutputTokens:maxTokens} }`（流式由路径 `:streamGenerateContent` 决定，body 无 stream 字段；model 在 URL 中）

空 `system` 时省略该字段。高级模式下，编辑器初始填充生成的 body，用户编辑后以编辑后的字节发送。

## 四、前端视图与数据层

- 新视图 `dashboard/src/views/TestView.vue`，路由 `/test`，名称 `test`。`SegmentedControl` 切换「短路测试 / 网关测试」。
- 左侧表单 + 右侧响应面板（复用现有 UI primitives 与 `useSSEParser`）。
- 短路模式表单：选 provider（`listProviders`）→ 选其 provider_endpoint（`listProviderEndpoints?providerId`）→ 由该端点的 `endpointType`（`listEndpoints` 映射 path→type）确定格式；结构化字段 + stream + 高级 body。
- 网关模式表单：选 API key + 目标（unified 格式或 `endpoint` 行）；结构化字段 + stream + 高级 body；path 占位填充。
- 响应面板：状态码、耗时/TTFT、`useSSEParser` 实时内容、原始响应字节。
- 数据获取统一走 `@tanstack/vue-query`；两个测试的「发送」动作是命令式 fetch（流式），在 `dashboard/src/api/client.ts` 增加手写 fetcher（`postTestDirect`、`postGatewayTest`）——这是流式接口的既有例外（与 SSE 一致），不经 `openapi-fetch`。
- 注册：`router/index.ts` 路由、`App.vue` `pageMeta`、`AppSidebar` 导航项。

## 复用与不引入

- 全部复用现有后端辅助函数（`effectiveSendResolver` / `substitutePathVars` / `applyCredentials` / `forwardRequest`）与现有 db 查询（`GetProviderByID` / `GetProviderEndpoint` / `GetEndpointByPath`），不新增 sqlc 查询、不改 schema。
- UI 仅用 `src/ui/` 本地 primitives，不引入第三方组件库。
- 不引入任何兼容层；短路接口是全新干净实现。
