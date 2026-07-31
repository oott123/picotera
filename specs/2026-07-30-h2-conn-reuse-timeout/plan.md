# Plan

前置阅读：`design.md`。所有改动集中在 `pkg/server/` 与 `pkg/configx/`，不涉及 DB、contract、openapi、dashboard。

## 1. 配置：全局关复用开关

`pkg/configx/configx.go`：

- `Config` 增加 `GatewayDisableKeepAlives bool \`mapstructure:"gateway_disable_keep_alives"\``（与其他 Gateway 字段并列）。
- `viper.SetDefault("gateway_disable_keep_alives", false)`。`bindEnvs` 自动覆盖，无需额外绑定。

## 2. transport 结构：保留 h2 句柄

`pkg/server/server.go`：

- `newGatewayTransport` 签名改为 `(config *configx.Config, responseHeaderTimeout time.Duration) (*http.Transport, *http2.Transport)`，返回 `http2.ConfigureTransports` 的结果（`err != nil` 时返回 `nil` h2 句柄，行为与现状一致）；构造 `http.Transport` 时设 `DisableKeepAlives: config.GatewayDisableKeepAlives`。
- `NewServer` 相应接收两对返回值，把两个 `*http2.Transport` 传给 `newProxyTransportCache`。

`pkg/server/proxy_transport.go`：

- `proxyTransportCache` 增加字段 `streamH2, nonStreamH2 *http2.Transport`；`newProxyTransportCache` 签名改为接收 `(streamBase, nonStreamBase *http.Transport, streamH2, nonStreamH2 *http2.Transport)`。
- 新方法 `closeIdle(proxyURL string, streaming bool)`：对 `get(proxyURL, streaming)` 返回的 t1 调 `CloseIdleConnections()`；对应 streaming 侧的 h2 句柄非 nil 时也调 `CloseIdleConnections()`。

## 3. 隔离状态机

新文件 `pkg/server/conn_quarantine.go`：

```go
type quarantineKey struct{ proxy string; streaming bool; host string }

type connQuarantine struct {
    mu      sync.Mutex
    until   map[quarantineKey]time.Time
    now     func() time.Time // 生产为 time.Now，单测注入
}

const connQuarantineTTL = 60 * time.Second
```

- `mark(proxy string, streaming bool, host string)`：写入 `now()+TTL`，顺带删除已过期项。
- `active(proxy string, streaming bool, host string) bool`：查表并判断未过期；过期即删。
- `Server` 增加字段 `connQuarantine *connQuarantine`，`NewServer` 初始化。

## 4. 错误识别

`pkg/server/gateway_helpers.go`（`forwardRequest` 旁）：

```go
// isAwaitHeadersTimeout 匹配 h2 的 "http2: timeout awaiting response headers"
// 与 h1 的 "net/http: timeout awaiting response headers"。两者均为未导出错误类型，
// 无 sentinel 可比较，只能按子串识别。
func isAwaitHeadersTimeout(err error) bool
```

实现：`err != nil && strings.Contains(err.Error(), "timeout awaiting response headers")`。

## 5. `forwardRequest` 改造（唯一收口点，签名不变）

`pkg/server/gateway_helpers.go`：

1. `t := s.proxyCache.get(proxyURL, streaming)`。
2. 隔离检查：`if s.connQuarantine.active(proxyURL, streaming, req.URL.Host) { req.Close = true; Debug 日志 }`。
3. 装 trace：`httptrace.ClientTrace{GotConn: ...}` 捕获 `Reused/WasIdle/IdleTime/LocalAddr/RemoteAddr` 到局部变量，并在 GotConn 回调里发 Debug 日志（字段 `conn_reused`、`conn_was_idle`、`conn_idle_time`、`conn_local`、`conn_remote`）；`req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))`。
4. `resp, err := t.RoundTrip(req)`。
5. `err != nil` 时：Warn 日志（连接字段 + err）；若 `isAwaitHeadersTimeout(err)` → `s.connQuarantine.mark(...)` + `s.proxyCache.closeIdle(proxyURL, streaming)` + Warn 日志（`host`、`proxy`、`streaming`、TTL）。
6. 返回 `resp, err`。

日志一律 `logx.WithContext(req.Context())`（attempt ctx 携带 request id）。

## 6. 单元测试

新文件 `pkg/server/conn_quarantine_test.go`、`forwardRequest` 相关加到合适的现有/新 `_test.go`：

- `isAwaitHeadersTimeout`：h2 串、h1 串、`fmt.Errorf("%w", ...)` 包裹 → true；`context.DeadlineExceeded`、`&net.DNSError{IsTimeout: true}`、普通 error、nil → false。
- `connQuarantine`：注入假时钟——mark 后 `active` 为 true；不同 host / proxy / streaming 的 key 为 false；时钟推进超过 TTL 后为 false 且条目被清除。
- `forwardRequest` 集成行为（`httptest.NewServer`，HTTP/1.1 足以验证机制）：
  - 先手动 `mark` 对应 key，再经 `forwardRequest` 发请求，服务端断言收到 `Connection: close`。
  - 未隔离时不携带 `Connection: close`。
  - GotConn 路径：请求成功后 Debug 日志分支被走到（以捕获变量断言 local/remote 非空即可）。
- 构造被测 `Server`：按 `pkg/server` 现有纯单测惯例手工拼 `Server{proxyCache: ..., connQuarantine: ...}`（`newGatewayTransport` 可用最小 `configx.Config` 直接调用）。

## 7. 构建与验收

- `go build -o picotera ./cmd/picotera && go test ./pkg/server/...`。
- 无 contract 改动，不需要 `mise run openapi` / dashboard 步骤。

## 8. 上线验证 runbook（代码合并后执行）

1. 部署后等待下一次爆发（无需开关，instrumentation 与隔离默认生效）。
2. 日志确认：失败 attempt 的 Warn 日志 `conn_reused=true` 且连串失败共享同一 `conn_local→conn_remote`；用该连接身份反查此前请求，与 `finish_reason=cancelled` 行对照，验证「取消污染连接」假设。
3. 效果确认：出现 `quarantine` Warn 日志后，同上游后续 attempt 应改用新连接（`conn_remote` 端口变化 / `conn_reused=false`），单次爆发长度收敛到 1–2 个 attempt。
4. 需要帧级证据时临时对进程加 `GODEBUG=http2debug=2`。
5. 若隔离机制仍不能止血：设 `PICOTERA_GATEWAY_DISABLE_KEEP_ALIVES=true` 全局关闭连接复用（紧急止血，代价为每请求一次 TCP+TLS 握手）。

存量数据旁证（可在实现前先跑）：按 provider + 时间聚合 `finish_reason = FinishReasonHeadersTimeout` 的 `request` 行确认爆发簇形态，并检查簇前是否紧邻同 provider 的 `FinishReasonCancelled` 行。
