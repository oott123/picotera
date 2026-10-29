# 设计：移除"模拟"功能

## 背景

"模拟"功能（`simulateDispatch`，admin 操作 `POST /api/picotera/simulate/dispatch`）运行网关流水线的前半段（endpoint 解析 → rewriteModel → 候选解析 → sortProviders），返回排序后的候选渠道列表与脚本日志，但不发起任何上游请求、不写入 `request` 行。前端为其提供 `SimulateView.vue` 页面与 `SimulateResultPanel.vue` 结果面板。

该功能是一个独立的只读诊断工具，不被其它功能依赖，可整体删除。

## 范围与依赖分析

### 后端

- `pkg/contract/simulate.go` — 全文件仅服务于该功能，整体删除。
- `pkg/server/handle_simulate.go` — 整体删除。其中以下辅助函数经确认**仅**被本文件使用，随文件一并消失：
  - `simulateBeforeTransform`
  - `mapGatewayError`
  - `hookHumaError`
  - `ptrMap`
  - `simulateFormatFromString`
  - `formatForEndpointType`
- `pkg/server/server.go:306` 的注册行 `huma.Register(admin, contract.OperationSimulateDispatch, s.handleSimulateDispatch)` 删除。

以下被 `handle_simulate.go` 调用但属于网关/unified 共享的函数**保留**：`sourceEndpointType`、`unifiedRoutePath`、`setUnifiedModel`、`resolveProviders`、`resolveProvidersByTypes`、`candidateEndpointTypes`、`upstreamFormatFor`、`sourceEndpointType` 等。

`gateway_helpers.go:403` 注释中提到 "(and the simulate path branch)"，更新该注释以去除对 simulate 的引用。

无数据库迁移、无 sqlc 查询涉及，无需改动。

### 前端

- 删除 `dashboard/src/views/SimulateView.vue`
- 删除 `dashboard/src/components/SimulateResultPanel.vue`
- `dashboard/src/router/index.ts` — 删除 `validRouteNames` 集合中的 `'simulate'` 项与 `/simulate` 路由定义。
- `dashboard/src/components/AppSidebar.vue` — 删除 `{ name: 'simulate', label: '模拟', icon: 'geometry' }` 入口。
- `dashboard/src/App.vue` — 删除标题映射中的 `simulate` 条目。
- `dashboard/src/api/client.ts` — 删除 `simulateDispatch` 函数及其 `SimulateDispatchRequestBody` / `SimulateDispatchResponseBody` 类型导入。
- `dashboard/src/api/index.ts` — 删除 `SimulateDispatchRequestBody` / `SimulateDispatchResponseBody` / `SimulateCandidate` / `SimulateLogEntry` 四个类型再导出。

### 生成产物

- `openapi.yaml` 由 `mise run openapi` 重新生成，自动移除全部 `Simulate*` schema 与 `/api/picotera/simulate/dispatch` 路径。
- `dashboard/src/openapi-types.d.ts` 由 `pnpm --dir dashboard generate-openapi` 重新生成。
- `dashboard/src/api/openapi.ts` 是一个无人引用、不在构建链路中的孤立旧生成文件（live 文件是 `src/openapi-types.d.ts`），整体删除。

### 不在范围内

- `third_party/axonhub/**` 中出现的 "simulate" 与本功能无关，不触碰。
- 历史 spec 目录 `specs/2026-05-18-simulate-dispatch`、`specs/2026-05-26-simulator-candidate-reuse` 作为历史记录保留。

## 验证

- `go build ./...` 通过（确认无悬空引用、无未使用辅助函数导致的编译错误）。
- `go test ./pkg/server/...` 通过。
- `pnpm --dir dashboard type-check` 与 `pnpm --dir dashboard build` 通过。
- 全仓 `grep -ri simulate`（排除 `third_party/`、`specs/`）仅余无关命中。
