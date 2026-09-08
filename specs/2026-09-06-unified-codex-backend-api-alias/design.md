# 设计

## 背景

`/api/unified/codex/*` 是一个 chi 通配挂载（`pkg/server/server.go` `registerEndpoints`），处理器 `handleUnifiedCodex` 把通配剩余部分交给 `normalizeCodexSuffix` 归一化（剥掉前导 `/v1` 段），再由 `codexUnifiedRoute(suffix)` 现算出本次请求的 `unifiedRoute`。ChatGPT 自身的 Codex 上游布局是 `<host>/backend-api/codex/responses`，所以把 `base_url` 指到 `…/api/unified` 的客户端会打到 `/api/unified/backend-api/codex/...`，当前落进网关兜底 → 404 / SPA。

## 方案

在同一个挂载上再注册一个别名前缀，别名之后的一切逻辑不变。

**挂载点列表化。** `pkg/server/unified_routes.go` 里 `codexMountPath` 旁边新增 `codexBackendAPIMountPath = "/api/unified/backend-api/codex"`，并用 `codexMountPatterns` 取代单个 `codexMountPattern`：

```go
var codexMountPatterns = []string{codexMountPath + "/*", codexBackendAPIMountPath + "/*"}
```

`registerEndpoints` 遍历该列表，对每个 pattern 注册同一个 `s.handleUnifiedCodex()`（POST + OPTIONS，仍在 `corsMiddleware` 分组内）。这是一个纯粹的替换，不保留旧的单例常量。

**处理器不变。** `handleUnifiedCodex` 读的是 chi 的 `*` 通配参数，chi 给出的是「匹配到的 pattern 之后的剩余部分」，所以别名下 `/responses`、`/v1/responses` 的剩余量与本体完全一致，`normalizeCodexSuffix` / `codexUnifiedRoute` 一行都不用改。

**记录路径天然归一。** `codexUnifiedRoute` 用 `codexMountPath + suffix` 拼 `route.Path`，而 `route.Path` 同时是 `RecordedEndpointPath`（meta 行的 `endpoint_path`）和虚拟端点的 `Path`。别名请求因此记 `/api/unified/codex/responses` 而非别名路径 —— 迁移 048 建立的 `completion_endpoint_path` 白名单、三个连续聚合、`ListRequests` 的 `endpoint_path` 前缀筛选、`handle_label.go` 里那一条 `Unified Codex` 标签全部不受影响，本次变更零迁移、零 SQL、零契约改动，`openapi.yaml` 与 dashboard 类型也无需重新生成。

**上游 URL 不变。** 候选构造用的是 `route.UpstreamSuffix`（归一化后缀），与请求打在哪个别名上无关，因此上游拿到的仍是 `<端点URL>/responses` 等。

## 影响面

- 认证：`/api/unified` 前缀不在 `auth.Middleware` 守护的 `/api/picotera` 之下，别名同样走 API Key 认证，无额外处理。
- 路由冲突：`/api/unified/backend-api/codex/*` 与现有六条固定 unified 路由无前缀重叠。
- 桥接行为：别名下的 `/responses` 依旧是 `FormatOpenAIResponses` 源，可桥接到 Anthropic / Gemini / ChatCompletions 上游；其余子路径依旧是 codex-only 透传。
