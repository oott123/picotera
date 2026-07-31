# Design: HTTP/2 连接复用导致的 "timeout awaiting response headers" 成串失败

## 问题模型

`http2: timeout awaiting response headers` 是 x/net/http2 的**每 stream** 错误：`ResponseHeaderTimeout` 到期只中止那一个 stream（发 RST_STREAM），**连接本身留在连接池里**。x/net 对这个错误没有任何连接逐出逻辑。由于 HTTP/2 把同一主机的所有请求多路复用到同一条连接上，一旦这条连接进入「新 stream 得不到响应」的状态，后续对同一上游的每次重试都会继续骑在这条坏连接上、逐个撞 header 超时——这正是「一出就是连着出、同一个上游、多次重试都出」的形态。上游日志显示有回复也与此吻合：坏的是我们与上游（或中间层 CDN/代理）之间**这一条连接**的新 stream 通路，不是上游服务本身。

与「上一个请求被掐掉」的关联：客户端取消（下游断开、面板中断、`idleTimeoutReader` 触发）会在连接上发 RST_STREAM 并中止半途的 body 写入。部分上游/中间盒对这种半途 RST 处理不当（流控记账、代理缓冲等实现各异），会把连接带入上述半死状态。我们不试图定位上游侧的具体缺陷（种类太多且不可控），而是让 PicoTera 在客户端侧做正确的事：**检测到坏连接就逐出并短期隔离，不再复用**。

已有的 h2 keepalive PING（`GatewayHTTP2ReadIdleTimeout=13s` + `PingTimeout=6s`，为 CONNECT 隧道假死加的）救不了这个场景：`ReadIdleTimeout` 只在连接上**完全收不到任何帧**时才发健康检查 PING。一条还在给旧 stream 送数据、或能正常回 PING、但新 stream 卡死的连接，永远不会被 PING 机制逐出。

## 已验证的库行为（x/net v0.56.0 + Go 1.26）

设计依赖以下事实，均已在源码中核对：

1. `req.Close = true` → h2 在开 stream 时把所载连接标记 `cc.doNotReuse = true`（`transport.go:1121-1123`，`isConnectionCloseRequest`）：该连接不再接受新 stream，空闲即关。对 HTTP/1.1 则发送 `Connection: close`。这是「让某条连接退役」的唯一公开 API 路径。
2. `http.Transport.DisableKeepAlives = true` → h2 新建连接直接 singleUse（`transport.go:462`，`newClientConn(c, t.disableKeepAlives(), nil)`），一请求一连接；HTTP/1.1 同理不复用。全局关复用开关用它实现即可，无需碰 `TLSNextProto`。
3. `http.Transport.CloseIdleConnections()` 会通过 altProto 反射（std `transport.go` issue 22891 处理）传播到 x/net 的 `http2.Transport`——但 `Transport.Clone()` **不复制**未导出的 altProto，所以代理变体（`proxyTransportCache` 里 Clone 出来的）调 `CloseIdleConnections()` 到不了 h2 池。且因 Clone 共享 `TLSNextProto`，代理变体的 h2 连接实际都落在对应 base 的 h2 池里。因此必须保留 `newGatewayTransport` 里 `http2.ConfigureTransports` 返回的两个 `*http2.Transport` 句柄（stream / nonStream），逐出时显式调用它们的 `CloseIdleConnections()`。
4. 错误识别：h2 为 `"http2: timeout awaiting response headers"`，HTTP/1.1 为 `"net/http: timeout awaiting response headers"`，两者都是未导出类型，无 sentinel 可 `errors.Is`；`forwardRequest` 直接调 `RoundTrip`，错误未被 `url.Error` 包裹。识别用子串匹配 `"timeout awaiting response headers"`（同时覆盖两种协议），不匹配拨号超时、TLS 超时、context 取消等其他 timeout。

## 方案：三层，全部落在 `forwardRequest` 一个收口点

`forwardRequest`（`gateway_helpers.go:719`）是所有上游出站请求的唯一出口（gateway 路径路由、unified 路由、test/direct、fetch-models 共用），三层改动都在这里及其配套结构上，调用方签名不变。

### 1. 验证层：连接级 instrumentation

给每个出站请求装 `httptrace.ClientTrace{GotConn}`，捕获 `Reused` / `WasIdle` / `IdleTime` / `LocalAddr→RemoteAddr`（本地端口+远端地址即连接身份）：

- 每次拿到连接：Debug 级日志一条（字段：`conn_reused`、`conn_was_idle`、`conn_idle_time`、`conn_local`、`conn_remote`）。请求上下文自带 request id，可与 request 表关联。
- `RoundTrip` 返回错误时：Warn 级日志，带同一组连接字段 + 错误本身。

验证方法（部署后等下一次爆发）：

- 失败的 attempt 日志里 `conn_reused=true` 且连串失败共享同一 `conn_local→conn_remote` → 证实「同一条复用连接被反复选中」。
- 用该连接身份反查此前使用它的请求，与 `finish_reason = cancelled` 的行对照 → 证实/证伪「被掐掉的请求污染连接」。
- 存量数据可先行旁证（无需代码）：`request` 表里 header 超时已被 `classifyForwardError` 归为 `FinishReasonHeadersTimeout`，按 provider + 时间聚一下即可看到爆发簇，以及簇前是否紧邻 cancelled 行。
- 需要帧级细节时运维侧临时加 `GODEBUG=http2debug=2`，不进代码。

### 2. 修复层：坏连接逐出 + 主机级短期隔离（自动，无配置）

新增 `connQuarantine`（挂在 `Server` 上，独立小文件）：互斥锁 + `map[quarantineKey]time.Time`（到期时刻），key 为 `(proxyURL, streaming, host)`，TTL 常量 60s，访问时惰性清理过期项。时间源用注入的 `now func() time.Time` 以便单测。

`forwardRequest` 中：

- **发送前**：若 key 在隔离期内 → `req.Close = true`。效果：隔离窗口内每条连接最多再承载一个请求就退役（h2 `doNotReuse`、h1 `Connection: close`），坏连接在一次 attempt 内排空，后续请求走新拨的连接。窗口结束自动恢复正常复用。
- **失败后**：若错误匹配 `isAwaitHeadersTimeout` → 标记该 key 隔离，并立即调该变体 `*http.Transport` 的 `CloseIdleConnections()` + 对应 streaming 侧保留的 `*http2.Transport` 句柄的 `CloseIdleConnections()`（坏连接此刻通常已无活跃 stream，直接被回收；若仍有别的 stream 在跑，则由隔离期的 `req.Close` 兜底退役）。标记时 Warn 一条（`host`、`proxy`、`streaming`、TTL），隔离期内命中时 Debug 一条。

收敛性质：爆发最多再多付一次失败 attempt（第一个带 `req.Close` 的重试可能仍被池分到坏连接，但它同时把坏连接标成 `doNotReuse`），之后即恢复。隔离粒度是主机级，不影响其他上游；无新增配置项。

配套结构改动：

- `newGatewayTransport` 返回 `(*http.Transport, *http2.Transport)`。
- `proxyTransportCache` 保存 `streamH2` / `nonStreamH2` 两个句柄，暴露 `closeIdle(streaming bool)`（同时关 t1 变体与 h2 句柄的空闲连接）——由 `forwardRequest` 在标记隔离时调用。

### 3. 兜底层：全局关闭连接复用开关

新增配置 `PICOTERA_GATEWAY_DISABLE_KEEP_ALIVES`（`gateway_disable_keep_alives`，bool，默认 `false`）：为 `true` 时 `newGatewayTransport` 设 `DisableKeepAlives: true`，两个 base 及其全部代理变体（Clone 继承该字段）生效——h2 一请求一连接，h1 不复用。代价是每请求一次 TCP+TLS 握手，仅作为紧急止血开关，不作为常态。

## 不做的事

- 不改 keepalive PING 参数、不换 HTTP 版本策略（不加「禁用 HTTP/2」开关）——用户要的开关语义是关复用，`DisableKeepAlives` 已完整覆盖。
- 不自建 `http2.ClientConnPool`——`req.Close`/`CloseIdleConnections` 组合已足够，且复杂度不成比例。
- 不把连接信息持久化进 request 行——日志（带 request id）足以做关联分析。

## 测试

纯单测（延续 `pkg/server` 无 DB harness 的风格）：

- `isAwaitHeadersTimeout`：h2 / h1 两种错误串（含 `fmt.Errorf` 包裹）→ true；拨号超时、`context.DeadlineExceeded`、普通错误 → false。
- `connQuarantine`：标记后同 key 生效、异 key（不同 host / proxy / streaming）不生效、注入时钟推过 TTL 后失效、过期项被清理。
- `forwardRequest` 行为测试（httptest HTTP/1.1 服务端即可验证机制)：隔离期内出站请求携带 `Connection: close`（服务端断言收到）；同一 key 非隔离期不携带。GotConn 捕获字段非空。
