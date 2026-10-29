# API 设计

## 新增：短路测试接口（原始 chi 路由，非 Huma）

```
POST /api/picotera/test/direct
Content-Type: application/json
```

注册位置：`pkg/server/server.go` 的 `registerEndpoints()`，在 unified 路由之后、`router.Mount("/", ...)` catch-all 之前。不进 `openapi.yaml`（与 unified 路由一致），前端用手写 fetcher 调用。

### 请求体

```json
{
  "providerId": 12,
  "endpointPath": "/anthropic/v1/messages",
  "stream": true,
  "pathVars": { "model": "gemini-2.0-flash" },
  "body": { "model": "claude-3-5-sonnet", "max_tokens": 256, "messages": [ ... ], "stream": true }
}
```

- `providerId`（int，必填）：上游 provider id。
- `endpointPath`（string，必填）：provider_endpoint 绑定的端点 path，定位 `upstream_url` 与凭证 resolver。
- `body`（object，必填）：原始上游请求体，后端不解析不重写，原样转发。
- `stream`（bool，可选）：仅用于选择 transport（流式/非流式超时档位）。
- `pathVars`（map，可选）：替换 `upstream_url` 中的 `{name}` 占位（如 gemini 的 `{model}`）。

### 响应

- 成功：透传上游响应。`status` = 上游 status；`Content-Type` 复制自上游；body 流式逐块写回（SSE 实时 flush）。
- 上游返回非 200 业务响应：按原样透传 status + body。
- 接口自身错误（provider/endpoint 未找到、网络失败等）：
  - `404`：`{"message":"provider not found"}` / `{"message":"provider endpoint not found"}` / `{"message":"endpoint not found"}`
  - `502`：`{"message":"<上游连接/转发错误>"}`
  - `400`：`{"message":"invalid request body"}`（请求 JSON 解析失败）

### 不产生的副作用

不写 `request` 行、不写 artifact、不开 jsx session、不解析 MPE、不做模型重写、不跑任何 hook。

## 网关测试

无新增接口。前端命令式 `fetch` 现有目标：

- unified：
  - `POST /api/picotera/v1/messages`
  - `POST /api/picotera/v1/responses`
  - `POST /api/picotera/v1/chat/completions`
  - `POST /api/picotera/v1beta/models/{model}:generateContent`
  - `POST /api/picotera/v1beta/models/{model}:streamGenerateContent`
- 网关 path 端点：所选 `endpoint.path`（含占位时由前端填充）。

请求头：`Content-Type: application/json` + `Authorization: Bearer <apiKey.key>`。

## 复用的现有管理接口（前端表单数据源）

- `GET /api/picotera/providers` — 短路模式选 provider。
- `GET /api/picotera/provider-endpoints?providerId=` — 短路模式选 provider_endpoint。
- `GET /api/picotera/endpoints` — path→endpointType 映射、网关模式选 path 端点。
- `GET /api/picotera/api-keys` — 网关模式选 API key（含明文 `key`）。
