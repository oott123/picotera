# 执行计划

## 后端：短路测试接口

1. **`pkg/server/handle_test_direct.go`（新建）** — `func (s *Server) handleTestDirect(w http.ResponseWriter, r *http.Request)`：
   - 解析请求 JSON 到结构体 `{ ProviderID int32; EndpointPath string; Stream bool; PathVars map[string]string; Body json.RawMessage }`。解析失败 → 400 JSON。
   - `GetProviderByID` / `GetProviderEndpoint` / `GetEndpointByPath`，任一未找到 → 404 JSON。
   - `sendResolver := effectiveSendResolver(endpoint.CredentialsResolver, pe.CredentialsResolver)`。
   - `url, err := substitutePathVars(pe.UpstreamUrl, in.PathVars)`。
   - `http.NewRequestWithContext(r.Context(), POST, url, bytes.NewReader(in.Body))`，设 `Content-Type: application/json`，`applyCredentials(req, provider.Credentials, sendResolver, nil)`。
   - `resp, err := s.forwardRequest(req, provider.ProxyUrl.String, in.Stream)`；err → 502 JSON。
   - 写回：`w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))`，`w.WriteHeader(resp.StatusCode)`，流式 `io.Copy` 并在每次写后 `flusher.Flush()`（若 `w` 实现 `http.Flusher`）。
   - 小工具：复用现有 `writeGatewayError` 或本地写一个 `writeTestError(w, status, msg)` 输出 `{"message":...}`。
2. **`pkg/server/server.go` `registerEndpoints()`** — 在五条 unified 路由之后、`s.router.Mount("/", ...)` 之前加入：
   `s.router.Post("/api/picotera/test/direct", s.handleTestDirect)`。
3. 编译验证：`go build ./...`。

## 前端：请求体构建器

4. **`dashboard/src/lib/testBody.ts`（新建）** — 导出 `type TestFormat`、`type TestFields = { model; system; maxTokens; userMessage; stream }`、`buildTestBody(format, fields): object`。按 design.md 的四/五种格式生成 body，空 system 省略。导出 `endpointTypeToFormat(endpointType): TestFormat | null`（不支持的类型返回 null，用于禁用测试）。

## 前端：数据层手写 fetcher

5. **`dashboard/src/api/client.ts`** — 新增两个命令式流式 fetcher（不经 openapi-fetch）：
   - `postTestDirect(payload): Promise<Response>` — `fetch('/api/picotera/test/direct', ...)`，返回原始 `Response`（调用方决定流式/JSON 处理）。
   - `postGatewayTest(targetUrl, apiKey, body): Promise<Response>` — `fetch(targetUrl, { headers: Authorization Bearer })`。
   - 两者返回原始 `Response`，由视图层用 `useSSEParser` 或 `.json()` 处理；非 2xx 不抛错，交由 UI 展示状态码与 body。

## 前端：视图

6. **`dashboard/src/views/TestView.vue`（新建）** — 主体：
   - `SegmentedControl` 切换 `direct` / `gateway` 模式。
   - 共享结构化表单：`Input`(model)、`Textarea`(system)、`Input[number]`(maxTokens)、`Textarea`(userMessage)、stream 开关（`SegmentedControl` 或 toggle）。
   - 高级区：`CodeEditor` 编辑原始 body；提供「由字段重建」按钮；标记是否手动覆盖。
   - **direct 模式**：provider 选择（`listProviders`）→ provider_endpoint 选择（`listProviderEndpoints` by providerId）→ 由所选端点的 `endpointType` 推断格式（`listEndpoints` 建 path→type 映射）；不支持的格式禁用发送并提示。占位变量输入（如 gemini model→pathVars）。
   - **gateway 模式**：API key 选择（`listApiKeys`）+ 目标选择（unified 五格式 或 `endpoint` 行）；path 占位填充。
   - 「发送」：组装 body（手动覆盖优先），direct 调 `postTestDirect`，gateway 调 `postGatewayTest`。
   - 响应面板：状态码 `StateText`/`Badge`、耗时与 TTFT、`useSSEParser` 实时渲染、原始响应字节查看。错误以可读文本展示。
   - 数据查询用 `@tanstack/vue-query` + `queryKeys`；如缺少 key 在 `queryKeys.ts` 补充对应 list key（providers/endpoints/provider-endpoints/api-keys 多已存在，复用）。
7. **`dashboard/src/router/index.ts`** — 注册 `{ path:'/test', name:'test', component: () => import('@/views/TestView.vue') }`。
8. **`dashboard/src/App.vue`** — `pageMeta` 增加 `test`（title + hint）。
9. **`AppSidebar`** — 增加「测试」导航项（选合适 `IconName`；如缺图标按 `src/ui/icons/paths.ts` 约定补充 `@tabler/icons-vue`）。

## 收尾验证

10. `go build ./...`；短路接口无 contract 改动，**不需要** 重跑 openapi/generate-openapi（路由非 Huma，前端手写调用）。
11. `pnpm --dir dashboard type-check` + `pnpm --dir dashboard lint`。
12. 本地联调：`docker compose up -d` → `mise run server` + `mise run web`，对已配置的 provider/endpoint 跑短路测试（流式与非流式各一次），对某 unified 端点 + API key 跑网关测试并确认 request 历史中出现日志（短路则不出现）。
