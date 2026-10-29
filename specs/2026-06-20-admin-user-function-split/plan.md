# 执行计划

## 后端

### 1. 标签契约 `pkg/contract/label.go`（新增）

- 定义视图：`ProviderLabel{ID int32; Name string}`、`ModelLabel{Name string}`、`EndpointLabel{Path, Name string; EndpointType string}`、`ProjectLabel{ID int64; Name string}`（ID 类型对齐各自 `db` 模型）。
- 转换函数：`ToProviderLabel(db.Provider)` 等四个。
- 响应类型：`ListProviderLabelsResponse{ Body []ProviderLabel }` 等四个。
- operation：`OperationListProviderLabels`/`...Models`/`...Endpoints`/`...Projects`，路径 `/labels/{providers,models,endpoints,projects}`，`Method GET`，`Tags:["Label"]`。
- 字段对齐核对：读 `pkg/contract/provider.go`、`model.go`、`endpoint.go`、`project.go` 现有视图，确认 `EndpointType` 的枚举字符串映射函数（如 `FromEndpointType`）复用。

### 2. 标签 handler `pkg/server/handle_label.go`（新增）

- 四个方法 `(s *Server) handleListProviderLabels` 等，分别调用既有 `s.queries.ListProviders/ListModels/ListEndpoints/ListProjects`，`map` 为标签视图返回。
- 核对各 list 查询的入参（部分可能带过滤参数）：读 `db/queries/*.sql` 对应方法签名，传与现有 list handler 相同的「全量」参数。

### 3. group 拆分与注册 `pkg/server/server.go`

- 新增中间件方法 `(s *Server) requireAdmin(ctx huma.Context, next func(huma.Context))`（见 design.md），用 `auth.UserFromContext(ctx.Context())` + `huma.WriteErr`。
- `registerOperations`：建 `mgmt` 与 `admin := huma.NewGroup(s.api, "/api/picotera")`；`admin.UseMiddleware(s.requireAdmin)`。
- 按 design.md 的归类表把每条 `huma.Register` 落到正确 group：管理操作移到 `admin`，用户操作留 `mgmt`，并在 `mgmt` 注册 4 个新标签 operation。
- 同步更新 `NewHuma()`：同样双 group 注册（保证 openapi 完整）。可抽出公共 `func (s *Server) register(mgmt, admin *huma.Group)` 供二者复用，避免两份注册列表漂移。

### 4. 短路测试管理员校验 `pkg/server/handle_test_direct.go`

- 在 `handleTestDirect` 入口读 `auth.UserFromContext(r.Context())`；`nil` → 500；`!IsAdmin` → 写 `403 {"message":"admin required"}` 返回。沿用该文件既有 JSON 错误写法。

### 5. 重新生成 spec 与类型

- `mise run openapi` 重写 `openapi.yaml`。
- `pnpm --dir dashboard generate-openapi` 重生成 `dashboard/src/openapi-types.d.ts`。

### 6. 后端校验

- `go build ./...`。
- `go test ./pkg/server/...`（确认现有助手测试不受 group 改动影响）。

## 前端

### 7. API 层 `dashboard/src/api/`

- `queryKeys.ts`：新增 `labels` 键族：`labels.providers`、`labels.models`、`labels.endpoints`、`labels.projects`（沿用 `all` 风格）。
- `client.ts`：新增 `listProviderLabels`/`listModelLabels`/`listEndpointLabels`/`listProjectLabels`，调用对应 `api.GET('/api/picotera/labels/...')`，错误经 `ApiRequestError` 统一抛出。
- `index.ts`：如需，re-export 新标签类型。

### 8. 权限 composable `dashboard/src/composables/useMe.ts`（新增）

- `useQuery({ queryKey: queryKeys.me, queryFn: fetchMe })`，导出 `me`、`isAdmin = computed(() => me.value?.isAdmin ?? false)`。

### 9. 侧栏两栏 `dashboard/src/components/AppSidebar.vue`

- 改用 `useMe`（替换内联 `useQuery(fetchMe)`，底部用户名沿用 `me`）。
- `nav` 拆为 `userNav` 与 `adminNav` 两数组（归类见 design.md）。
- 模板渲染两个分区：分区标题「用户功能」「管理功能」；`adminNav` 整段包 `v-if="isAdmin"`。

### 10. 路由守卫 `dashboard/src/router/index.ts`

- 定义 `ADMIN_ROUTES` 集合（管理组路由 name）。
- `router.beforeEach(async (to) => { if (!ADMIN_ROUTES.has(to.name)) return true; const me = await queryClient.ensureQueryData({ queryKey: queryKeys.me, queryFn: fetchMe }); return me.isAdmin ? true : { name: 'overview' } })`。从 `@/api/queryClient` 取共享 `queryClient`。

### 11. 测试页 `dashboard/src/views/TestView.vue`

- 引入 `useMe`。`modeOptions` 改 `computed`：`isAdmin` 时含 `direct`+`gateway`，否则仅 `gateway`；`mode` 初值在非管理员时强制 `gateway`（`watchEffect` 或初始化判断）。
- direct 模式 UI 已有 `v-if="mode==='direct'"`，无需隐藏额外块。
- 查询守卫：`providersQuery`（保留 `listProviders`，仅 direct 用到 `providerModels`）与 `providerEndpointsQuery` 加 `enabled: computed(() => isAdmin.value && mode.value==='direct')`（后者再 && providerId）。
- 网关模式数据：`endpointsQuery`、`modelsQuery` 改用 `listEndpointLabels`/`listModelLabels` + `queryKeys.labels.*`。`endpointTypeByPath`、`endpointPathOptions`、模型 ComboBox 字段不变（标签含 `path`/`name`/`endpointType` 与 `name`）。

### 12. 概览 `dashboard/src/views/OverviewView.vue`

- `listProviders`/`listModels`/`listProjects` → 标签 fetcher + `queryKeys.labels.*`。`listApiKeys` 不变。
- 核对引用字段：图例 / 过滤只用 `id`、`name`，确认无对完整字段的依赖（如有则调整）。

### 13. 请求 `dashboard/src/views/RequestsView.vue`

- `listModels`/`listEndpoints` → 标签 fetcher + `queryKeys.labels.*`（只用 `name` / `path`+`name`）。
- provider/project 过滤经 `useProvidersMap`/`useProjectsMap`（步骤 14 改造后自动生效）。

### 14. Map composables

- `useProvidersMap.ts`：`listProviders` → `listProviderLabels`，`queryKey` → `queryKeys.labels.providers`。`providerLabel`/`providers` 输出形态不变（id→name）。
- `useProjectsMap.ts`：`listProjects` → `listProjectLabels`，`queryKey` → `queryKeys.labels.projects`。
- 核对：确认这两个 composable 不被任何**管理视图**用到完整字段（仅用于 id→name）。`TracesView` 仅用 `projectLabel`，符合。

### 15. 前端校验

- `pnpm --dir dashboard type-check`。
- `pnpm --dir dashboard lint`。
- `pnpm --dir dashboard build`。

## 联调验证（手动）

- 管理员登录：两栏全显示；进入各管理页正常；短路测试可用。
- 非管理员（`is_admin=false`）：侧栏仅「用户功能」；直接输入管理路由 URL 被重定向到概览；概览 / 请求 / 追踪图例与过滤名称正常（经标签接口）；网关测试可用、短路测试模式不出现；直接调用任一管理 API 返回 403、`/test/direct` 返回 403。
