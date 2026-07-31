# Plan

前置阅读：`design.md`。基线是工作区中已实现（未提交）的 `specs/2026-07-30-h2-conn-reuse-timeout/`。全部改动在 `pkg/server/` 与 `pkg/configx/`，无 DB / contract / openapi / dashboard 步骤。

## 1. 配置（`pkg/configx/configx.go`）

- `Config` 增加两个字段（与其他 Gateway 字段并列）：
  - `GatewayDisableHTTP2 bool \`mapstructure:"gateway_disable_http2"\``
  - `GatewayEphemeralTransport bool \`mapstructure:"gateway_ephemeral_transport"\``
- `viper.SetDefault("gateway_disable_http2", false)`、`viper.SetDefault("gateway_ephemeral_transport", false)`。`bindEnvs` 自动覆盖 env 绑定。

## 2. transport 构造（`pkg/server/server.go` + `pkg/server/proxy_transport.go`）

`newGatewayTransport(config, responseHeaderTimeout)`：

- 无条件补 `MaxIdleConnsPerHost: 100`。
- `config.GatewayDisableHTTP2 == true` 时：设 `ForceAttemptHTTP2: false`、`TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}`（非 nil 空 map），**不调用** `http2.ConfigureTransports`，返回 `(t, nil)`。`ReadIdleTimeout`/`PingTimeout` 仅在 h2 句柄非 nil 时设置（现有代码结构不变）。
- 函数注释更新，写明非 nil 空 TLSNextProto 关闭自动 h2、且 `Transport.Clone` 会复制该 map 使代理变体同样关闭。

`proxy_transport.go`：

- 从 `proxyTransportCache.get` 中提取代理应用逻辑为包级函数：

  ```go
  // applyProxyConfig sets t.Proxy per the proxyURL semantics shared by the
  // transport cache and ephemeral transports: "" keeps ProxyFromEnvironment,
  // "direct" disables proxying, a URL string routes through that proxy, and an
  // unparsable URL falls back to ProxyFromEnvironment (API validation catches
  // it earlier; this mirrors the cache's historical fallback-to-base behavior).
  func applyProxyConfig(t *http.Transport, proxyURL string)
  ```

  `get` 改为 clone 后调用它（"" 分支仍直接返回 base，不 clone；非法 URL 分支从"返回 base"改为"回落 ProxyFromEnvironment"——对 clone 而言语义等价，且 ephemeral 路径可共用）。

## 3. ephemeral transport（`pkg/server/gateway_helpers.go`）

- `Server` 新增方法：

  ```go
  // newEphemeralTransport builds a one-shot transport for a single attempt:
  // full gateway config, proxy applied, keep-alives forced off so the
  // connection dies with the request instead of lingering in an unreachable
  // idle pool.
  func (s *Server) newEphemeralTransport(proxyURL string, streaming bool) (*http.Transport, *http2.Transport)
  ```

  实现：`responseHeaderTimeout := s.config.GatewayResponseHeaderTimeout`（`!streaming` 时用 `s.config.GatewayReadTimeout`，与 NewServer 两个 base 的取值一致）→ `newGatewayTransport(s.config, responseHeaderTimeout)` → `t.DisableKeepAlives = true` → `applyProxyConfig(t, proxyURL)`。

- `forwardRequest` 改造：
  1. transport 选择：`config.GatewayEphemeralTransport` 为 true 时用 `newEphemeralTransport`（拿到 `t, h2`），否则 `s.proxyCache.get(proxyURL, streaming)`（`h2` 为 nil，不参与回收）。
  2. RoundTrip 出错且 ephemeral 时：立即 `t.CloseIdleConnections()` + `h2 != nil` 时 `h2.CloseIdleConnections()`，再走现有错误处理（Warn 日志、quarantine mark）。
  3. RoundTrip 成功且 ephemeral 时：`resp.Body` 包为 `closeIdleOnCloseBody`（内嵌 `io.ReadCloser` + `sync.Once`，`Close` 先关 body 再对 t1/h2 调 `CloseIdleConnections`），替换 `resp.Body` 后返回。类型放在 `gateway_helpers.go` 内。
  4. quarantine 的 active/req.Close/mark/`proxyCache.closeIdle` 逻辑不加任何 ephemeral 分支，保持原样。

## 4. instrumentation 补充（`pkg/server/gateway_helpers.go`）

`forwardRequest` 现有 `httptrace.ClientTrace` 增加：

- `WroteRequest`：记录 `time.Now()` 到局部变量 `wroteRequestAt`。
- `GotFirstResponseByte`：记录到局部变量 `gotFirstByteAt`。

失败路径的 Warn 日志追加字段：`wrote_request_ago`（`time.Since(wroteRequestAt)`，零值时输出 0 表示请求未写完）、`got_first_byte`（bool）。成功路径不加新日志。

## 5. flush 修复（三个写出点）

新 helper（放 `gateway_helpers.go`）：

```go
// commitResponseHeaders writes the status line and immediately flushes it to
// the wire. Without the flush, Go's http server buffers the header block until
// the first body flush — a downstream client's response-header timer then
// covers our whole time-to-first-chunk instead of stopping when we commit.
func commitResponseHeaders(w http.ResponseWriter, status int)

// markSSENoBuffering sets X-Accel-Buffering: no when contentType is
// text/event-stream, telling nginx-style reverse proxies in front of us not to
// buffer the stream (headers included).
func markSSENoBuffering(h http.Header, contentType string)
```

- `commitResponseHeaders`：`w.WriteHeader(status)`；`w` 断言为 `http.Flusher` 成功时 `Flush()`。
- `markSSENoBuffering`：`strings.Contains(strings.ToLower(ct), "text/event-stream")` 时 `h.Set("X-Accel-Buffering", "no")`。

调用点：

1. `gateway_flow_success.go` `openPathInternalReader`：`w.WriteHeader(http.StatusOK)`（约 168 行）改为先 `markSSENoBuffering(w.Header(), w.Header().Get("Content-Type"))`（CT 已由 `copyPathSuccessHeaders` 复制到位）再 `commitResponseHeaders(w, http.StatusOK)`。
2. `gateway_unified_helpers.go` `unifiedStreamSuccess`：`w.WriteHeader(http.StatusOK)`（约 438 行）改为 `streamMode` 时先 `markSSENoBuffering(w.Header(), clientCT)`，再 `commitResponseHeaders(w, http.StatusOK)`（流式/非流式统一走它）。
3. `handle_test_direct.go`：`w.WriteHeader(resp.StatusCode)` 改为 `commitResponseHeaders(w, resp.StatusCode)`（`flusher` 变量获取位置不动）。

三处 Flush 均在 `StartClientWrite`/读循环启动之前，无并发写竞争。

## 6. 单元测试

- 新文件 `pkg/server/response_commit_test.go`：
  - fake writer（实现 `http.ResponseWriter`+`http.Flusher`，记录 `["writeHeader:200","flush"]` 调用序列）→ `commitResponseHeaders` 断言顺序；仅实现 `http.ResponseWriter` 的 fake → 不 panic、状态已写。
  - `markSSENoBuffering`：`"text/event-stream"`、`"text/event-stream; charset=utf-8"` → 设头；`"application/json"`、`""` → 不设。
- `pkg/server/proxy_transport_test.go`（或现有测试文件追加）：
  - `applyProxyConfig` 三种语义 + 非法 URL 回落 ProxyFromEnvironment（比较函数指针非 nil 即可，nil/非 nil + "direct" 置 nil 可精确断言）。
- `pkg/server/server_test.go`（或新文件）`newGatewayTransport`：
  - 默认 config → h2 句柄非 nil、`MaxIdleConnsPerHost == 100`。
  - `GatewayDisableHTTP2: true` → h2 句柄 nil、`TLSNextProto != nil && len == 0`、`ForceAttemptHTTP2 == false`；`Clone()` 后 TLSNextProto 仍非 nil 空。
- `pkg/server/gateway_helpers_test.go`（追加）ephemeral `forwardRequest`：
  - httptest h1 服务端在 handler 里记录 `r.RemoteAddr`。构造 `Server{config: ..., proxyCache: ..., connQuarantine: newConnQuarantine()}`。
  - `GatewayEphemeralTransport: true` → 顺序两次 `forwardRequest`（每次读完并 Close body）源端口不同。
  - `GatewayEphemeralTransport: false`（keep-alive 默认开）→ 两次源端口相同。
  - ephemeral 响应 body Close 后，`httptest.Server.Close()` 正常返回（无活跃连接挂起）作连接回收的断言面。

## 7. 构建与验收

- `go build -o picotera ./cmd/picotera && go test ./pkg/server/...`。
- 无 contract 改动，不需要 `mise run openapi` / dashboard 步骤。

## 8. 上线 runbook（合并后执行，非代码；假设排序与判定详见 design.md）

1. 判定性一步（可先于部署）：A 侧临时加 `GODEBUG=http2debug=2`，等失败样本。日志里出现 HEADERS 帧但仍超时 → x/net 客户端内部问题铁证；无 HEADERS 帧 → 转终结层日志 + B 侧 `ss -ti`/抓包。
2. 部署 flush 修复 + instrumentation，并开启 `PICOTERA_GATEWAY_DISABLE_HTTP2=true`（保持 `PICOTERA_GATEWAY_DISABLE_KEEP_ALIVES=true`，h1 无 keep-alive = 每请求独占新连接，对客户端共享/竞态假设是整类消除）。`curl -sv -N` 验证响应头在成功提交时立刻到达。
3. A 侧把 `PICOTERA_GATEWAY_RESPONSE_HEADER_TIMEOUT` 调大到 180s+，覆盖 B 的重试循环。
4. h1 模式下彻底消失 → 假设成立；稳定后可关 DISABLE_KEEP_ALIVES 恢复 h1 连接池（MaxIdleConnsPerHost 已修）观察是否复现。仍复现 → 问题在 A 之外，回到第 1 步的终结层/B 侧排查；`PICOTERA_GATEWAY_EPHEMERAL_TRANSPORT=true` 作为最后一个 A 侧变量。
5. 残留失败结合 Warn 日志新字段 `wrote_request_ago` / `got_first_byte` 与 GODEBUG 帧日志互相印证。
