# 设计：前端功能的管理员 / 用户划分

## 目标

在已有的用户身份模型（`2026-06-18-multi-user`、`2026-06-19-user-management`）与资源归属（`2026-06-20-user-resource-ownership`）之上，引入第一层基于 `is_admin` 的能力授权：

- 后端：管理员功能的接口强制只允许管理员访问。
- 前端：按 `/me.isAdmin` 把界面分为用户 / 管理两栏，自动隐藏无权页面。
- 用户功能依赖的共享配置数据通过新增的轻量标签接口获取，不暴露完整配置与敏感字段。

`is_admin` 至此首次进入授权路径——此前仅用于 `me` 视图与 `set-admin` CLI。

## 功能归类

| 分类 | 功能 | 管理 API 操作 |
| --- | --- | --- |
| 用户 | 概览 | overview summary / distribution / series / speed-boxplot |
| 用户 | 密钥 | api-key list/get/create/update/delete（已按 owner 隔离） |
| 用户 | 请求 | request list/get/list-by-span/list-traces/get-live/interrupt（已按 user 隔离） |
| 用户 | 追踪 | request list-traces（已按 user 隔离） |
| 用户 | 测试·网关测试 | 经 API Key 走 `/api/unified` 与网关 catch-all（非管理 API） |
| 用户 | `/me` | get-me |
| 用户 | 汇率（读） | exchange-rate list（货币换算依赖，用户功能可读） |
| 用户 | 标签（新增） | label providers/models/endpoints/projects/upstream-models |
| 管理 | 渠道 | provider list/get/create/upsert/update-models/delete、fetch-models |
| 管理 | 模型 | model list/get/put/delete |
| 管理 | 端点 | endpoint list/upsert/delete、provider-endpoint list/upsert/delete |
| 管理 | 项目 | project list/get/upsert/delete/merge |
| 管理 | 脚本 | script list/get/create/update/delete |
| 管理 | 缓存 | kv list/get/upsert/delete |
| 管理 | 汇率（写） | exchange-rate get/put/delete、match-pricing（list 见用户组） |
| 管理 | 用户 | user list/get/create/update/delete、user-identity 全部 |
| 管理 | 设置 | global-setting list/get/upsert/delete |
| 管理 | 模拟 | simulate-dispatch |
| 管理 | 测试·短路测试 | `POST /api/picotera/test/direct`（原始 chi 路由） |

`is_admin` 不进入用户功能：管理员与普通用户在密钥 / 请求 / 追踪 / 概览上仍只看本人数据（沿用 `2026-06-20-user-resource-ownership` 的「不设管理员旁路」约定）。`is_admin` 只决定能否进入管理功能。

## 后端鉴权机制

### Huma 管理操作：双 group + 管理员中间件

`registerOperations` 现在把所有管理操作注册在单个 `mgmt := huma.NewGroup(s.api, "/api/picotera")` 上。改为两个 group，共用同一前缀：

```go
mgmt   := huma.NewGroup(s.api, "/api/picotera")               // 全体登录用户
admin  := huma.NewGroup(s.api, "/api/picotera")               // 仅管理员
admin.UseMiddleware(s.requireAdmin)
```

Huma v2.37 支持同一 API 上注册多个 group；每个 operation 独立注册，两个 group 产生的路径都在 `/api/picotera/...` 下，互不冲突。

`requireAdmin` 中间件（`func(ctx huma.Context, next func(huma.Context))`）：

```go
func (s *Server) requireAdmin(ctx huma.Context, next func(huma.Context)) {
    u := auth.UserFromContext(ctx.Context())
    if u == nil {
        _ = huma.WriteErr(s.api, ctx, http.StatusInternalServerError, "no user in context")
        return
    }
    if !u.IsAdmin {
        _ = huma.WriteErr(s.api, ctx, http.StatusForbidden, "admin required")
        return
    }
    next(ctx)
}
```

中间件在 auth 中间件（chi 层，已把 user 写入 context）之后运行，故 `UserFromContext` 必非空；`nil` 视为接线 bug → 500，与 `handleGetMe` 一致。

用户 group（`mgmt`）注册：me、overview ×4、api-key ×5、request ×7（list/get/list-by-span/list-traces/get-live/interrupt + 其它）、新增 label ×4。
管理 group（`admin`）注册：provider、model、endpoint、provider-endpoint、project、script、kv、exchange-rate、match-pricing、fetch-models、global-setting、user、user-identity、simulate。

`NewHuma()`（仅用于生成 openapi）同样改为双 group 注册，保证 `openapi.yaml` 含全部操作。

### 短路测试：原始 chi 路由的管理员校验

`/api/picotera/test/direct` 是注册在 `mgmtRouter` 上的原始 chi handler（已有 user auth）。在 `handleTestDirect` 入口加管理员校验：读取 `auth.UserFromContext(r.Context())`，非管理员写 `403 {"message":"admin required"}` 并返回。网关测试不经此路由，不受影响。

## 标签接口（用户功能的共享配置数据）

用户功能需要四类管理资源的名称用于展示与过滤，但不应能读到完整配置（尤其渠道 `credentials`）。为此新增四个只读标签接口，注册在用户 group 上：

- `GET /api/picotera/labels/providers` → `[{ id, name }]`
- `GET /api/picotera/labels/models` → `[{ name }]`
- `GET /api/picotera/labels/endpoints` → `[{ path, name, endpointType }]`
- `GET /api/picotera/labels/projects` → `[{ id, name }]`

端点标签含 `endpointType`，因网关测试的「配置端点」模式需据其推断请求体格式（`TestView` 的 `endpointTypeByPath`）；该字段非敏感。

**不新增 sqlc 查询**：标签 handler 复用既有 `ListProviders` / `ListModels` / `ListEndpoints` / `ListProjects`，在 handler 内投影为标签视图。代价是仍从库里读出完整行后丢弃多余字段；这些表规模小、列表本就被管理页频繁读取，可接受，换取零 SQL 改动与单一数据来源。

## 契约与 OpenAPI

- 新增 `pkg/contract/label.go`：四个标签视图 + 四个 operation 定义 + 转换函数。
- `/me` 已含 `isAdmin`，无需改动。
- 改动后重新生成 `openapi.yaml`，再生成前端 TS 类型。

## 前端

### 权限来源

新增 `useMe` composable：基于已有 `fetchMe` + `queryKeys.me` 的 `useQuery`，导出 `me`、`isAdmin`（`computed(() => me.value?.isAdmin ?? false)`）。`AppSidebar` 当前直接 `useQuery(fetchMe)`，改用 `useMe`。

### 侧栏两栏

`AppSidebar` 的单一 `nav` 数组拆为两组并按分类渲染两个分区标题（「用户功能」「管理功能」）：

- 用户组：overview、apiKeys、requests、traces、test。
- 管理组：providers、models、endpoints、projects、scripts、kv、rates、users、simulate、settings。

非管理员（`!isAdmin`）不渲染整个管理组。`activeRouteName` 逻辑保留（requestDetail → requests）。

### 路由守卫

`router.beforeEach`：维护一份管理路由名集合；当目标路由属管理组且 `!isAdmin` 时 `redirect` 到 `/overview`。`isAdmin` 取自 `queryClient` 缓存的 `me`（守卫内用 `queryClient.ensureQueryData({ queryKey: queryKeys.me, queryFn: fetchMe })` 读取，避免守卫早于 me 加载）。`/me` 401 等错误不在本守卫处理（沿用现有行为）。

### 测试页内的短路测试

`TestView` 对所有用户开放，但短路测试模式仅管理员可见：

- `modeOptions` 在 `!isAdmin` 时去掉 `direct` 项，`mode` 默认 `gateway`。
- 仅 direct 模式使用的查询（`listProviders` 经标签化后无需，`listProviderEndpoints` 仍为管理接口）以 `enabled: isAdmin && mode==='direct'` 守卫，避免非管理员触发 403。

### 用户功能改用标签接口

被锁定的完整列表接口（`listProviders`/`listModels`/`listEndpoints`/`listProjects`）对非管理员返回 403，故用户功能改读标签接口：

- 新增 fetcher：`listProviderLabels`、`listModelLabels`、`listEndpointLabels`、`listProjectLabels`，与新增 query key（`queryKeys.labels.*`）。
- `useProvidersMap` / `useProjectsMap` 改用标签 fetcher（其只用 id→name 映射，标签字段已够）。
- `RequestsView`：provider/project 过滤经上述 map；`listModels`/`listEndpoints` 改为标签 fetcher（只用 `name` / `path`+`name`）。
- `OverviewView`：`listProviders`/`listModels`/`listProjects` 改为标签 fetcher（图例与过滤只需名称）；`listApiKeys` 不变（本就用户功能）。
- `TestView` 网关模式：`listEndpoints`/`listModels` 改标签 fetcher（含 `endpointType`）。direct 模式所需 `listProviders` 也改标签 fetcher（direct 表单只用渠道名 + `providerModels`……）见下。

`TestView` direct 模式的渠道下拉用到 `p.providerModels`（模型 ComboBox），这是完整 `ProviderView` 字段，标签接口不含。由于 direct 模式仅管理员可见，该处保留 `listProviders`（管理接口，管理员可访问），以 `enabled: isAdmin` 守卫；非 direct 模式不读。即：**TestView 渠道完整数据仅在管理员的 direct 模式下加载**，网关模式不依赖渠道。

### 共享视图统一走标签接口（不按角色分支）

概览 / 请求 / 追踪 / 网关测试这几个共享视图（及 `useProvidersMap`/`useProjectsMap`）对所有角色**一律**使用标签 fetcher，**不引入 `isAdmin` 分支**。理由：

- 标签接口对全体登录用户开放，管理员同样可读，无功能缺失。
- 共享视图只需 id→name / path / endpointType，标签字段已足够；按角色切换 fetcher 只会在这些地方堆叠条件逻辑，无收益。

代价是对管理员存在一份轻微的缓存冗余：同一渠道数据会以两个 query key 各存一份——管理视图（`ProvidersView` 等）用完整接口的 `queryKeys.providers.all`，共享视图用 `queryKeys.labels.providers`，各发一次请求。这些表规模小、stale time 30s，冗余可忽略；换取的是共享视图对所有角色行为一致、代码无分支。非管理员只有标签那一份（管理视图不可达）。

### 受影响视图清单（前端）

- 改造：`AppSidebar.vue`（两栏 + useMe）、`router/index.ts`（守卫）、`TestView.vue`（模式按权限、查询守卫、标签 fetcher）、`RequestsView.vue`、`OverviewView.vue`、`useProvidersMap.ts`、`useProjectsMap.ts`、`api/client.ts`（新增 4 个标签 fetcher）、`api/queryKeys.ts`（新增 labels key）。
- 不改造：各管理视图（`ProvidersView` 等）继续用完整接口，仅靠路由守卫 + 侧栏隐藏阻止非管理员进入。

## 安全与失败语义

- 后端是唯一权威：即便前端守卫被绕过，管理操作仍返回 403。标签接口不含敏感字段，是用户能读到的全部共享配置信息。
- 非管理员误访管理操作 → 403；缺失 context user（不应发生）→ 500。
- 短路测试非管理员 → 403；网关测试不受影响。

## 不做项

- 不引入第三方库或算法。
- 不改动 `2026-06-20-user-resource-ownership` 的数据隔离逻辑（密钥 / 请求 / 追踪 / 概览仍按 owner，管理员无旁路）。
- 不为模拟设计脱敏层（直接归管理员）。
- 不引入按资源的所有权到渠道 / 模型 / 端点 / 项目（保持全局共享，仅区分读标签 vs 管理）。
- 不做兼容层 / 回退分支。
