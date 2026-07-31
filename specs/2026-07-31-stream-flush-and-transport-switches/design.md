# Design: 流式响应头未及时 Flush 的修复 + 禁用 HTTP/2 / 每请求新建 transport 开关

## 问题模型

下文 A = 下游 picotera（报错方），B = 上游 picotera（transform 路径）。三个已核实的事实：

1. **A 侧连接复用已被排除。** `PICOTERA_GATEWAY_DISABLE_KEEP_ALIVES=true` 下 h2 连接是 singleUse（一请求一连接），失败的 attempt 走的是全新连接，上一条 spec 的"坏连接被复用"模型解释不了现在的现象。
2. **B 已经把响应写出去了。** 用户核实：超时对应的请求在 B 侧 upstream 行和 meta 行都记录了 body chunk。meta 行的 chunk 由 B 的客户端写循环记录，每个 chunk 都随即 Flush——meta 行有记录即字节（连同响应头）已离开 B 的进程。
3. **A 到 h2 终结点的连接在整个 91s 里是活的。** A 配置了 h2 keepalive PING（`ReadIdleTimeout=13s` + `PingTimeout=6s`）：一条 91s 收不到任何帧的连接会在 ~19s 被 PING 判死并报 connection lost，而不是等到 91s 报 header timeout。既然报的是 header timeout，这条连接在 91s 内持续有 PONG（或其他帧）回来。

另有三个已确认的约束：**B 的首个 meta chunk 落在 A 的 91s 窗口内**（时间戳判据已跑；A 的 attempt 与 B 的行经 `docs/example-scripts/add-upstream-request-id.js` 的逐 attempt 请求 id 关联，对应关系可靠）；并发只有约 4；B 前面的 TLS/h2 终结是用户长期使用的成熟网关（picotera 自身只 serve 明文 HTTP/1.1，A 报 `http2:` 错误意味着这一层必然存在，A 侧还可能有 `proxy_url` 的 CONNECT 前置代理）。

事实拼起来：B 及时应答了、A 的连接活着、唯独该 stream 的 HEADERS 没到 A、负载小到不足以压垮任何成熟组件。嫌疑按优先级：

1. **A 侧 h2 客户端层的共享与竞态（用户当前怀疑的方向，有具体机制支撑）。** x/net 的 h2 连接池按 host:port 作 key，且 base transport 与全部代理变体共享同一个池（Clone 共享 TLSNextProto，前一 spec 已注明）——同一上游 host 的所有请求，不论 provider / proxy 变体，都汇入同一套 `addConnIfNeeded` / 池机制。即使 singleUse（DISABLE_KEEP_ALIVES），并发同拨同一 host 时 x/net 的去重路径会丢弃后来者刚握手好的连接（dup `addConnCall` → `go c.Close()`），把请求路由到池内 singleUse 连接上竞争唯一 slot，竞争失败走 `ErrNoCachedConn` → std 层重试重拨。4 路并发持续打同一 host 恰好长期处于这个路径上。快速检索未找到能精确对上"HEADERS 已到达但 RoundTrip 超时"的已知 issue，但 DisableKeepAlives 在 h2 上的语义历史上确实浑浊（golang/go#33260、#22441；#61863 是 singleUse 连接生而不可用的先例）。该假设无法从源码推演直接证实，用下述判定手段钉死。
2. **h2 终结跳按 stream 丢/憋响应。** 与"成熟网关、低负载、别处从未见过"相抵，降级为次要嫌疑。
3. **B→终结层的投递失败。** B 的 meta 记录只证明 userspace write 成功（落入内核发送缓冲），不证明对端收到；若终结层根本没收到 B 的响应，它对 A 的表现与 2 相同。低概率（同一条刚投递完请求的 TCP 流单向黑洞），留作 1、2 排除后的检查项。

**判定手段（决定性）：A 侧临时加 `GODEBUG=http2debug=2`。** x/net 会在读循环里逐帧打日志——失败样本若日志里**出现了** HEADERS 帧但 RoundTrip 仍报 header timeout → 铁证是 A 侧 x/net 客户端内部问题（假设 1），两个实验开关任一都能根治，并可向上游报 bug；若 91s 内**没有** HEADERS 帧 → 问题在 A 之外（假设 2/3），转向终结层日志与 B 侧 `ss -ti`/抓包确认 B 的字节是否被 ACK。4 路并发下该日志量可承受，仅短期开启。

两个实验开关对假设 1 都是整类消除：`gateway_disable_http2` 让请求完全绕开 x/net h2 层（h1 + DISABLE_KEEP_ALIVES = 每请求一条独占新连接，无任何池语义）；`gateway_ephemeral_transport` 每 attempt 一个全新 transport（含全新 h2 池），即用户说的"每次都开个新的"，共享面为零。

无论哪种，代码侧都有一个确定存在的缺陷要修——**两条流式成功路径在 `WriteHeader` 之后直到第一个 body chunk 才第一次 `Flush`**：

- 路径网关：`gateway_flow_success.go` `openPathInternalReader` 里 `w.WriteHeader(http.StatusOK)`，首次 Flush 在 `pipePathResponse` 的读循环里、第一个 chunk 写出之后。
- unified/transform 路径：`gateway_unified_helpers.go` `unifiedStreamSuccess` 里 `w.WriteHeader(http.StatusOK)`（约 438 行），首次 Flush 同样在读循环第一个 chunk 之后；桥接时第一个 chunk 还要等 llmbridge 从上游首个原生事件转换出首个源格式事件（host 侧 BridgeStream 是 pipe+双泵增量转发，不整流缓冲）。

Go 的 http server 把响应头滞留在输出缓冲里直到首次 Flush，所以 A 对 B 的 header 计时目前覆盖到"B 转发出首个事件"而非"B 拿到其上游响应头"。对首 token 以分钟计的 thinking 模型，这是一整类真实的暴露窗口；已观测个案不能证明所有超时个案的首 chunk 都及时写出了。

用户问的两点，答案一半一半：

- **flush：确实没有（headers 随首 chunk 才出去）。** 要修，但已观测个案表明它不是唯一根因。
- **chunked：没有问题。** 两条成功路径都剥掉了 Content-Length（`copyPathSuccessHeaders` / unified 的 header 复制循环），Go 对 h1 自动 chunked、h2 用 DATA 帧，无需改动。

## 方案

四部分：一个无条件 bug 修复（flush）、两个实验开关（关 h2、每请求新建 transport，均为用户显式要求）、少量 instrumentation 补充。全部落在 `pkg/server/` 与 `pkg/configx/`，无 contract / openapi / dashboard 改动。基线是工作区中已实现的 2026-07-30 spec（connQuarantine + httptrace 日志 + DISABLE_KEEP_ALIVES）。

### 1. 修复：WriteHeader 后立即 Flush（无条件）

新增小 helper `commitResponseHeaders(w http.ResponseWriter, status int)`：`WriteHeader(status)` 后立即 `Flush`（`w` 实现 `http.Flusher` 时）。替换三个调用点的裸 `WriteHeader`：

- `openPathInternalReader`（路径网关流式成功）。
- `unifiedStreamSuccess`（unified 流式/非流式成功；非流式路径提前 flush 头无语义变化——状态码在 WriteHeader 时就已承诺）。
- `handleTestDirect`（透传测试路由，顺带对齐）。

三处的 Flush 都发生在 `StartClientWrite` / 并发写入者启动之前，无竞争。效果：A 在 B 拿到其上游响应头、决定成功提交的那一刻就收到响应头，header 计时器停止；后续等待归 A 的 `idleTimeoutReader`（读空闲超时）与 `gateway_read_timeout` 管辖——这才是各计时器的本意分工。

配套：SSE 响应（Content-Type 含 `text/event-stream`）在 WriteHeader 前设置 `X-Accel-Buffering: no`。A→B 走 h2 意味着 B 前面必然有 TLS 终结的反向代理（picotera 自身只 serve 明文 h1），nginx 类代理默认缓冲响应体，会在 B 之外再造一层同样的"头/首块滞留"；该头是标准的逐跳去缓冲指令，对不认识它的代理无害。

### 2. 开关：禁用 HTTP/2（`PICOTERA_GATEWAY_DISABLE_HTTP2`）

`gateway_disable_http2`（bool，默认 `false`）。为 `true` 时 `newGatewayTransport`：

- 不调用 `http2.ConfigureTransports`，返回 nil h2 句柄（`proxyTransportCache.closeIdle` 已处理 nil）。
- 设 `ForceAttemptHTTP2: false`、`TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}`（非 nil 空 map 是 std 文档定义的"关闭自动 HTTP/2"方式；`Transport.Clone` 会复制非 nil 的 TLSNextProto，所以代理变体同样关闭）。

所有上游出站请求（gateway、unified、test/direct、fetch-models）从此走 HTTP/1.1。h2 keepalive PING 配置随之不生效（本就挂在 h2 句柄上）。

配套修正：`newGatewayTransport` 无条件补 `MaxIdleConnsPerHost: 100`（与 `MaxIdleConns` 对齐）。std 默认每主机只留 2 条空闲连接——关掉 h2 后单一上游主机的并发流量会因此疯狂建连/断连，这个默认值必须一起修，否则 h1 模式在并发下退化。

### 3. 开关：每请求新建 transport（`PICOTERA_GATEWAY_EPHEMERAL_TRANSPORT`）

`gateway_ephemeral_transport`（bool，默认 `false`）。为 `true` 时 `forwardRequest` 不再从 `proxyTransportCache` 取共享 transport，而是**每个 attempt 现建一个**：

- 复用 `newGatewayTransport`（含 disable_http2 / 超时等全部配置），再按 proxyURL 应用代理语义（`""` → ProxyFromEnvironment；`"direct"` → nil；URL → `http.ProxyURL`；解析失败 → 回落 ProxyFromEnvironment，与 cache 现行为一致）。代理应用逻辑从 `proxyTransportCache.get` 中提取成共享函数，两处调用。
- 强制该 transport `DisableKeepAlives = true`：临时 transport 上的连接本就不可能被第二个请求复用，关掉 keep-alive 让连接随请求自然死亡（h1 发 `Connection: close`，h2 singleUse），杜绝连接滞留到 `IdleConnTimeout` 的泄漏窗口。
- 兜底回收：RoundTrip 出错时立即对该 transport（t1 + h2 句柄）调 `CloseIdleConnections`；成功时把 `resp.Body` 包一层，`Close` 时做同样的回收（`sync.Once` 防重入）。
- connQuarantine 的 mark / req.Close / cache closeIdle 逻辑保持原样——ephemeral 模式下它们是无害冗余（cache 池为空时 closeIdle 是 no-op），不为此加分支。

代价明确：每请求一次 TCP+TLS 握手 + 一个 transport 分配，仅作实验/止血用。

### 4. Instrumentation 补充

现有 httptrace（GotConn 日志）再加两个时间戳：`WroteRequest`（请求写完）与 `GotFirstResponseByte`。失败时的 Warn 日志带上 `wrote_request_ago` / 是否收到过首字节——用于区分"请求写完后干等响应头"（本次的形态）与"请求体写不出去"（流控/连接问题的形态），给 runbook 提供判据。

## 验证与 runbook（代码之外）

- **第 1 步（判定性，不改代码）：A 侧临时加 `GODEBUG=http2debug=2`**，等下一次失败样本。HEADERS 帧在日志里出现过但仍超时 → 假设 1（x/net 客户端内部）铁证；91s 内无 HEADERS 帧 → 转向终结层日志（用请求 id 查它何时收到 B 的响应、有没有向 A 发 HEADERS）与 B 侧 `ss -ti`/抓包（B 的字节是否被 ACK），区分假设 2 与 3。
- **第 2 步：部署本 spec（flush 修复 + instrumentation），并开启 `PICOTERA_GATEWAY_DISABLE_HTTP2=true`**（保持 `PICOTERA_GATEWAY_DISABLE_KEEP_ALIVES=true`：h1 + 无 keep-alive = 每请求一条独占新连接，对假设 1 是整类消除，也是最干净的隔离实验）。`curl -sv -N` 验证响应头在成功提交时立刻到达。
- 若 h1 模式下彻底消失 → 假设 1 成立。稳定后可视性能需要关掉 DISABLE_KEEP_ALIVES 恢复 h1 连接池（MaxIdleConnsPerHost 已修），并观察是否复现（h1 池语义简单，预期不会）。
- 若 h1 模式下仍复现 → 问题不在 h2 层，回到第 1 步的终结层/B 侧排查；此时 `PICOTERA_GATEWAY_EPHEMERAL_TRANSPORT=true` 作为最后一个 A 侧变量（连 transport 结构本身都不共享）。
- A 侧失败日志新字段：`wrote_request_ago` 大且 `got_first_byte=false` → 请求写完后响应通路无回音，与 GODEBUG 帧日志互相印证。
- 堆叠网关的结构性事实：A 的 header 计时覆盖 B 的整个重试循环。上游是另一个 picotera 时应把 A 的 `PICOTERA_GATEWAY_RESPONSE_HEADER_TIMEOUT` 调大（例如 180s+），这是配置项已有的能力，不改代码。

## 不做的事

- 不改 chunked/Transfer-Encoding 处理——已确认正确。
- 不动 `pkg/jsx` 的 `fetchClient`（脚本用的 host fetch，5s 超时的独立 client，与网关出站无关）。
- 不移除 2026-07-30 的 connQuarantine / DISABLE_KEEP_ALIVES——与本次改动正交，且 ephemeral/h1 模式下自动退化为 no-op。
- 不给 flush 加开关——它是 bug 修复，不是行为选项。

## 测试

纯单测，延续 `pkg/server` 无 DB harness 惯例：

- `commitResponseHeaders`：fake ResponseWriter（实现 Flusher，记录调用序列）→ 断言 `WriteHeader` 后紧跟 `Flush`；不实现 Flusher 的 writer 不 panic。
- SSE 去缓冲头 helper：`text/event-stream`（含参数变体）→ 设 `X-Accel-Buffering: no`；`application/json` → 不设。
- `newGatewayTransport`：`disable_http2=true` → TLSNextProto 非 nil 且空、h2 句柄为 nil、ForceAttemptHTTP2 为 false；默认 → h2 句柄非 nil；两种模式 MaxIdleConnsPerHost 均为 100。
- 代理应用函数：三种 proxyURL 语义 + 非法 URL 回落。
- ephemeral `forwardRequest`（httptest h1 服务端记录每请求的 `RemoteAddr`）：开关开 → 连续两次请求源端口不同（不复用）；开关关（且 keep-alive 开）→ 源端口相同（复用）。resp.Body Close 后 transport 空闲连接被回收（服务端连接计数或 `httptest.Server` 关闭无泄漏告警即可作断言面）。
