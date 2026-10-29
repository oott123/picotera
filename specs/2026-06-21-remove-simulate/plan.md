# 执行计划：移除"模拟"功能

## 1. 后端删除

1. 删除文件 `pkg/contract/simulate.go`。
2. 删除文件 `pkg/server/handle_simulate.go`。
3. `pkg/server/server.go`：删除第 306 行 `huma.Register(admin, contract.OperationSimulateDispatch, s.handleSimulateDispatch)`。
4. `pkg/server/gateway_helpers.go:403`：将注释中 "(and the simulate path branch)" 的描述去除。

## 2. 前端删除

5. 删除文件 `dashboard/src/views/SimulateView.vue`。
6. 删除文件 `dashboard/src/components/SimulateResultPanel.vue`。
7. `dashboard/src/router/index.ts`：删除 `validRouteNames` 中的 `'simulate'`，删除 `/simulate` 路由定义行。
8. `dashboard/src/components/AppSidebar.vue`：删除 simulate 导航项。
9. `dashboard/src/App.vue`：删除标题映射中的 `simulate` 条目。
10. `dashboard/src/api/client.ts`：删除 `simulateDispatch` 函数，删除 `SimulateDispatchRequestBody`、`SimulateDispatchResponseBody` 两个类型导入。
11. `dashboard/src/api/index.ts`：删除 `SimulateDispatchRequestBody`、`SimulateDispatchResponseBody`、`SimulateCandidate`、`SimulateLogEntry` 四个再导出。
12. 删除孤立旧生成文件 `dashboard/src/api/openapi.ts`（无人引用、不在构建链路）。

## 3. 重新生成契约与类型

13. 运行 `mise run openapi` 重新生成 `openapi.yaml`。
14. 运行 `pnpm --dir dashboard generate-openapi` 重新生成 `dashboard/src/openapi-types.d.ts`。

## 4. 验证

15. `go build ./...`
16. `go test ./pkg/server/... ./pkg/contract/...`
17. `pnpm --dir dashboard type-check`
18. `pnpm --dir dashboard build`
19. 全仓 `grep -rni simulate` 排除 `third_party/`、`specs/`、`node_modules/`，确认仅余无关命中。
