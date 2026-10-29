# 执行计划

## 步骤 1：unified meta path 模板化（后端）

文件：`pkg/server/handle_unified_gateway.go`，`newUnifiedGatewayFlowConfig` 内 `virtualEndpoint`。

- 将 `Path: r.URL.Path,` 改为 `Path: unifiedRoutePath(srcFormat),`。
- 同步更新该字段附近的注释，说明现在用注册路由模式（含 `{model}`）而非实际请求 path。

文件：`pkg/server/gateway_unified_helpers.go`（`unifiedStreamArgs` 上方注释，约 264 行）。

- 更新注释：`metaEndpointPath` 现在是 unified 路由模式（如 `/api/unified/v1beta/models/{model}:generateContent`），不再是实际请求 path。

## 步骤 2：端点栏最大宽度（前端）

文件：`dashboard/src/views/RequestsView.vue`，`#cell-endpointPath` 插槽（约 599 行）。

- 把 `<span class="font-mono text-ink-faint">` 改为
  `<span class="block max-w-[16rem] truncate font-mono text-ink-faint" :title="row.endpointPath">`。

## 步骤 3：验证

- 后端：`go build -o /dev/null ./cmd/picotera`（确认编译通过）；`go test ./pkg/server/...`（确认未破坏现有单测）。
- 前端：`pnpm --dir dashboard type-check` 与 `pnpm --dir dashboard lint`。
- 无 contract 变更，**不需要**重新生成 `openapi.yaml` 或 TS 类型。

## 不涉及

- 无数据库迁移、无 sqlc 变更、无 OpenAPI/SDK 再生成。
- 不回填历史数据：已记录的 unified gemini 旧请求仍是具体 path，仅新请求采用模板化值。
