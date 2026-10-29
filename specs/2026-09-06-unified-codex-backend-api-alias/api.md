# API

管理 API（`/api/picotera`）无变更，`openapi.yaml` 与 dashboard 生成类型均不需要重新生成。仅网关侧接受的 URL 空间扩大。

## 新增可用挂载点

`POST /api/unified/backend-api/codex/*`（以及对应的 `OPTIONS` 预检）

与 `POST /api/unified/codex/*` 完全等价：

| 请求路径 | 归一化后缀 | 记录的 `endpoint_path` | 源格式 |
| --- | --- | --- | --- |
| `/api/unified/backend-api/codex/responses` | `/responses` | `/api/unified/codex/responses` | OpenAI Responses（可桥接） |
| `/api/unified/backend-api/codex/v1/responses` | `/responses` | `/api/unified/codex/responses` | OpenAI Responses（可桥接） |
| `/api/unified/backend-api/codex/responses/compact` | `/responses/compact` | `/api/unified/codex/responses/compact` | 透传，仅 codex 类型上游 |
| `/api/unified/backend-api/codex/alpha/search` | `/alpha/search` | `/api/unified/codex/alpha/search` | 透传，仅 codex 类型上游 |

404 情形同样一致：`/api/unified/backend-api/codex`、`/api/unified/backend-api/codex/`、`/api/unified/backend-api/codex/v1`、`/api/unified/backend-api/codex/v1/` 返回 `404 route_not_found`（裸挂载点不落进 codex 处理器时由网关兜底 404 / SPA，行为与本体一致）。

认证：与其它 `/api/unified` 路由相同，用 API Key。
