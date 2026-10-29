# 代码库改进研究 — 设计文档

## 方法

对 PicoTera 后端（`pkg/*`、`cmd/*`、`db/*`）与前端（`dashboard/src/**`）进行只读审查（不含 `third_party/`、`node_modules`）。五个并行审查覆盖：网关请求流、JSX 脚本引擎、llmbridge 转换器、数据层与契约、Vue 仪表盘。第二轮审查（Opus 复审）逐条复核既有发现并补充覆盖入站体量、绑定数据完整性、身份切换缓存、管理 CRUD 语义、凭据脱敏面、CORS、JSX 大 body 物化与文档漂移。每条发现均给出确切位置（`file:line`）。共整理 69 项发现，按主题与严重度分组如下。

严重度约定：**🔴 高（正确性/安全/可靠性缺陷）** · **🟡 中（应修但不紧急）** · **⚪ 低（改进/清理）**。

> **复审结论**：既有发现绝大多数仍成立，无一被最近提交修复；R2/R5 经证据下调、T2/F4/F5/F7 影响收窄（见「十一」节末的严重度更正）。最重要的元发现是 **N14——`CLAUDE.md` 的 project-matching 段与实现严重漂移（projectRouter 缓存不存在、正则数量与 hook 计数均错）**，会误导后续开发与审查判断，应优先修正文档。

---

## 一、已确认的正确性 Bug

| # | 位置 | 问题 | 方向 |
|---|------|------|------|
| C1 🔴 | `pkg/server/handle_requests.go:143-159` | `handleListRequests` 构造 `db.ListRequestsParams` 时**未设置 `ProjectID`**，但请求类型 `ListRequestsRequest.ProjectID`（`request.go:378`）、SQL 过滤（`request.sql:19`）、`ListRequestsParams.ProjectID`（`request.sql.go:352`）都存在。结果 narg 为 NULL，项目过滤被静默跳过——仪表盘请求列表的项目筛选是空操作。概览端点却正确接线了 project_id，说明这是遗漏而非有意。 | 在 params 中加 `ProjectID: filterProjectID`（0 时无效，与其他过滤一致）。 |
| C2 🔴 | `pkg/server/gateway_unified_helpers.go:633-653`（`failUnifiedSuccess`，调用点 :456/:467/:474/:502，均在 :432 `WriteHeader(200)` 之后） | 200 已提交后，bridge/web-search 失败时调用 `writeGatewayError` 尝试写 502——既无法改变客户端状态（流已开始），又会写出畸形响应并**误记状态为 502**。 | 200 之后失败应改为以源格式注入 in-stream error 事件（SSE error frame / JSON error object），并把行状态记为已提交的 200 + 错误标记（参照已有的 in-stream `StreamError` 路径），而非伪造 502。 |
| C3 🔴 | `pkg/server/gateway_flow_success.go:168-173`；`gateway_unified_helpers.go:433-437` | 200 提交后 `StartClientWrite` 失败时，meta 行与 upstream 行**永久停留在未完成状态**（无 `completeFailedAttemptWithReason`/`failMeta`、无 meta 响应 artifact）。对照上方 `derr` 分支（:150-166）有正确收尾。 | StartClientWrite 失败时镜像 `derr` 分支：补全 upstream 行（`FinishReasonCancelled/Internal`）、failMeta、上传 meta 响应 artifact。body 已无法改，但行必须收尾。 |

---

## 二、可靠性与生命周期

| # | 位置 | 问题 | 方向 |
|---|------|------|------|
| R1 🔴 | `cmd/picotera/main.go:25-36`；`pkg/server/server.go:364-372` | 无优雅关闭：`Serve()` 用裸 `http.ListenAndServe`，无信号处理、无 `http.Server.Shutdown`；`OnStart` 注册了但**无 `OnStop`**。退出时 pgxpool、llmbridge 插件子进程、artifact sink worker 均不排空，在途转换/上传被进程树强杀。 | 注册 `OnStop`：`http.Server.Shutdown(ctx)` → 关闭 pgxpool → `llmBridge.Close(ctx)` → 排空 artifact worker。用 `&http.Server{}` + `Shutdown`。 |
| R2 🔴 | `pkg/llmbridge/plugin_client.go:159-166`(`Close`) | `Close()` 杀死当前子进程但**不设 `closed` 标志**，`b.client` 仍非 nil。下次请求 `acquire()` 见 `client.Exited()` 即调 `restartLocked()` **重启插件**——Close 非终止。叠加 R1，生产中 Close 路径实为死代码。 | Close 在 mu 下置 `closed=true`；`acquire`/`restartLocked` 检查后返回 sentinel（如复用 `errDisabled`）。 |
| R3 🔴 | `pkg/jsx/session.go:145,149,165`（`newSession`）；`third_party/quickjs/quickjs.go:368-388,501` | 顶层脚本 eval（sdk.js、ctxInit、每个 enabled 脚本）在 `SetEvalTimeout` **之前**执行。`m.timeout==0` 时 `configureInterrupt` 设 `timeLimit=0`（无中断）；纯 CPU 死循环 `while(true){}` 不分配内存，`SetMemoryLimit` 无效。代码从不从 watchdog 调 `vm.Interrupt()`，QuickJS 不观察 Go request context。有缺陷/被篡改的 enabled 脚本顶层循环会**永久挂住网关 goroutine**，客户端断连与 ctx 取消都无法解除。 | 每次顶层 `EvalFile` 前调 `SetEvalTimeout`；更稳的做法是起一个绑定 `s.ctx` 的 watchdog goroutine，在取消/硬截止时调 `vm.Interrupt()`（SetEvalTimeout 仅在 JS 中断检查点生效，对 C 层阻塞无效）。 |
| R4 🟡 | `pkg/server/web_search_stream_loop.go:84-91` | `webSearchSSELoopDriver` 在子轮返回非 200 时提前返回，但**未关闭 `rec`**，子请求 `ServeHTTP` goroutine 泄漏（`rec.pr` 未关，pw 写阻塞）。 | 早期返回前 `rec.Reader().Close()`；或 `defer rec.Reader().Close()` 覆盖整个循环体。 |
| R5 🟡 | `pkg/server/gateway_flow.go:321-327` | `authenticateAndBackfill` 派生的 `upsertProjectSeen` goroutine 用传入 ctx 派生 5s 超时；请求流结束后 ctx 取消，**静默丢弃 project-seen 更新**。 | 绑定 `context.Background()`（自带 5s 超时），如 artifact sink worker 那样。 |
| R6 🟡 | `pkg/llmbridge/plugin_client.go:433-438`(`Close` 等 `<-done`)、:361-390(recv)、:325-331(`closeAll` 仅 `CloseSend`) | `pluginStreamReadCloser.Close()` 阻塞于 `<-done` 无超时；`closeAll` 仅 `CloseSend`（发半关闭）不能解除阻塞的 `Recv`。当前仅因调用方恰好在 `clientReader.Close()` 前 `cancel()` ctx 才不挂死——**依赖未文档化的调用方约定**。 | 给 BridgeStream 自己的 `context.WithCancel`，在 `closeAll` 取消；或用 `stream.Close()`（双向取消）而非 `CloseSend`。 |
| R7 🟡 | `pkg/server/server.go:364-372`；`gateway_flow.go` readBody 无 deadline | 入站 HTTP server 无 `ReadHeaderTimeout/ReadTimeout/WriteTimeout/IdleTimeout`——slowloris 攻击面 + 无界 body 读取。 | 用 `&http.Server{ReadHeaderTimeout:...}`；至少 `ReadHeaderTimeout` 关闭 slowloris 窗口。 |
| R8 🟡 | `pkg/artifacts/sink.go:20-24,91-93,126-132` | `Sink` 接口无 `Close/Shutdown`；4 个 worker + 至多 256 排队 artifact 在退出时被遗弃不排空；`Put()` 队列满时**静默丢弃**。对以 artifact 为主要调试面的网关，每次部署/重启都有数据丢失窗口。 | 接口加 `Close(ctx)`：关 jobs channel、等 worker 排空（带超时）、在优雅关闭中调用。丢包时暴露 metric。 |
| R9 ⚪ | `pkg/server/gateway_helpers.go:987-1009` | `idleTimeoutReader.Read` 每 32KB chunk 起一个 goroutine，超时后仍阻塞在底层 read。 | 单长存 reader goroutine + 每 Read deadline channel，或 `context.AfterFunc`。 |
| R10 ⚪ | `pkg/server/web_search_loop.go:176-190`、`web_search_stream_loop.go:79-85` | Web-search 仿真**重新进入完整网关管线**（经 httptest recorder），每轮产生重复 request 行/traces/artifact。 | 内部 in-process 分发路径，跳过 meta 行/auth 重解析/artifact 存储，携带父 meta ID 合并结果；至少标记子轮行为仿真派生以便过滤/不计费。 |

---

## 三、安全

| # | 位置 | 问题 | 方向 |
|---|------|------|------|
| S1 🔴 | `db/migrations/010_api_key_management.sql:2-8`；`db/queries/api_key.sql:7-8`；`pkg/server/gateway_helpers.go:188` | 迁移 010 丢弃了原 `api_key_hash BYTEA`+`api_key_masked TEXT`，改为 `key TEXT NOT NULL` + 明文唯一索引，`GetApiKeyByKey` 精确字符串查找。**所有下游 API key 明文存储**——任何 DB 读权限（DBA、逻辑副本、pg_dump、流式复制）即获全部在用 key。`ApiKeyView` 还把完整明文 Key 返回给 mgmt 组（`api_key.go:16,34`）。 | 存 key 哈希（查找列）+ masked 前缀（展示列），索引哈希；若展示确需明文则加密静态存储（KMS/应用层）而非裸 TEXT。 |
| S2 🔴 | `pkg/server/handle_user_admin.go:104-122`；全 schema 无任何 FK（迁移零 REFERENCES/CASCADE） | `handleDeleteUser` 仅删 `app_user`+`user_identity`；`api_key`/`request`/`traces`/`user_setting`/`project` 引用被删 user_id 的行**永久孤儿**（查询过滤后不可见但膨胀 hypertable 并保留明文 key）。且无「不能删自己」「不能删最后管理员」保护——一次误调即锁死整个管理 API。 | 加 FK（CASCADE/RESTRICT）或同事务显式删 api_key/user_setting/project（request/traces 决策保留）；加自删/末管理员保护。 |
| S3 🟡 | `pkg/jsx/helpers.go:19,89-120`；`sdk.js:58-63` | `picotera.fetch` 是**无界 SSRF/OOM 向量且忽略 request context**：`fetchClient` 全局 `*http.Client` 硬编码 5s 超时；`http.NewRequest`（无 context）使 `s.ctx` 不传播，客户端断连仍阻塞至 5s；无 URL 主机白名单，可 fetch 云元数据 `169.254.169.254` 或内网服务；`io.ReadAll(resp.Body)` 无大小上限，多 GB 响应 OOM。`SetEvalTimeout` 无法中断阻塞宿主函数，5s/次是唯一后盾且可循环调用。 | 用 `http.NewRequestWithContext(s.ctx,...)`；`http.MaxBytesReader` 限体；超时可配或取自 HookTimeout；私网 SSRF 黑/白名单。 |
| S4 ⚪ | `db/queries/request.sql:203-229`；`pkg/server/request_update.go:20-25` | `UpdateRequest` 仅 `WHERE id=$1 AND created_at=$2`，无 `user_id` 谓词。XID 不可猜、网关自生，当前不可利用，但违背自身模式（其他查询均 gate user_id），若 id+created_at 永不经客户端输入则升级为真实漏洞。 | WHERE 加 `AND user_id = ...::bigint` 并传认证用户 id。 |

---

## 四、输入校验缺失（违反 fail-fast 原则）

| # | 位置 | 问题 | 方向 |
|---|------|------|------|
| V1 🟡 | `pkg/configx/configx.go:108` | `viper.Unmarshal(&config)` 的**返回错误被丢弃**。解码失败（duration 解析不出、类型不匹配）使 config 部分零值、服务器以默认静默启动——超时/端口/auth/S3 全可能错且无报错。仅有的后置校验是 `header_enabled && header_name==""`。 | 捕获并返回 Unmarshal 错误；加显式校验（至少一个 auth provider 启用、port 正、database_url 非空、S3 endpoint 设定时 access/secret 在场）。 |
| V2 🟡 | `pkg/server/handle_providers.go:54-99,101-180`；`pkg/contract/provider.go:132-169` | `handleCreateProvider/UpsertProvider` 仅校验 `ProxyUrl`。无 Name 非空、`ProviderModelEntry.Model` 非空、Endpoints 路径合法、`ModelsEndpointUrl` 是 URL 检查。空 model 名静默破坏路由（`elem->>'model' = $model_name` 匹配到缺失/null 字段）。`FromProviderView` 也不校验。 | 校验 Name 非空、每个 model 匹配 slug 且数组内唯一、`ModelsEndpointUrl` 合法 URL、端点路径数组无重复；违例 400。 |
| V3 🟡 | `pkg/server/endpoint_router.go:175-178`；`handle_endpoint.go:38-47` | 畸形 endpoint 路径（花括号不平衡、坏 token 名、重复变量）在 router load 时被**静默跳过**，upsert 时不校验——端点变成静默不可路由。 | upsert 时校验路径：`compilePattern` 失败即拒，立即暴露误配给运维。 |
| V4 ⚪ | `pkg/server/handle_exchange_rate.go:43-49,65-68`；`011_pricing_and_exchange_rate.sql` | `handlePutExchangeRate` 仅查 `code!=""` 与 `unitsPerUsd>0`，无长度/格式校验；任意字符串（超长、与 base 'USD' 大小写变体冲突）被接受，code 是主键污染表与 cagg `cost_currency` 分组。删除仅拦字面 'USD'。 | code 匹配 `^[A-Z]{3}$`，违例拒。 |
| V5 ⚪ | `pkg/jsx/session.go:604`；`gateway_flow_attempts.go:407-428` | `afterUpstreamError` 的 `statusCode` 用 `r.statusCode | 0` 截断为 int32 但**无范围检查**；巨大正整数（如 99999）直送 `WriteHeader`，写出畸形 HTTP 状态行。 | 截断/校验到合法 HTTP 范围 `[100,599]`。 |
| V6 ⚪ | `dashboard/src/ui/Input.vue:26-29` | `.number` 修饰符在 `Number(v)` 为 NaN 时**返回原始字符串**（如 `'12.'`、`'-'`、`'abc'`），而表单字段类型声明为 number，类型系统声称 number 而运行时持 string，后端收到未校验字符串。违背 fail-fast。 | `.number` 解析失败时 emit NaN/undefined 让表单 required 校验失败，或显式 `Number.isFinite` 校验后再提交。 |

---

## 五、错误处理与一致性

| # | 位置 | 问题 | 方向 |
|---|------|------|------|
| E1 🟡 | `pkg/jsx/session.go:273-275`（`isInterrupt`）、:254-257 | `isInterrupt` 用 `strings.Contains(err.Error(),"interrupted")` 判定超时——会误捕脚本 `throw new Error('request interrupted')`、含 "interrupted" 的网络错误、KV 错误。命中即置 `tainted=true` 返回 `ErrHookTimeout`，**永久禁用本请求剩余所有 hook**（经 failHook 显 503），掩盖真实原因。 | 暴露类型化中断 sentinel（直查 interruptData 标志），或精确匹配 QuickJS 中断异常串。 |
| E2 🟡 | `pkg/jsx/session.go:328-332`（`RunSortProviders`）、:580-583（`RunRewriteProviderModels`） | hook 返回数组 `json.Unmarshal` 失败时这两者 **Debug 日志后返回 (initial,nil) 静默吞掉**；而 `RunRewriteModel`/`RunBeforeRequest`/`RunBeforeTransform`/`RunAfterUpstreamError`/`RunRewriteRequest` 都返回解码错误（经 failHook 显 502）。畸形 sortProviders 输出被无声忽略，运维无信号。AGENTS.md 未记录此分歧。 | 统一 fail-fast：返回解码错误；若这两者有意静默保留则文档化原因。 |
| E3 🟡 | `pkg/llmbridgeimpl` 调用链；`cmd/picotera-llmbridge-plugin/main.go:90-95,110-115,126-131`；host `plugin_client.go:210-213,239-242,264-266` | 格式/profile 解析错误正确映射 `codes.InvalidArgument`，但 `parseSourceRequest`/`TransformResponse`/`AggregateStreamChunks` 的错误（畸形 JSON 体、空体、transformer 拒绝）一律 `codes.Internal`。host 返回通用 `llmbridge: ... plugin call: %w`，**无法区分 4xx 类客户端坏输入与 5xx 插件/服务器故障**，网关无法选择性重试 vs fail-fast。 | 分类 transformer 解析/拒绝错误为 `codes.InvalidArgument`，`codes.Internal` 留给意外插件故障。 |
| E4 ⚪ | `pkg/server/handle_kv.go:86-89` | Set/SetEx 成功后读 TTL 出错返回普通 `fmt.Errorf`，Huma 渲染默认 500（无 `code`/结构化 `details`），与同函数其余用 `huma.Error500InternalServerError` 不一致。 | 改 `huma.Error500InternalServerError("failed to get ttl after set", err)`。 |
| E5 ⚪ | `dashboard/src/router/index.ts:51-58` | admin guard `ensureQueryData` await **无 try/catch**；`/me` 401（会话过期）时 reject、guard 抛错、路由拒绝导航产生未处理错误，用户看到空白/坏跳转而非被送回 `/overview`。 | 包 try/catch，出错重定向 `/overview`；让后端 auth 边界全局处理 401。 |

---

## 六、测试与 CI 缺口

| # | 位置 | 问题 | 方向 |
|---|------|------|------|
| T1 🔴 | `.github/workflows/`（仅 `docker.yaml`） | **无测试/lint CI**——`docker.yaml` 仅构建镜像。Go 测试大量存在（`pkg/server/*_test.go` 等 35 个测试文件）却不在 CI 跑，回归直接进绿。AGENTS.md 确认「无 Go linter 配置」。 | 加 `ci.yaml`：`go test ./...` + `go vet`；dashboard `pnpm type-check && pnpm lint`。 |
| T2 🟡 | `pkg/llmbridgeimpl/bridge_stream.go`；测试仅 `bridge_test.go`(BridgeRequest)、`plugin_client_test.go:117-184`(identity+crash) | **跨格式流式转换零内容断言**。仅 `TestPluginBridgeIdentityStreamPassthrough`(字节相等) 与 `TestPluginBridgeStreamSelfHealsAfterCrash`(仅断言 `err==nil`，从不验证转换后内容)。`encodeSSEEvent` 单测孤立。流转换回归会绿发布。 | 加 in-process 测试：OpenAI→Anthropic、Anthropic→OpenAI、双 Gemini 变体，喂 canned 上游 SSE，断言源格式事件序列与重组内容。 |
| T3 ⚪ | `pkg/llmbridge/plugin_protocol.go`、`plugin_client.go`、`llmbridgeimpl/aggregate.go` | 协议辅助/转换边界缺测：header 往返、ABI 校验、pluginLogWriter 部分行缓冲、SignalPlugin 死/缺 pid、空体/畸形 JSON 的 BridgeRequest、`[DONE]`-only 的 AggregateStream、Error-frame→`pw.CloseWithError` 传播。 | 针对性单测，优先 malformed/空体与 Error-frame。 |
| T4 ⚪ | `TODO.md:36` | TODO.md 第 36 行被泄漏的 agent 输出污染（混入环境元数据与模型名），文件损坏。 | 清理该行。 |

---

## 七、性能

| # | 位置 | 问题 | 方向 |
|---|------|------|------|
| P1 🟡 | `pkg/server/handle_requests.go:170-172`(list)、:336(spans)；`pkg/artifacts/sink.go:152` | `attachArtifactUrls` 每行同步调用 2 次 `PresignedGet`（请求+响应 artifact），每页至多 100 行 → 200 次 SigV4 presign（HMAC-SHA256），串行化响应。 | errgroup 并发 presign，或 Sink 暴露批量 PresignedGet。 |
| P2 🟡 | `cmd/picotera-llmbridge-plugin/main.go:227-237`；`pkg/llmbridgeimpl/bridge_stream.go:134-153` | `streamWriter.Write` 每次 `make+copy` 再 `Send`，而输入 `p` 已是 `encodeSSEEvent` 每事件新分配且不被复用——纯开销。 | 直接发 `p`；若未来复用缓冲则文档化契约。 |
| P3 ⚪ | `pkg/llmbridge/plugin_client.go:335-345`(pump) | BridgeStream host pump 每 read `make+copy`（gRPC Send 异步缓冲、buf 复用所致），高吞吐流持续分配压力。 | `sync.Pool` 按容量池化，或批小读为大 Send 帧。 |
| P4 ⚪ | `pkg/llmbridge/plugin_protocol.go:100-111,118-138` | profile 配置：host `Marshal` 后 `validateJSONObject`(解码到 any)，plugin 再 `validateJSONObject`+`Decode`——同字节解析两次，每请求（含流起始帧）。 | `profileToProto` 去冗余校验；`profileFromProto` 单次 `Decode` 到 map 再类型检查。 |
| P5 ⚪ | `db/queries/project.sql:22-29`；`037_project_user_ownership.sql:23` | `MatchProjectByPaths` 每网关请求展开该用户所有 project 的所有 path 逐串比较（O(projects×paths)），`project.paths` 无 GIN 索引，热路径。 | GIN 索引(jsonb_path_ops) + `WHERE p.user_id=@user_id AND p.paths ?| @candidate_paths::text[]`，保留 length 排序。 |
| P6 ⚪ | `pkg/artifacts/payload.go:112-130` | `marshalAndCompress` 每次分配 zstd writer，无 `sync.Pool`，高吞吐 CPU 开销。 | `sync.Pool` 池化 zstd writer。 |

---

## 八、JSX 引擎其他

| # | 位置 | 问题 | 方向 |
|---|------|------|------|
| J1 🟡 | `pkg/jsx/session.go:215,234`（`PatchContext`/`SetClientBody` `EvalFile`）；`gateway_flow.go:359-373` | 这两处直接 `EvalFile` 继承上次 `m.timeout`（首个 hook 前为 0，无中断）。表达式极小故潜伏，但脚本可经 `Object.defineProperty` 在 globalThis.ctx 装 getter 陷阱，把 `Object.assign` 变成无超时界的任意用户代码执行。 | 每次 EvalFile 前 `SetEvalTimeout(s.timeout())`，或封装 set+eval helper。 |
| J2 🟡 | `pkg/llmbridge/plugin_client.go:408-425`(`pluginLogWriter`) | `w.buf` 按行缓冲无上限；插件发出超长无 `\n` 串（buggy 死循环、病态 panic 串）可无界增长 OOM 网关。panic 栈按行终止故潜伏。 | 截断部分行缓冲（超 N KiB flush/drop + 截断标记）。 |
| J3 ⚪ | `pkg/jsx/objects.go:391-403,99-109,83-94` | `arrSplice` 对每个被移除元素 `describe`→`register`，写入 `entries/nodeIDs/tree.ids`，splice 重写 `Elems` 后这些子树不再在树中却留存至 slot reset（请求结束）。大量 splice 大体时孤儿子树（可能含多 MiB data-url）累积；孤儿 Proxy 写会置 `tree.dirty=true` 击穿字节相同 clean-passthrough 优化。 | describe 移除元素时不注册，或注册后立即反注册。 |
| J4 ⚪ | `pkg/datamask/*`（整包）；`pkg/jsx/large_body_test.go:20-66` | `pkg/datamask` **确认死代码**：grep 全 Go 零导入，AGENTS.md:101 自认 unwired。其设计目标（防止多 MiB data-url 进 QuickJS 的 UTF-16→UTF-8 OOM）未实现，`large_body_test.go` 文档化了该 OOM 仍存在。死包 + 311 行测试仍随二进制维护。 | 清理删除（clean cutover），或按原设计接线（在 `SetClientBody`/`RunRewriteRequest` 的 body bytes 处应用）。鉴于项目禁兼容层，删除是更低风险默认。 |
| J5 ⚪ | `pkg/jsx/validate.go:19-27`；`session.go:159-169` | `ValidateSyntax` 的 VM 不设 `SetMemoryLimit`（与会话 VM 不一致），病态大源码编译期 OOM；每请求 `EvalFile` 重编译全部脚本，QuickJS 字节码不跨 VM 缓存。 | 校验 VM 设内存上限；按 script-id 缓存编译字节码（更新失效）。 |
| J6 ⚪ | `AGENTS.md:90` | AGENTS.md 说「五个 waterfalls」并列了六个，实际代码（`sdk.js:25-33`、`iface.go:33-45`）有**七个**——漏 `beforeTransform`（`gateway_flow_attempts.go:514` 调用，驱动 llmbridge outbound-profile 选择）。 | 更新 AGENTS.md 列全七个并文档化 beforeTransform。 |

---

## 九、KV / 杂项

| # | 位置 | 问题 | 方向 |
|---|------|------|------|
| K1 🟡 | `pkg/kv/memory.go:65-90` vs `pkg/kv/redis.go:13-68` | `Store` 接口承诺 glob ScanEntries，但 MemoryStore 用 `filepath.Match`，RedisStore 用 Redis SCAN（`*`/`?`/`[abc]`），**语义不同**（转义、`*` 跨 `/`、字符类差异）；`filepath.Match` 畸形 pattern 出错被静默吞（memory.go:70-71）。dev 用 memory、prod 用 redis 会同 pattern 不同结果。 | 两后端用同一 glob matcher/库，接口文档化支持的 pattern 语法。 |
| K2 ⚪ | `pkg/server/proxy_transport.go` | `proxyTransportCache` 按 proxyURL 缓存 `*http.Transport`，无淘汰——每个不同 proxyURL 一份永不释放。受 admin 配置数约束故影响小；但 `url.Parse` 极宽容，畸形 URL 静默回退环境代理（违背 fail-fast）。 | 配置时严格校验 proxy URL，畸形即拒。 |

---

## 十、仪表盘（前端）

| # | 位置 | 问题 | 方向 |
|---|------|------|------|
| F1 🔴 | `dashboard/src/views/TestView.vue:391,442,476-488,503-505` | TestView 流式 fetch + `AbortController` **卸载从不清理**（grep 无 `onBeforeUnmount`/`onUnmounted`）。导航离开时 ReadableStream reader 循环继续跑、消费上游响应、改未挂载组件状态、网络连接保持至流完成。controller 仅 stop() 点击才 abort。 | `onBeforeUnmount(() => controller?.abort())`。 |
| F2 🟡 | `dashboard/src/ui/ConfirmDialog.vue:21-39`；`Overlay.vue:10-20`；`composables/useConfirm.ts` | 模态确认/overlay 无 `role`/`aria-modal`、无 Escape、无 focus trap、无 focus 管理。所有 9 个 `confirm.require` 调用都是删除——破坏性确认变鼠标专用。 | `role='dialog' aria-modal`、Escape reject、打开移焦、关闭复焦、焦点陷阱。 |
| F3 🟡 | `dashboard/src/ui/AutoDataTable.vue:78-110`；`Tr.vue:17-29` | 所有基于 AutoDataTable 的列表页（Requests/Traces/ApiKeys/Users/Projects/Models...）行仅鼠标点击激活；overlay `<a tabindex='-1'>` 移出 tab 序，`<tr>` 无 keydown。键盘用户无法开行。 | 行 `tabindex='0'` + `@keydown.enter/space` 转发 handleRowClick。 |
| F4 🟡 | `dashboard/src/components/SSEEventsVirtualList.vue:19-27,48-68` | 三个独立触发都调 `measureVisibleRows()`：onMounted、`watch(virtualRows,{flush:'post'})`、`onUpdated`（每次组件更新都触发，非仅虚拟行变）；外加 `watch(events)` 调 `measure()+scrollToOffset(0)`。`measureElement` 改 virtualizer 状态→改 `getVirtualItems()`→重触 post-flush watcher——**递归测量循环**，多 SSE 事件 artifact 显著浪费布局。 | 去 `onUpdated`，仅靠 post-flush watch；事件增长时勿每次 `scrollToOffset(0)`。 |
| F5 🟡 | `dashboard/src/views/RequestsView.vue:143-164,603-607` | `resetCursorAndReload` 置 `isRefreshing=true` 后 refetch，仅 `watch(requests)` 重置它。若刷新返回结构相同数据，vue-query structural sharing 保同一 `items` 引用，`requests` computed 不重算，watcher 不触发，**isRefreshing 永真**，新行动画计算被跳过/卡死。 | 在 query `onSettled` 或 `finally` 重置，或改用 `isFetching` 标志。 |
| F6 ⚪ | `dashboard/src/composables/useArtifact.ts:6-13` | `fetchArtifact` 忽略 vue-query 传入的 `AbortSignal`；查询键变/导航离开时在途请求不取消，响应仍 fetch+JSON 解析后丢弃，大体 artifact 浪费带宽/CPU。 | `queryFn: ({signal}) => fetchArtifact(url, signal)`。 |
| F7 ⚪ | `dashboard/src/App.vue:13-14` | `App.vue` 调 `useExchangeRates()` 丢弃结果，下一行 `provideCurrencyContext()` 内部又调一次——两个 observer + 两条响应式订阅在 app 根，浪费且误导。 | 删 `App.vue:13`。 |
| F8 ⚪ | `RequestsView.vue:158-160`、`ApiKeysView.vue:66-68`、`SettingsView.vue:85-87`、`RawArtifactView.vue:70-72` | UI 反馈 `setTimeout` 卸载不清（对比 `PreferencesMenu`/`SelectMenu`/`ConversationView` 有清理）。短时长故实战无害，但与代码库其余清理纪律不一致。 | 存 timer id，`onBeforeUnmount` 清；或共享 `useTimeout` helper。 |
| F9 ⚪ | `dashboard/src/components/SidePanelHost.vue:31-61` | modal 模式仅 backdrop click + panel close，无 Escape、无 focus trap、无 focus 还原（F2 同伴）。 | modal 开时注册 Escape、`role='dialog'`、focus trap。 |

---

## 十一、第二轮新增发现（2026-07-07）

| # | 位置 | 问题 | 方向 |
|---|------|------|------|
| N1 🔴 | `pkg/server/gateway_flow.go:116-124,136-145`；`server.go:137` | 网关主流程在认证前执行 `readBody()`，`io.ReadAll` 读取完整请求体且没有大小上限。缺失/无效 API key 的请求同样可以先消耗内存；`decompressRequest` 又在全局 router 上先包装 gzip/br/zstd，所以上限缺失也覆盖解压后的 body。大体请求或压缩膨胀体能在认证失败前打爆进程内存。 | 调整顺序为 endpoint/OTR/meta → API key 认证 → 限流读取 body → project/preview/artifact backfill；新增 `PICOTERA_GATEWAY_MAX_REQUEST_BODY_BYTES`，超过上限返回 413 并收尾 meta。 |
| N2 🟡 | `pkg/server/live_requests.go:80,110-118,205-213` | live progress 为每个 in-flight 响应用 `bytes.Buffer` 保存完整 body；长流式响应或大响应会在内存中无限增长，且 `GET /live` 每次把完整 buffer 转 string 返回。artifact 已负责持久化，live view 不应成为第二份无上限 body 存储。 | 增加 live body byte cap，超过后停止追加 body 并暴露 `bodyTruncated`。默认保留可诊断的前缀，完整响应仍看 artifact。 |
| N3 🟡 | `pkg/server/handle_provider_endpoint.go:41-51`；`db/migrations/001_initial.sql:21-26`；`db/queries/routing.sql:1-21` | `provider_endpoint` 没有外键，upsert 时也不验证 provider 与 endpoint 存在；`GetEndpointByPath` 只有在成功且 endpoint 是 `modelList` 时才拒绝，not found/DB error 会被忽略并继续写入。删除 provider/endpoint 后会留下孤儿绑定；`ListAvailableModelNames` 不 join `endpoint`，会把 stale 绑定计入“可用模型”。 | upsert 前严格校验 provider/endpoint 存在；迁移清理孤儿绑定并添加 FK `ON DELETE CASCADE`；`ListAvailableModelNames` join `endpoint`。 |
| N4 🟡 | `dashboard/src/api/queryKeys.ts:35-130`；`dashboard/src/views/UsersView.vue:68-73`；`dashboard/src/components/AppSidebar.vue:20-23` | impersonation 切换只 `invalidateQueries()`，所有 user-scoped query key 不包含身份维度。切换身份后，Overview/Requests/API keys/Projects 等页面会先渲染上一身份缓存，再等待 refetch；refetch 失败时 stale 数据继续留在缓存。 | 身份切换时先清空 query cache，再改变/还原 impersonation 状态并导航。 |
| N5 ⚪ | `pkg/server/handle_models.go:10-14`；`db/queries/model.sql:10-11`；`db/queries/provider.sql:26-27`；`db/queries/endpoint.sql:10-11`；`db/queries/script.sql:21-22` | 管理 CRUD 的 not-found 语义不一致：`GET /models/{name}` 对 `pgx.ErrNoRows` 返回 500；多处 delete query 是 `:exec`，删除不存在资源静默成功。API key/project/user-setting 等路径已经返回 404 或 execrows，说明这是遗漏。 | 将 get model no rows 映射 404；管理 delete query 改 `:execrows` 并在 0 rows 时返回 404。 |
| N6 ⚪ | `pkg/auth/auth.go:87-130`；`pkg/auth/`（无 `*_test.go`） | 认证、disabled user、single-user-mode、http-header auto-create、impersonation 权限与错误码都是安全边界，但 `pkg/auth` 没有单测。现有 `go test ./...` 通过不能覆盖这些分支。 | 增加 auth resolver/middleware 单测，覆盖 disabled 拒绝、single-user root、header missing、auto-create race fallback、admin/non-admin impersonation、target not found。 |
| N7 🟡 | `pkg/server/gateway_helpers.go:588-599`（`buildUpstreamRequest` 剥离清单）、:621-652（`redactUpstreamCredentials`） | 客户端 `Cookie` 请求头**既不从上游请求剥离、也不在请求 artifact 中脱敏**。剥离清单含 authorization/x-api-key/x-goog-api-key/host/content-length/x-picotera*/本地 auth 头，脱敏覆盖 Authorization/X-Api-Key/X-Goog-Api-Key/Cf-Access-*/`?key=`——两处均无 `Cookie`。最近的 redact-cookies（e2722ed）只处理**响应** `Set-Cookie`。结果客户端会话 Cookie 原样转发上游并明文入 artifact，与该脱敏系列意图不一致。 | 请求侧对 `Cookie` 脱敏（artifact），并按需从上游请求剥离；确认是否有意透传。 |
| N8 🟡 | `pkg/server/cors.go:15-20` | 预检把 `Access-Control-Request-Headers` 原样回填 `Allow-Headers`，且 `Expose-Headers: *` + `Allow-Origin: *`。网关/unified 用 API key 认证，`*` 源 + 任意 Allow-Headers 意味任何网站 JS 可用浏览器内脚本持有的 key 直连网关；`Expose-Headers: *` 还把上游透传头（如 `X-Api-Key`）暴露给跨源脚本读取。 | `Expose-Headers` 收敛为白名单（如仅 `X-PicoTera-Request-Id`）；复核 `*` 源是否符合威胁模型。 |
| N9 🟡 | `pkg/server/handle_exchange_rate.go:43-63`；`db/queries/exchange_rate.sql:7-13` | `handleDeleteExchangeRate` 有 `Code=="USD"` 保护，但 `handlePutExchangeRate` **无对应保护**，`UpsertExchangeRate` 的 `ON CONFLICT (code) DO UPDATE` 可把 USD 本币 `units_per_usd` 改成任意值（如 0.5），破坏所有成本换算基准。 | put 时若 `Code=="USD"` 拒绝，或强制 `units_per_usd=1`。 |
| N10 🟡 | `pkg/jsx/sdk.js:306-315`（`consoleEmit`） | `console.log(ctx.request.body)` 时，body Proxy 的标量 get 返回内联值，`JSON.stringify` 递归拉取整棵树、把多 MiB data-url 全部物化进 QuickJS 并序列化——正是 body-proxy 架构极力避免的内存尖峰，可击穿 JS 内存限。host 端 `maxLogMessageLen` 截断发生在 JS 完成整串序列化**之后**，OOM 已可能触发。 | console 序列化前对 body Proxy 特判/限深；至少文档化「勿 log 整个 body」。 |
| N11 🟡 | `pkg/jsx/objects.go:441-476,192-204`（`set`→`resolveMarkers`） | `body.x = {...}` 的 set 路径对新值树递归 `resolveMarkers(clone=true)`，每个 marker `jsonast.Clone` 在 **Go 堆**发生、不计入 `SetMemoryLimit`——链式 `body.x = {a: body.x_prev}` 可指数放大树体积绕过 JS 内存限，且无节点计数上限。 | resolveMarkers 设节点总数/深度上限；将 Go 侧 body 树大小纳入独立配额。 |
| N12 ⚪ | `db/queries/project.sql:59-61`（`MergeProjectReassignRequests`）、`MergeProjectUpdateTarget`、`UpsertProjectSeen` | `UPDATE request SET project_id=@target WHERE project_id=@source` 无 `user_id` 谓词。当前调用方事务内已用 `GetProject(user_id)` 校验归属、project_id 为用户私有，实际不跨用户，但违背「读/写路径注入 mandatory user_id」的纵深防御原则。 | merge 的 request 重写与 project 更新加 `AND user_id = @user_id`。 |
| N13 ⚪ | `dashboard/src/views/RequestsView.vue:562-569` vs `dashboard/src/components/RequestDetailsContent.vue:166-173` | 两处各自定义 `requestState`：列表版「ok」要求 2xx **且** `finishReason ∈ {2,3,5}`，详情版只要 2xx。同一请求可能列表标红、详情标绿；`[2,3,5]`/阈值两处硬编码易漂移。 | 抽到共享 util 统一分类。 |
| N14 🟡 | `CLAUDE.md`「Project matching」段与 `CLAUDE.md:124`/`AGENTS.md:90` | **文档与实现严重漂移，影响代码库理解与前几轮判断**：(a) CLAUDE.md 称有 `Server.projectRouter` 内存最长前缀缓存、任何 project 表变更「必须调 `Invalidate()`」——**该缓存不存在**（全仓无 `projectRouter` 符号，`project_extractor.go` 每请求走 `MatchProjectByPaths` 活查询）；据此 P5 应重定性为「热路径同步 DB 查询」，且任何「project 写入点漏调 Invalidate」类发现均为伪阳性。(b) 称「三条固定正则」，实际 **6 条**（`project_extractor.go:44-51`）。(c) J6：hook 计数「five」+ 6 项列表两处均错，实为 **7 个**（漏 `beforeTransform`），CLAUDE.md:124「runs all five JS hooks」同病。 | 修正 CLAUDE.md：移除 projectRouter 描述、更正正则数量与匹配机制、hook 改「seven」并补 `beforeTransform`。 |

### 严重度更正（第二轮 Opus 复审，附证据）

- **R2 → 低**：`Bridge.Close` 全仓无生产调用者（仅 `plugin_client_test.go:210` 的 `t.Cleanup`），进程退出时子进程随之消亡。属 API 语义瑕疵而非实际泄漏路径；修复方向（加 `closed` 标志短路）不变。
- **R5 成因更正 → 低**：goroutine 实际用 `f.ctxs.Persist()`（`WithoutCancel`+30s），非「请求 ctx 派生 5s」。真正窗口是 `run()` 的 `defer f.ctxs.cancelBase()` 在极快请求下先于 upsert 完成而级联取消，命中概率低。
- **T2 缺口收窄**：`aggregate_test.go:40-169` 对四格式**聚合**已有充分内容/usage 断言，`encodeSSEEvent` 有单测。真正零断言的是**跨格式流式逐事件转换**（OpenAI SSE→Anthropic SSE 等）——补测应针对此。
- **F4 → 收敛非死循环**：行高稳定后 `measureElement` 命中缓存不再通知，循环终止；准确描述是 `onUpdated` 与 `watch(virtualRows)` 两个重叠触发源在测量未稳定期反复重测（抖动/多余渲染）。去 `onUpdated` 即可。
- **F5 影响收窄**：`isRefreshing` 不被模板消费，卡住的后果是新行动画检测失效，而非可见加载态卡死。
- **F7 影响收窄**：vue-query 同 key 共享缓存**不产生重复网络请求**，仅多一个无用 observer 订阅；删 `App.vue:13` 即可。

---

## 跨切面观察

1. **优雅关闭普遍缺失**（R1/R2/R8）——服务器无信号处理，pgxpool/插件/artifact worker 均不排空，退出即丢在途请求与 artifact。这是单条最高杠杆的可靠性改进。
2. **入站体量控制缺失**（N1/N2/R7）——认证前读取完整 body、live view 无上限缓存、HTTP server 无读超时，三者共同形成内存与慢连接 DoS 面。
3. **QuickJS 超时/中断面有系统性漏洞**（R3/J1/E1）——顶层 eval 无超时、宿主函数阻塞不可中断、`isInterrupt` 误判，三处叠加使有缺陷脚本能挂住或误禁用网关。
4. **fail-fast 校验原则多处未贯彻**（V1-V6/K2/N3/N5/N9）——config 解码错误被吞、provider/exchange/endpoint 路径/HTTP status/Input.number 校验缺失，provider-endpoint 可写孤儿绑定，管理 delete not-found 静默成功，exchange rate PUT 可篡改 USD 本币。
5. **凭据脱敏/CORS 面不完整**（S1/N7/N8/N12）——API key 明文存储、客户端 Cookie 透传上游且明文入 artifact、CORS `Expose-Headers: *` 泄漏透传头、merge/seen 查询缺纵深防御 user_id。
6. **无测试 CI + 关键路径零内容断言**（T1/T2/N6）——回归直达生产，认证边界也缺少单测。
7. **文档漂移**（N14/J4/J6/T4）——CLAUDE.md project-matching 段过时、`pkg/datamask` 整包 unwired、hook 计数错、TODO.md 污染。

## 优先级

按「风险×杠杆」排序，最值得先做的五项：

1. **优雅关闭 + llmbridge Close 终止性**（R1+R2+R8）——单批修复退出数据丢失与插件僵尸重启。
2. **网关入站 body 与 live 缓存上限**（N1+N2）——堵住认证前内存 DoS 与长流双份 body 缓存。
3. **已确认 Bug 修复**（C1 项目过滤空操作 + C2/C3 流式后失败行收尾）——直接修对的用户可见缺陷。
4. **QuickJS 超时/中断硬化**（R3+J1+E1）——堵住脚本 DoS 与误禁用。
5. **CI、流式转换测试与 auth 测试**（T1+T2+N6）——防回归基线。
6. **API key 明文存储**（S1）——安全债，影响所有 DB 读权限面。

> 本文档为研究产出。具体执行计划见 `plan.md`。
