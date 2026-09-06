# 执行计划

## 1. 数据库

**`db/migrations/048_endpoint_prefix_match.sql`**（goose）

Up：

1. `ALTER TABLE endpoint ADD COLUMN prefix_match BOOLEAN NOT NULL DEFAULT FALSE;`
2. `UPDATE endpoint SET endpoint_type = 1 WHERE endpoint_type IN (11, 12);`（旧 codex 专属类型退回 general）
3. `CREATE OR REPLACE VIEW completion_endpoint_path AS`
   - `SELECT path AS path FROM endpoint WHERE endpoint_type = ANY(ARRAY[2,3,4,7,8]::int[])`
   - `UNION ALL SELECT e.path || s.suffix AS path FROM endpoint AS e CROSS JOIN unnest(ARRAY['/responses','/responses/compact']::text[]) AS s(suffix) WHERE e.endpoint_type = 14`
   - `UNION ALL SELECT unnest(ARRAY[…047 的七条 unified 路径…]) AS path`

Down：还原 047 的视图定义、`DROP COLUMN prefix_match`；注释说明第 2 步不可逆（哪些行原本是 11 / 12 已无从分辨）。

**`db/queries/endpoint.sql`** —— `UpsertEndpoint` 插入 / 更新 `prefix_match`（新增 `$6`）。

**`db/queries/routing.sql`** —— `GetProvidersByEndpointTypesAndModel` 增选 `e.prefix_match`。

**`db/queries/request.sql`** —— `ListRequests` 的端点筛选改为
`AND (sqlc.narg('endpoint_path')::text IS NULL OR r.endpoint_path = sqlc.narg('endpoint_path') OR starts_with(r.endpoint_path, sqlc.narg('endpoint_path') || '/'))`。

跑 `sqlc generate`。

## 2. 契约层

**`pkg/contract/endpoint.go`**

- 删除 `codexCompact` / `codexSearchV1Alpha` 的 `ToEndpointType` / `FromEndpointType` 分支；`EndpointType_CodexCompact` / `EndpointType_CodexSearchV1Alpha` 两个常量保留并加注释「已废弃，禁止复用，历史请求行仍带此值」。
- 新增 `EndpointType_Codex int32 = 14` 与 `codex` 双向映射。
- `EndpointView` 加 `PrefixMatch bool \`json:"prefixMatch"\``；`endpointType` 的 `enum` tag 更新。
- `ToEndpointView` 带上 `PrefixMatch`。

**`pkg/contract/label.go`** —— `EndpointLabel.EndpointType` 的 `enum` tag 同步。

## 3. 端点路由器与端点写入

**`pkg/server/endpoint_router.go`**

- `compiledEndpoint` 加 `prefix bool`；`load` 时前缀端点跳过 `compilePattern`，直接以 `literalLen = len(path)` 入表（含 `{}` 的前缀行在写入时已被拒，这里防御性跳过）。
- `matchLocked` / `Match` 多返回 `suffix string`：普通端点返回空串；前缀端点要求 `strings.HasPrefix(path, ep.Path+"/")`，`suffix = path[len(ep.Path):]`。
- 更新文件头注释：unified 路由现在是五条固定路由 + `/api/unified/v1/embeddings` + `/api/unified/codex/*` 通配挂载。

**`pkg/server/gateway_helpers.go`**

- `resolveEndpoint` 多返回 `suffix`。
- `buildUpstreamRequest` 新增 `appendPath string` 参数：在 `substitutePathVars` 之后，把 `appendPath` 插到 URL 字符串第一个 `?` / `#` 之前；空串时零开销。

**`pkg/server/handle_endpoint.go`** —— `handleUpsertEndpoint` 按 api.md 的表加四条 400 校验，`UpsertEndpoint` 传 `PrefixMatch`。

## 4. 路径网关

**`pkg/server/handle_gateway.go`**

- `ServeHTTP` 接住 `resolveEndpoint` 的 `suffix`；若匹配到的是前缀端点，用 `r.URL.EscapedPath()` 重切后缀（不以端点路径开头 → 404 `route_not_found`）。
- `newPathGatewayFlowConfig(endpoint, pathVars, suffix)` 设置 `RecordedEndpointPath = endpoint.Path + suffix`，并把 suffix 传给候选集构建。

**`pkg/server/gateway_flow.go`** —— `gatewayFlowConfig` 加 `RecordedEndpointPath string`；meta 行插入处（`EndpointPath`）改读它。

**`pkg/server/gateway_flow_candidates.go`**

- `gatewayCandidateSidecar` 加 `AppendPath string`。
- `buildPathCandidateSet` 多收一个 `suffix`：`AppendPath = suffix`（端点 `prefix_match` 为真时），`EndpointPath = endpoint.Path + AppendPath`。
- `buildUnifiedCandidateSet` 改签名：多收 `suffix string` 与上游格式闭包 `upstreamFormat func(int32) llmbridge.Format`；对 `row.PrefixMatch` 为真的行设 `AppendPath = suffix`、`EndpointPath = row.EndpointPath + suffix`。

**`pkg/server/gateway_flow_attempts.go`** —— `buildRewrittenUpstreamRequest` 把 `input.Sidecar.AppendPath` 传给 `buildUpstreamRequest`。

## 5. unified codex 挂载点

**`pkg/server/unified_routes.go`**

- 路由表删掉 `/api/unified/codex/responses`、`/api/unified/codex/responses/compact`、`/api/unified/v1/alpha/search`，留六条固定路由。
- `unifiedRoute` 加 `UpstreamSuffix string` 与 `Codex bool`。
- 新增常量 `codexMountPath = "/api/unified/codex"`、`codexMountPattern = "/api/unified/codex/*"`，以及 `normalizeCodexSuffix(raw string) (string, bool)`（剥 `/v1`，空后缀返回 false）与 `codexUnifiedRoute(suffix string) unifiedRoute`（按 design.md 的表产出 Path / Format / SourceType）。

**`pkg/server/server.go` `registerEndpoints`** —— 固定路由循环不变，额外注册 `r.Post(codexMountPattern, s.handleUnifiedCodex())` 与对应 `Options`。

**`pkg/server/handle_unified_gateway.go`**

- 新增 `handleUnifiedCodex()`：取 `chi.URLParam(r, "*")` → `normalizeCodexSuffix` → 失败写 404 `route_not_found`；成功用 `codexUnifiedRoute(suffix)` 复用现有 `newUnifiedGatewayFlowConfig`。
- `newUnifiedGatewayFlowConfig` 设置 `RecordedEndpointPath = route.Path`，`ResolveCandidates` 里传 `route.UpstreamSuffix` 与上游格式闭包（codex 类型 → `route.Format`，其余 → `upstreamFormatFor`）。

**`pkg/server/gateway_unified_helpers.go`**

- `candidateEndpointTypes`：透传路由返回 `{route.SourceType}`；`route.Codex && !passthrough()`（即 `/responses`）在四类基础上追加 `contract.EndpointType_Codex`。
- `extractUnifiedModel` / `setUnifiedModel` / `geminiRoute` 无需改动（codex 一律按 body `model`）。

**`pkg/server/user_message_preview.go`** —— 删掉 `EndpointType_CodexCompact` / `EndpointType_CodexSearchV1Alpha` 两处分支，不为 codex 加分支（走 `default` 启发式链）。

## 6. 标签与 OpenAPI

**`pkg/server/handle_label.go`** —— 固定路由照旧遍历 `unifiedRoutes`；额外追加一条 `{Path: codexMountPath, Name: "Unified Codex", EndpointType: "codex"}`。

跑 `mise run openapi` 与 `pnpm --dir dashboard generate-openapi`。

## 7. 前端

- `src/api/index.ts`：`ENDPOINT_TYPE_LABELS` / `ENDPOINT_TYPES_MODEL_ROUTED` 按 design.md 更新。
- `src/components/EndpointForm.vue`：表单加 `prefixMatch`；类型选 codex 时强制勾选并禁用；路径 placeholder / 帮助文案按前缀匹配状态切换。
- `src/views/EndpointsView.vue`：前缀端点路径列加 `/*` 标记。
- `src/utils/requestLabels.ts`：`UNIFIED_ENDPOINT_NAMES` 按 design.md 增删；`unifiedEndpointName` 精确未命中时按最长前缀回退。
- `src/views/RequestsView.vue`：`endpointDisplay` 对非 unified 路径也做最长前缀回退。
- `src/lib/testBody.ts`：注释中的不支持类型清单更新。
- `pnpm --dir dashboard type-check`、`lint`、`format`。

## 8. 测试

- `pkg/server/handle_unified_gateway_test.go`：重写 `TestUnifiedRoutesTable`（六条固定路由），新增 `normalizeCodexSuffix` 表驱动用例（`/responses`、`/v1/responses`、`/responses/compact`、`/v1/alpha/search`、`""`、`/v1`、`/v1x/y`）与 `codexUnifiedRoute` 的 Path / Format / SourceType / 候选类型集合断言。顺带修掉当前 master 上就存在的用例数失配。
- 新增 `pkg/server/endpoint_router_test.go` 用例：前缀端点命中 / 前缀本身不命中 / 同前缀下精确端点优先 / 后缀切分正确。
- `pkg/server/gateway_helpers_test.go`：`buildUpstreamRequest` 的后缀拼接（普通 URL、带查询串的 URL、空后缀）。
- `pkg/server/gateway_flow_candidates` 相关：codex 候选的 `AppendPath` / `EndpointPath` / `UpstreamFormat` 断言。
- `go build ./...`、`go test ./...`。

## 9. 文档

更新根 `CLAUDE.md`：

- 「Unified generation routes」一节改写为六条固定路由 + `/api/unified/codex/*` 挂载点，说明 `/v1` 剥离与 `/responses` 桥接 / 其余透传的分派；
- 「Key Patterns」的 Endpoint matching 段补前缀匹配规则与后缀拼接；
- 数据库一节补 `endpoint.prefix_match`、migration 048 的 `completion_endpoint_path` 新定义与 11 / 12 的废弃说明。
