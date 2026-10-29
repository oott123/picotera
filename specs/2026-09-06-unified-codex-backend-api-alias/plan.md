# 执行计划

## 1. `pkg/server/unified_routes.go`

在 codex 常量块里：

- 保留 `codexMountPath = "/api/unified/codex"`，注释补一句：它同时是**记录用的规范前缀**（别名请求也记这个）。
- 新增 `codexBackendAPIMountPath = "/api/unified/backend-api/codex"`，注释说明它镜像 ChatGPT 自身的 `<host>/backend-api/codex` 布局，是同一个挂载的别名。
- 删除 `codexMountPattern`，改为包级变量：

  ```go
  // codexMountPatterns are the chi wildcard registrations for the codex mount.
  // Both prefixes resolve to the same handler, the same suffix normalization and
  // the same recorded endpoint_path (always codexMountPath + suffix).
  var codexMountPatterns = []string{
      codexMountPath + "/*",
      codexBackendAPIMountPath + "/*",
  }
  ```

- 文件头部注释里「Codex is NOT in this list…」那段，补一句两个前缀共用同一个通配挂载。

`normalizeCodexSuffix`、`codexUnifiedRoute` 不改。

## 2. `pkg/server/server.go` — `registerEndpoints`

把

```go
codex := s.handleUnifiedCodex()
r.Post(codexMountPattern, codex)
r.Options(codexMountPattern, codex)
```

替换为遍历 `codexMountPatterns` 注册同一个 handler（`codex := s.handleUnifiedCodex()` 仍只构造一次）。注释补充：第二个前缀是 ChatGPT 布局的别名，剩余部分的处理完全一致。

## 3. `pkg/server/handle_unified_gateway.go`

`handleUnifiedCodex` 的逻辑不动，只更新其文档注释：把 “serves the /api/unified/codex/* wildcard mount” 改成两个前缀共用的挂载，并说明记录路径永远归一到 `codexMountPath`。

## 4. 测试 `pkg/server/handle_unified_gateway_test.go`

新增 `TestCodexMountPatterns`：用一个裸 `chi.NewRouter()` 注册 `codexMountPatterns`（handler 里做 `normalizeCodexSuffix("/" + chi.URLParam(r, "*"))` 并把 `codexUnifiedRoute(suffix).Path` 写回响应体），配合 `httptest` 逐条断言 —— 不需要 `Server` / DB：

| 请求 | 期望 |
| --- | --- |
| `/api/unified/codex/responses` | 200，body `/api/unified/codex/responses` |
| `/api/unified/backend-api/codex/responses` | 200，body `/api/unified/codex/responses` |
| `/api/unified/backend-api/codex/v1/responses` | 200，body `/api/unified/codex/responses` |
| `/api/unified/backend-api/codex/responses/compact` | 200，body `/api/unified/codex/responses/compact` |
| `/api/unified/backend-api/codex/v1` | 404（`normalizeCodexSuffix` 返回 false） |
| `/api/unified/backend-api/codex/` | 404 |

现有 `TestNormalizeCodexSuffix` / `TestCodexUnifiedRoute` 不变。

## 5. 文档 `CLAUDE.md`

「Unified generation routes」一节：

- 路由清单里 `POST /api/unified/codex/*` 那行后面加同一挂载的别名 `POST /api/unified/backend-api/codex/*`。
- 「The Codex mount」段落补一句：挂载注册在 `codexMountPatterns` 两个前缀上（`/api/unified/codex` 与镜像 ChatGPT 布局的 `/api/unified/backend-api/codex`），后缀归一化与 `endpoint_path` 记录一律按 `codexMountPath` 走，所以别名不影响 `completion_endpoint_path`、连续聚合与端点筛选。

## 6. 验证

```bash
go build ./... && go test ./pkg/server/
```

无 sqlc / 迁移 / OpenAPI / dashboard 改动，因此不跑 `sqlc generate`、`mise run openapi`、`pnpm generate-openapi`。

手工冒烟（可选，需本地起服务与一个 codex 类型 provider endpoint）：对 `/api/unified/backend-api/codex/responses` 发一次请求，确认上游收到 `<端点URL>/responses`，且请求列表里该行的端点显示为 `Unified Codex`（`endpoint_path` 为 `/api/unified/codex/responses`）。
