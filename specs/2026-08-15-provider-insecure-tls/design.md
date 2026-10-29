# 设计：渠道级 insecureTls

## 目标

`provider` 增加布尔字段 `insecure_tls`。开启后，该渠道发出的所有 HTTPS 请求跳过服务端证书校验（`tls.Config{InsecureSkipVerify: true}`）。

覆盖范围就是全部三处出站调用点（`Server.forwardRequest` 是唯一的出站通道，共三个调用方）：

| 调用点 | 文件 | 场景 |
| --- | --- | --- |
| 网关转发 | `pkg/server/gateway_flow_attempts.go:170` | 路径网关 + `/api/unified` 的每次上游尝试 |
| 拉取模型列表 | `pkg/server/handle_provider_endpoint.go:96` | ProviderForm 的「获取模型」 |
| 直连测试 | `pkg/server/handle_test_direct.go` | `POST /api/picotera/test/direct` |

`pkg/jsx` 里脚本用的 `picotera.fetch`（独立的 `fetchClient`）不属于渠道请求，不受影响。

## 传输层：从 proxyURL 升级为 transportProfile

当前传输层的身份只有 `proxyURL`（外加 streaming 标志）。证书校验策略同样是 **连接级** 属性——和 `Proxy` 一样无法按请求覆盖，且决定了连接池能否共用——因此把这两个字段合成一个值类型，作为传输层的唯一身份：

```go
// pkg/server/proxy_transport.go
type transportProfile struct {
    ProxyURL    string // "" 环境代理 / "direct" 不走代理 / URL
    InsecureTLS bool
}
```

`transportProfile` 替换以下位置的 `proxyURL string` 参数：`Server.forwardRequest`、`Server.newEphemeralTransport`、`proxyTransportCache.get/closeIdle`、`connQuarantine.mark/active`（`quarantineKey.proxy` → `quarantineKey.profile`）。连接隔离因此自动成立：证书策略不同的两个渠道即使指向同一 host，也落在不同的传输实例与不同的连接池上，不会互相复用连接。

### `newGatewayTransport` 增加 insecureTLS 参数

```go
func newGatewayTransport(config *configx.Config, responseHeaderTimeout time.Duration, insecureTLS bool) (*http.Transport, *http2.Transport)
```

`TLSClientConfig` 必须在 `http2.ConfigureTransports(t)` **之前** 赋值：`configureTransports` 会往 `t1.TLSClientConfig.NextProtos` 里塞 `h2`/`http/1.1`，若先 Configure 再换 `TLSClientConfig`，新 config 的 ALPN 列表是空的，HTTPS 直接退回 HTTP/1.1。这条顺序约束在代码里以注释固定下来。

由于连接由 `*http.Transport` 完成 TLS 握手后才通过 `TLSNextProto["h2"]` 升级交给 h2，`InsecureSkipVerify` 写在 h1 传输上即可对 h2 上游同样生效。

### 缓存改为「按 key 独立构造」，不再 Clone

`proxyTransportCache` 现在的做法是 `base.Clone()` 后改 `Proxy`。这条路对 insecure 变体不成立，原因有二：

1. `Clone` 浅拷贝 `TLSNextProto`，其 `"h2"` 项仍指向 base 的 `*http2.Transport`，克隆体建立的 h2 连接会进 base 的共享连接池——一条 `InsecureSkipVerify` 握手出来的连接可能被正常校验的渠道复用（现有注释已记录这一 Clone 语义）。
2. `TLSClientConfig` 的 ALPN 必须在 Configure 时就位，克隆体没有自己的 Configure 时机。

因此缓存改为：**每个 key 都用构造函数新建一套 transport（含自己的 `http2.ConfigureTransports`），条目同时保存 `*http.Transport` 与配套的 `*http2.Transport`。**

```go
type transportKey struct {
    profile   transportProfile
    streaming bool
}

type transportEntry struct {
    t1 *http.Transport
    h2 *http2.Transport
}

type proxyTransportCache struct {
    build func(profile transportProfile, streaming bool) (*http.Transport, *http2.Transport)
    mu    sync.RWMutex
    cache map[transportKey]transportEntry
}
```

`newProxyTransportCache(build)` 只收一个构造函数；`server.go` 传入的闭包按 streaming 选择 header 超时（streaming 用 `GatewayResponseHeaderTimeout`，非 streaming 用 `GatewayReadTimeout`，语义不变），并把 `profile.InsecureTLS` 透传给 `newGatewayTransport`，然后 `applyProxyConfig(t, profile.ProxyURL)`。零值 profile 的两条「base」也由同一路径惰性生成，不再在 `NewServer` 里预建。

收益：`closeIdle` 直接用条目自己的 h2 句柄，`streamH2`/`nonStreamH2` 两个特例字段和「Clone 丢失 altProto」那圈解释一并消失；每个变体的连接池互不干扰。

`applyProxyConfig` 与「`""` → 环境代理 / `direct` → 无代理 / URL 解析失败退回环境代理」的既有语义不变。

`NewServer` 里 `baseTransport` 只被一个从未被读取的 `Server.httpClient` 字段用到（全仓库只有赋值、无使用）。缓存自持构造函数后该字段失去构造来源，直接删除，不做替代实现。

### 一次性传输（默认路径）

`GatewayEphemeralTransport` 默认为 true，实际热路径是 `newEphemeralTransport`，它本来就每次新建 transport，改造只是把 `insecureTLS` 从 profile 传进 `newGatewayTransport`。

## 数据流

`provider.insecure_tls` 沿用 `proxy_url` 的既有链路，逐段并排新增：

- `db/queries/routing.sql` 三个候选查询（`GetProvidersByEndpointAndModel`、`GetProvidersByEndpointTypesAndModel`、`GetProvidersByEndpoint`）各加一列 `p.insecure_tls`。
- `providerCandidateRow`（`gateway_helpers.go`）与 `gatewayCandidateSidecar`（`gateway_flow_candidates.go`）各加一个 `InsecureTLS bool`，sidecar 上提供 `transport() transportProfile` 把两个字段合成 profile 给 `forwardRequest`。
- 拉取模型列表与直连测试直接从 `db.Provider` 行构造 profile。

列类型 `BOOLEAN NOT NULL DEFAULT FALSE`（与 `supports_native_web_search` 一致），不用可空布尔——没有「未设置」语义。

## API / 前端

契约字段名 `insecureTls`（对齐用户措辞与 `proxyUrl` 的驼峰风格），Go 字段 `InsecureTls`，无 `omitempty`（与 `SupportsNativeWebSearch` 同）。详见 `api.md`。

`ProviderForm.vue` 在「代理 URL」下方加一个复选框，与「状态」「Web 搜索」两处的写法一致（原生 `input[type=checkbox]` + `Field as="div"`），文案点明风险：`不校验上游 HTTPS 证书（仅用于自签名证书的私有部署）`。

## 明确不做

- 不暴露给 JS 脚本：`jsx.ProviderSummary` 保持 `{id, name, priority, annotations}`，脚本无法读取或改写该开关。
- 不做全局开关或环境变量：仅渠道级。
- 不加 annotation 形式的兼容读取路径。
