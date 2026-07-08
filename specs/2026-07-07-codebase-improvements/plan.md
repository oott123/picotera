# 修复执行计划

本计划仅覆盖用户指定的 15 项：**C1 C2 C3 R1 R2 R4 V1 V3 E1 E2 P2 F1 F2 N4 N14**。
每项给出确切改动位置、方案与验收标准。问题背景详见 `design.md`。

严格遵守项目约定：不引入兼容层/回退分支；输入校验 fail-fast，非法输入直接报错。

---

## 分组与执行顺序

按耦合度与风险分为五组，组内可并行，组间无强依赖，可任意顺序执行：

1. **后端正确性 Bug**：C1、C2、C3
2. **生命周期/可靠性**：R1、R2、R4
3. **fail-fast 校验**：V1、V3
4. **JSX / 插件一致性与性能**：E1、E2、P2
5. **前端**：F1、F2、N4
6. **文档修正**：N14

---

## 组一：后端正确性 Bug

### C1 — 请求列表的项目过滤是空操作

- **位置**：`pkg/server/handle_requests.go:143-159`
- **现状**：`handleListRequests` 构造 `db.ListRequestsParams` 时未设置 `ProjectID`；`ListRequestsRequest.ProjectID`（`request.go:378`）、SQL 过滤（`request.sql`）、`ListRequestsParams.ProjectID`（`request.sql.go:352`）均已就绪，仅遗漏接线。
- **方案**：
  1. 在 :95-130 的过滤变量区新增 `filterProjectID`，遵循既有 `input.ProviderID != 0` 模式：
     ```go
     var filterProjectID pgtype.Int4
     if input.ProjectID != 0 {
         filterProjectID = pgtype.Int4{Int32: input.ProjectID, Valid: true}
     }
     ```
  2. 在 `ListRequests` 调用参数中加入 `ProjectID: filterProjectID`。
- **验收**：带 `projectId` 查询请求列表时，结果被正确按项目过滤；`go build ./...` 通过。

### C2 — 流式 200 提交后失败被伪造为 502

- **位置**：`pkg/server/gateway_unified_helpers.go` — `failUnifiedSuccess`（:633-653），调用点 :456/:467/:474/:502（均在 :432 `WriteHeader(200)` 之后）
- **现状**：响应流已开始（200 已提交）后，bridge/web-search 失败调用 `failUnifiedSuccess` → `writeGatewayError(a.w, 502, ...)`：无法改变客户端已发出的 200 状态，写出畸形响应体，并把行状态错误记为 502。
- **方案**：区分「200 提交前」与「200 提交后」两类失败，不再共用同一 `failUnifiedSuccess`。
  1. **200 提交前**的失败（decode 前，:427 那一处 `failUnifiedSuccess`）：保持写 502 语义（此时可正常写状态）。
  2. **200 提交后**的失败（:456/:467/:474/:502 四处，均在 `WriteHeader(200)` 之后）：新增 `failUnifiedSuccessInStream(ctx, a, errMsg)`：
     - 以源格式（`a.srcFormat`）向 `a.w` 注入一个 in-stream error 事件（SSE error frame，参照 `response_extractor.go` 检测的错误格式；非流式 JSON 分支写 JSON error object），随后正常收尾流。
     - 两行（upstream + meta）状态码记为已提交的 `200`，`FinishReason` 记为 `db.FinishReasonStreamError`，`ErrorMessage` 记 `errMsg`。
     - meta 响应 artifact 用已写出的字节（含注入的 error 事件）。
  3. 将四处 200 后调用点从 `failUnifiedSuccess` 改为 `failUnifiedSuccessInStream`。
- **注**：源格式 error frame 的构造复用 web-search 已有的 SSE 写法（`writeSSE` 风格）与 `db.FinishReasonStreamError` 常量；不新增 llmbridge 往返。
- **验收**：模拟 bridge/web-search 在 200 后失败时，客户端收到合法的源格式 in-stream error 事件；request 行 status=200、finishReason=StreamError；不再出现 502 与畸形体。

### C3 — 200 提交后 StartClientWrite 失败导致行永久未完成

- **位置**：`pkg/server/gateway_flow_success.go:168-173`（path 路由 `openPathInternalReader`）；`pkg/server/gateway_unified_helpers.go:433-437`（unified 路由）
- **现状**：`w.WriteHeader(200)` 后 `StartClientWrite()` 失败时，仅 `cancel()` + 关闭 reader 后 `return`，两行永久停留未完成，无 meta 响应 artifact。对照 `derr` 分支（:150-166）有完整收尾。
- **方案**：两处 StartClientWrite 失败分支镜像各自的 in-stream 收尾逻辑（body 已无法改，仅收尾行）：
  - **path 路由**（`gateway_flow_success.go:169-173`）：用 `input.Flow.ctxs.Persist()` 派生 `bgCtx`，调用 `completeFailedAttemptWithReason(bgCtx, UpstreamID, UpstreamCreatedAt, AttemptStart, 200, "start client write: "+err, db.FinishReasonCancelled, resp.Header)`，并 `failMeta` + 上传 meta 响应 artifact（状态记 200、finishReason Cancelled、errMsg）。
  - **unified 路由**（`gateway_unified_helpers.go:433-437`）：复用 C2 新增的 `failUnifiedSuccessInStream`（或其行收尾部分），status=200、finishReason=Cancelled、errMsg="start client write: "+err，上传 meta 响应 artifact。
- **决策**：`FinishReason` 用 `FinishReasonCancelled`（客户端连接不可写等价于取消），与 C2 的 StreamError 区分。
- **验收**：StartClientWrite 失败后，upstream 与 meta 行均被收尾（status=200、finishReason=Cancelled、有 errMsg 与 meta artifact），不再残留未完成行。

---

## 组二：生命周期 / 可靠性

### R1 — 无优雅关闭

- **位置**：`cmd/picotera/main.go:23-37`；`pkg/server/server.go:364-372`（`Serve`）
- **现状**：`Serve()` 用裸 `http.ListenAndServe`，无信号处理、无 `http.Server.Shutdown`；`OnStart` 注册但无 `OnStop`。退出时 pgxpool、llmbridge 插件子进程、artifact sink worker 均不排空。
- **方案**：
  1. **`server.go`**：`Serve()` 改用持有的 `*http.Server`（存到 `Server.httpServer` 字段），`ListenAndServe` 忽略 `http.ErrServerClosed`。新增 `Server.Shutdown(ctx)`：
     ```
     httpServer.Shutdown(ctx)  → llmBridge.Close(ctx)  → artifacts.Close(ctx)(见下)  → db.Close()
     ```
     - `pgxpool.Pool.Close()` 已有；`llmBridge.Close(ctx)` 已存在（R2 修其终止性）。
     - **artifact sink 排空**：`pkg/artifacts/sink.go` 的 `Sink` 接口新增 `Close(ctx context.Context) error`；实现关闭 jobs channel、等 worker 排空（带 ctx 超时）。这是 R1 收尾 artifact worker 的最小前置（不含 R8 的丢包 metric）。
  2. **`main.go`**：`OnStart` 改为在 goroutine 中 `Serve()`；`humacli` 的 `OnStop` 钩子里派生带超时（如 25s）的 `context.Background()`，调 `server.Shutdown(ctx)`。humacli 自身处理 SIGINT/SIGTERM 并触发 OnStop。
- **决策**：关闭顺序固定为 HTTP → 插件 → artifact → DB（先停止入站，再排空在途，最后断连接）。超时 25s（留 humacli 默认宽限余量）。
- **验收**：`go build ./...` 通过；SIGTERM 后进程等待在途请求收尾、artifact 排空、插件子进程正常退出，无强杀。

### R2 — llmbridge `Close` 非终止（Close 后仍会重启插件）

- **位置**：`pkg/llmbridge/plugin_client.go:159-166`（`Close`）、:100-120（`acquire`/`reacquire`/`restartLocked`）
- **现状**：`Close()` 杀子进程但不设终止标志，`b.client` 仍非 nil；下次 `acquire()` 见 `Exited()` 即 `restartLocked()` 重启，Close 变成非终止。
- **方案**：
  1. `pluginBridge` 结构新增 `closed bool`（受 `b.mu` 保护）。
  2. `Close` 在锁内置 `b.closed = true` 后再 `Kill`。
  3. 新增 sentinel `var errClosed = errors.New("llmbridge: bridge closed")`；`acquire` 与 `reacquire` 进入后先检查 `if b.closed { return nil, nil, errClosed }`，`restartLocked` 同样在开头短路。
- **验收**：`Close` 后任何 `BridgeRequest`/`BridgeStream`/`acquire` 返回 `errClosed`，不再重启子进程；`go test ./pkg/llmbridge/...` 通过（`plugin_client_test.go` 的 cleanup 不受影响）。

### R4 — web-search 子轮非 200 提前返回泄漏 recorder goroutine

- **位置**：`pkg/server/web_search_stream_loop.go:84-91`
- **现状**：`rec := newStreamingResponseRecorder(); go func(){ defer rec.Close(); ... ServeHTTP }()` 后，若 `rec.StatusCode() != 200` 提前 `return`，`rec.Reader()`（`rec.pr`）未关，`ServeHTTP` 侧 pw 写阻塞、goroutine 泄漏。
- **方案**：在子轮循环体内，`rec` 创建后立即 `defer rec.Reader().Close()`（覆盖非 200 提前返回、正常路径、`forwardSubStream` 出错返回三条路径）。因循环每轮新建 `rec`，`defer` 需绑定到每轮作用域——将每轮迭代体抽成闭包或用局部函数，使 `defer` 在每轮结束即触发，避免累积到 `run()` 结束。
- **决策**：将 for 循环体（:72-111）抽成 `func() (shouldContinue bool)` 局部闭包，内部 `defer rec.Reader().Close()`；`run()` 按返回值决定 break/continue，保持原有 `fallbackPauseTurn` 语义。
- **验收**：子轮返回非 200 时无 goroutine 泄漏（可用 `runtime.NumGoroutine` 前后对比或 `go.uber.org/goleak` 风格断言，若不引依赖则代码审查确认所有返回路径均关闭 `rec.Reader()`）；现有 web-search 测试通过。

---

## 组三：fail-fast 校验

### V1 — config Unmarshal 错误被丢弃

- **位置**：`pkg/configx/configx.go:108`
- **现状**：`viper.Unmarshal(&config)` 返回值被忽略，解码失败静默以零值/默认启动。
- **方案**：
  1. 捕获错误：`if err := viper.Unmarshal(&config); err != nil { return nil, fmt.Errorf("configx: unmarshal: %w", err) }`。
  2. 在既有 auth 后置校验（:110-112）旁补充显式必填校验，违例返回 error：
     - `config.DatabaseURL == ""` → 报错。
     - `config.Port <= 0 || config.Port > 65535` → 报错。
     - 至少一个 auth provider 启用：`!SingleUserMode && !HeaderEnabled` → 报错（"no auth provider enabled"）。
     - S3：`config.S3.Endpoint != ""` 时要求 `AccessKey` 与 `SecretKey` 非空 → 否则报错。
- **决策**：严格 fail-fast，不做任何默认兜底；上述四项为最小必要集，均在 `Parse()` 内返回 error 使启动失败。
- **验收**：`go build ./...` 通过；坏 duration/类型、缺 database_url、无 auth provider、S3 端点缺凭据等场景启动即报清晰错误。

### V3 — 畸形 endpoint 路径静默不可路由

- **位置**：`pkg/server/handle_endpoint.go:30-58`（`handleUpsertEndpoint`）；`pkg/server/endpoint_router.go:174-178`（load 时静默 skip）
- **现状**：upsert 不校验路径；router load 时 `compilePattern` 失败静默 `continue`，端点变成无声不可路由。
- **方案**：
  1. `handleUpsertEndpoint` 在 :37 写库前调用 `compilePattern(input.Body.Path)`，失败返回 `huma.Error400BadRequest("invalid endpoint path", err)`（`compilePattern` 已导出于同包，返回的 error 已含花括号不平衡/重复变量/坏 token 名/regex 编译失败等原因）。
  2. `endpoint_router.go` 的 `load` 静默 skip 保留（防止单条坏数据打挂全表路由的运行时保护），但因 upsert 已拦截，正常路径不会再产生坏数据。
- **决策**：校验点放在 upsert（写入即拒），不改 load 的容错兜底——两者职责不同，非兼容层。
- **验收**：upsert 花括号不平衡/重复变量的路径返回 400 并带原因；合法路径正常写入并可路由；现有 endpoint 测试通过。

---

## 组四：JSX / 插件一致性与性能

### E1 — `isInterrupt` 字符串匹配误判

- **位置**：`pkg/jsx/session.go:273-275`（`isInterrupt`）、:245-259（`evalJSON`）
- **现状**：`isInterrupt` 用 `strings.Contains(err.Error(), "interrupted")` 判定超时，会误捕脚本 `throw new Error('...interrupted...')`、含该词的网络/KV 错误，误置 `tainted=true` 并返回 `ErrHookTimeout`，永久禁用本请求剩余 hook。
- **背景**：QuickJS 中断异常经 `errFromException`（`third_party/quickjs/quickjs.go:1079-1104`）渲染为 `InternalError: interrupted` 文本，无类型化 sentinel 暴露到 Go 侧。
- **方案**：**用超时可观测状态替代字符串匹配**，不改 third_party：
  - `evalJSON` 在调用 `EvalValueFile` 前记录本次是否设置了有限超时；出错后判定「中断」的依据改为：`vm` 的中断/超时确实被触发。具体做法——在 `qjsSession` 侧维护一个 watchdog 或超时标记：`SetEvalTimeout(s.timeout())` 后，若 `s.timeout() > 0` 且 eval 耗时 `>= timeout`（用单调时钟测量 eval 前后耗时）且返回 error，则判定为超时中断，置 `tainted`；否则视为普通脚本错误，返回 `fmt.Errorf("jsx: %s: %w", name, err)` 不 taint。
  - 精确化：`isInterrupt` 保留为窄匹配 `strings.Contains(msg, "InternalError: interrupted")`（QuickJS 中断的固定前缀，见 `examples_test.go` 输出），并**同时**要求上述耗时判据成立，二者取交集，杜绝脚本自造字符串命中。
- **决策**：采用「耗时判据 + 窄前缀匹配」双条件，避免依赖 third_party 内部改造（third_party 不宜改动），同时消除误判。
- **验收**：脚本 `throw new Error('request interrupted')` 只使当前 hook 以 502 失败、不 taint、不禁用后续 hook；真实超时（死循环到 timeout）仍正确 taint 并 503；`go test ./pkg/jsx/...` 通过。

### E2 — sortProviders / rewriteProviderModels 解码失败静默吞

- **位置**：`pkg/jsx/session.go:328-333`（`RunSortProviders`）、:580-584（`RunRewriteProviderModels`）
- **现状**：这两者 `json.Unmarshal` 失败时 Debug 日志后返回 `(initial, nil)` 静默吞；而 `RunRewriteModel`/`RunBeforeRequest`/`RunAfterUpstreamError`/`RunRewriteRequest` 均返回解码错误（经 failHook 显 502）。语义分歧且未文档化。
- **方案**：统一为 fail-fast，与其余 hook 一致：
  - `RunSortProviders`：解码失败改为 `return initial, fmt.Errorf("jsx: sortProviders decode: %w", err)`，删除 Debug-吞逻辑。
  - `RunRewriteProviderModels`：解码失败改为 `return initial, fmt.Errorf("jsx: rewriteProviderModels decode: %w", err)`。
- **验收**：畸形 sortProviders/rewriteProviderModels 输出触发 502（经 failHook），运维可见；`go test ./pkg/jsx/...` 通过；调用方（`gateway_flow_attempts.go`、fetch-models 流程）已有 error 处理路径，确认编译通过。

### P2 — 插件流式 `streamWriter.Write` 多余 make+copy

- **位置**：`cmd/picotera-llmbridge-plugin/main.go:227-237`（`streamWriter.Write`）
- **现状**：`Write` 每次 `make([]byte,len(p)) + copy` 再 `Send`。输入 `p` 来自 `bridge_stream.go:116` 的 `encodeSSEEvent(ev)`——每事件全新分配、写出后不复用，故 copy 纯属多余开销。
- **方案**：`streamWriter.Write` 直接用 `p` 构造 `BridgeStreamChunk_Data{Data: p}` 发送，去掉 `out := make+copy`。
- **契约保证**：确认 `Pump`（`bridge_stream.go:110-125`）每轮调用 `encodeSSEEvent` 返回**新** buffer，`w.Write` 返回后不再触碰该 buffer；gRPC `Send` 在返回前完成序列化（proto marshal 拷入自有缓冲），不持有 `p` 引用。在 `streamWriter.Write` 上方加注释记录「调用方保证每次传入独立、不复用的 buffer」。
- **决策**：直接发 `p` 并文档化契约（符合 design.md P2 方向）。
- **验收**：`go build ./cmd/picotera-llmbridge-plugin/...` 通过；流式转换内容不变（现有 `aggregate_test`/identity passthrough 测试通过）。

---

## 组五：前端

### F1 — TestView 流式 fetch 卸载不清理

- **位置**：`dashboard/src/views/TestView.vue:391,442-500,503-505`
- **现状**：`controller: AbortController` 仅在点击 stop 时 abort；组件卸载时（导航离开）无 `onBeforeUnmount`/`onUnmounted`，ReadableStream reader 循环继续跑、改已卸载组件状态、连接保持至流完成。
- **方案**：在 `<script setup>` 中新增：
  ```ts
  import { onBeforeUnmount } from 'vue'   // 合并进现有 vue import
  onBeforeUnmount(() => controller?.abort())
  ```
- **验收**：流式请求进行中导航离开 TestView，请求被 abort、reader 循环终止、无对已卸载组件的状态写入；`pnpm --dir dashboard type-check` 通过。

### F2 — 确认对话框 / Overlay 无可访问性与键盘支持

- **位置**：`dashboard/src/ui/ConfirmDialog.vue:21-39`；`dashboard/src/ui/Overlay.vue:10-21`；`dashboard/src/composables/useConfirm.ts`
- **现状**：模态无 `role`/`aria-modal`、无 Escape 关闭、无 focus trap、无 focus 管理。全部 9 处 `confirm.require` 皆为破坏性删除，当前仅鼠标可用。
- **方案**（聚焦 ConfirmDialog，Overlay 补 role 供复用）：
  1. **ConfirmDialog.vue**：
     - 外层 dialog 容器加 `role="dialog" aria-modal="true"`，`aria-label`/`aria-describedby` 关联消息文本。
     - 打开时（`watch(() => confirmState.visible)`）记录 `document.activeElement`，将焦点移到「删除」按钮；关闭时复焦到之前元素。
     - 注册 `keydown`：Escape → `reject()`；Tab/Shift+Tab 在对话框内两个按钮间循环（focus trap，仅取消/删除两个可聚焦元素，实现从简：监听 keydown 在首尾元素上拦截 Tab 折返）。
     - 事件监听在 visible 变 true 时 `window.addEventListener('keydown', ...)`，变 false 或 `onBeforeUnmount` 时移除。
  2. **Overlay.vue**：容器加 `role="dialog" aria-modal="true"`（F9/SidePanelHost 复用同规范，但本次仅改 Overlay 自身以支撑 ConfirmDialog 视觉一致；不扩展到 SidePanelHost，属计划外）。
- **决策**：本次范围仅 ConfirmDialog（承载全部删除确认）+ Overlay 的 role 属性；不含 SidePanelHost（F9，未选）。focus trap 用最简两按钮折返实现，不引第三方库（遵守本地 UI 库约定）。
- **验收**：确认框打开时焦点在「删除」，Escape 取消，Tab 在两按钮间循环不逃逸，`aria-modal` 就位；`pnpm --dir dashboard type-check && pnpm --dir dashboard lint` 通过。

### N4 — 身份切换只 invalidate、渲染上一身份缓存

- **位置**：`dashboard/src/views/UsersView.vue:68-73`（`impersonate`）；`dashboard/src/components/AppSidebar.vue:20-23`（`stopImpersonating`）
- **现状**：切换/还原 impersonation 只 `queryClient.invalidateQueries()`，所有 user-scoped query key 不含身份维度。切换后页面先渲染上一身份缓存，refetch 失败时 stale 数据继续留存。
- **方案**：将两处的 `invalidateQueries()` 改为**先清空缓存**再切换/导航：
  - `impersonate`（UsersView）：
    ```ts
    async function impersonate(u) {
      impersonation.start({ id: u.id, displayName: u.displayName })
      await router.push({ name: 'overview' })
      queryClient.clear()   // 清空而非 invalidate，杜绝渲染上一身份缓存
    }
    ```
  - `stopImpersonating`（AppSidebar）：
    ```ts
    async function stopImpersonating() {
      impersonation.stop()
      queryClient.clear()
    }
    ```
  - 顺序：先改 impersonation 状态（使后续 openapi-fetch 中间件注入正确身份头）→ 导航 → `clear()`（清空后各页面以新身份重新拉取，无 stale 窗口）。
- **决策**：用 `queryClient.clear()` 全量清空（身份切换是全局身份变更，所有 user-scoped 数据均失效），比给每个 query key 加身份维度改动更小且更安全。
- **验收**：切换/还原身份后，Overview/Requests/API keys/Projects 等页面不再闪现上一身份数据；refetch 失败时展示空/错误态而非 stale；`pnpm --dir dashboard type-check` 通过。

---

## 组六：文档修正

### N14 — CLAUDE.md project-matching 段与实现严重漂移

- **位置**：`CLAUDE.md`「Project matching」段、`CLAUDE.md:124`（"runs all five JS hooks"）、`AGENTS.md:90`（hook 计数）
- **现状**：
  - (a) 称有 `Server.projectRouter` 内存最长前缀缓存、project 表变更「必须调 `Invalidate()`」——**该缓存不存在**（全仓无 `projectRouter` 符号；`project_extractor.go` 每请求走 `MatchProjectByPaths` 活查询）。
  - (b) 称「三条固定正则」，实际 **6 条**（`project_extractor.go`）。
  - (c) hook 计数错：CLAUDE.md/AGENTS.md 称 "five"/列 6 项，实为 **7 个**（漏 `beforeTransform`）。
- **方案**：修正文档（仅文档，无代码）：
  1. 「Project matching」段：删除 `projectRouter` 缓存与 `Invalidate()` 相关描述，改为「每请求经 `project_extractor.go` 的 `MatchProjectByPaths` 同步 DB 活查询匹配（无内存缓存）」。
  2. 「三条固定正则」→ 更正为实际条数（核对 `project_extractor.go` 现有正则，写明数量与各自匹配的锚点）。
  3. `CLAUDE.md:124`「runs all five JS hooks」→ "seven"；补列 `beforeTransform`。
  4. `AGENTS.md:90` hook 段：改「seven」并补 `beforeTransform`（驱动 llmbridge outbound-profile 选择，`gateway_flow_attempts.go` 调用）。
- **前置核对**：修改前 `grep -c` 复核 `project_extractor.go` 的正则实际条数与 hook 注册处（`sdk.js`/`iface.go`）的实际数量，以代码为准写入文档。
- **验收**：文档描述与代码一致；`grep -rn projectRouter` 无残留引用于 CLAUDE.md；hook 计数与正则条数与实现相符。

---

## 全局验收

- 后端：`go build ./...` 与 `go test ./...` 全通过（重点 `pkg/server`、`pkg/jsx`、`pkg/llmbridge`）。
- 插件：`mise run llmbridge-plugin` 构建通过。
- 前端：`pnpm --dir dashboard type-check`、`pnpm --dir dashboard lint` 通过。
- 契约无变更（C1 仅接线既有字段，不动 openapi）——无需重生成 `openapi.yaml`/TS 类型。
- 无兼容层/回退分支引入；所有输入校验为 fail-fast 且返回清晰错误。
