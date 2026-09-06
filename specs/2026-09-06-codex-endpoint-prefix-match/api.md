# API 变更

## 管理 API

### `EndpointView`（`GET/PUT /api/picotera/endpoints`）

```jsonc
{
  "name": "Codex",
  "path": "/api/codex",
  "modelPath": "model",
  "credentialsResolver": "bearerToken",
  "endpointType": "codex",   // 新增枚举值；删除 codexCompact / codexSearchV1Alpha
  "prefixMatch": true         // 新增字段
}
```

`endpointType` 枚举：`general`、`openaiChatCompletions`、`openaiResponses`、`anthropicMessages`、`anthropicCountTokens`、`geminiGenerateContent`、`geminiStreamGenerateContent`、`exaSearch`、`modelList`、`codex`、`openaiEmbedding`、`unknown`。

`prefixMatch`（`boolean`，必填，默认 `false`）：开启后该端点按前缀匹配请求路径，前缀之后的部分原样接到上游 URL 后面。

`PUT /api/picotera/endpoints` 新增 400 校验：

| 条件 | 错误 |
| --- | --- |
| `endpointType == "codex"` 且 `prefixMatch != true` | `codex endpoint must enable prefixMatch` |
| `prefixMatch` 且 `path` 含 `{` 或 `}` | `prefix-match endpoint path must not contain path variables` |
| `prefixMatch` 且 `path` 以 `/` 结尾 | `prefix-match endpoint path must not end with /` |
| `prefixMatch` 且 `path` 含 `%` 或非 ASCII 字符 | `prefix-match endpoint path must be plain ASCII without percent-encoding` |

### `EndpointLabel`（`GET /api/picotera/labels/endpoints`）

`endpointType` 枚举同步。unified 合成标签由九条减为七条：五条固定生成路由 + `/api/unified/v1/embeddings` + 新增的 `{"path": "/api/unified/codex", "name": "Unified Codex", "endpointType": "codex"}`；删除 `/api/unified/codex/responses`、`/api/unified/codex/responses/compact`、`/api/unified/v1/alpha/search` 三条。

### `GET /api/picotera/requests`

`endpointPath` 查询参数由精确相等改为**前缀感知**：命中 `endpoint_path = <值>` 或 `endpoint_path` 以 `<值>/` 开头的行。因此选中 `/api/codex` 或 `/api/unified/codex` 能一次筛出该前缀下所有子路径的请求。其余筛选参数不变。

## 网关路由

### 路径网关

前缀端点 `path = /api/codex`、`prefixMatch = true`、绑定渠道的 `upstream_url = https://example.com/codex`：

| 请求 | 上游 URL | 记录的 `endpoint_path` |
| --- | --- | --- |
| `POST /api/codex/responses` | `https://example.com/codex/responses` | `/api/codex/responses` |
| `POST /api/codex/responses/compact` | `https://example.com/codex/responses/compact` | `/api/codex/responses/compact` |
| `POST /api/codex/alpha/search` | `https://example.com/codex/alpha/search` | `/api/codex/alpha/search` |
| `POST /api/codex` | 404 `route_not_found`（前缀端点不服务前缀本身） | — |

上游 URL 自带查询串时后缀插在 `?` 之前：`https://example.com/codex?v=1` + `/responses` → `https://example.com/codex/responses?v=1`。客户端查询串照旧由 `mergeClientQuery` 合并。

### unified codex 挂载点

`POST|OPTIONS /api/unified/codex/*`。归一化：路径中紧跟前缀的 `/v1` 段被剥离，因此 `…/api/unified/codex` 与 `…/api/unified/codex/v1` 两种 base_url 等价。

| 请求路径 | 归一化后缀 | 源格式 | 候选端点类型 | 记录的 `endpoint_path` |
| --- | --- | --- | --- | --- |
| `/api/unified/codex/responses`<br>`/api/unified/codex/v1/responses` | `/responses` | OpenAI Responses | `anthropicMessages`、`openaiChatCompletions`、`openaiResponses`、Gemini（按 stream 选变体）、`codex` | `/api/unified/codex/responses` |
| `/api/unified/codex/responses/compact`<br>`/api/unified/codex/v1/responses/compact` | `/responses/compact` | —（透传） | `codex` | `/api/unified/codex/responses/compact` |
| `/api/unified/codex/alpha/search`<br>`/api/unified/codex/v1/alpha/search` | `/alpha/search` | —（透传） | `codex` | `/api/unified/codex/alpha/search` |
| 其它子路径 | 原样 | —（透传） | `codex` | `/api/unified/codex` + 后缀 |
| `/api/unified/codex`、`/api/unified/codex/`、`/api/unified/codex/v1` | — | — | — | 404 `route_not_found` |

选中 codex 类型候选时上游 URL 为 `upstream_url + 归一化后缀`，upstream 请求行记 `codex 端点 path + 归一化后缀`；选中其它类型候选（只可能在 `/responses` 上）时上游 URL 与今天一致，不追加后缀。

所有子路径都按请求体的 `model` 字段选路，缺失或为空 → 400 `model_not_found`；候选为空 → 404 `no_provider_available`。

### 删除的路由

- `POST /api/unified/v1/alpha/search` —— 改由 `/api/unified/codex/alpha/search` 承接。
- `POST /api/unified/codex/responses`、`POST /api/unified/codex/responses/compact` 作为**固定路由**删除，改由通配挂载点服务；对客户端而言 URL 与行为不变（`/responses` 仍可跨格式桥接）。
