# 执行计划

## 1. 数据库

1. 新建 `db/migrations/046_provider_insecure_tls.sql`：

   ```sql
   -- +goose Up
   ALTER TABLE provider ADD COLUMN insecure_tls BOOLEAN NOT NULL DEFAULT FALSE;

   -- +goose Down
   ALTER TABLE provider DROP COLUMN insecure_tls;
   ```

2. `db/queries/provider.sql`：
   - `CreateProvider` 的列表与 VALUES 各加 `insecure_tls` / `$11`。
   - `UpdateProvider` 加一行 `insecure_tls = CASE WHEN @set_insecure_tls::bool THEN @insecure_tls::bool ELSE insecure_tls END,`（放在 `disabled` 之后、`proxy_url` 之前均可，跟随现有列序）。
3. `db/queries/routing.sql`：在 `GetProvidersByEndpointAndModel`、`GetProvidersByEndpointTypesAndModel`、`GetProvidersByEndpoint` 三个 SELECT 的 `p.proxy_url,` 后各加 `p.insecure_tls,`。
4. 运行 `sqlc generate`，确认 `pkg/db/` 重新生成（不手改）。

## 2. 契约层

5. `pkg/contract/provider.go`：
   - `ProviderView`、`CreateProviderRequest.Body`、`UpsertProviderRequest.Body` 各加 `InsecureTls bool \`json:"insecureTls"\``。
   - `ToProviderView` 填 `InsecureTls: provider.InsecureTls`；`FromProviderView` 回填到 `db.Provider`。
6. `pkg/server/handle_providers.go`：
   - create 路径的 `db.CreateProviderParams` 加 `InsecureTls: input.Body.InsecureTls`。
   - upsert 路径：新建分支的 `CreateProviderParams` 同上；更新分支的 `UpdateProviderParams` 加 `SetInsecureTls: true, InsecureTls: input.Body.InsecureTls`。

## 3. 传输层改造（`pkg/server`）

7. `proxy_transport.go`：
   - 新增 `transportProfile{ProxyURL string; InsecureTLS bool}`。
   - `transportKey` 改为 `{profile transportProfile; streaming bool}`；新增 `transportEntry{t1 *http.Transport; h2 *http2.Transport}`。
   - `proxyTransportCache` 字段改为 `build func(profile transportProfile, streaming bool) (*http.Transport, *http2.Transport)` + `cache map[transportKey]transportEntry`；删除 `streamBase`/`nonStreamBase`/`streamH2`/`nonStreamH2` 与 `base()` 方法。
   - `newProxyTransportCache(build)` 只收构造函数。
   - `get(profile, streaming) *http.Transport` 改为惰性构造（RLock 命中 → Lock 双检 → `build` → 存表），零值 profile 不再特判走 base。
   - `closeIdle(profile, streaming)` 用条目自身的 `t1`/`h2` 关闭空闲连接，删除原来对共享 h2 句柄的特殊处理与相应注释。
   - `applyProxyConfig` 保持不变（由 `build` 闭包调用）。
8. `server.go`：
   - `newGatewayTransport(config, responseHeaderTimeout, insecureTLS bool)`：当 `insecureTLS` 为真，在 `http2.ConfigureTransports(t)` **之前** 设置 `t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}`，并写注释说明顺序原因（ConfigureTransports 需要往该 config 的 `NextProtos` 追加 `h2`）。
   - `NewServer`：删除 `baseTransport`/`nonStreamBase`/两个 h2 句柄的预建，改为
     ```go
     proxyCache := newProxyTransportCache(func(profile transportProfile, streaming bool) (*http.Transport, *http2.Transport) {
         timeout := config.GatewayResponseHeaderTimeout
         if !streaming {
             timeout = config.GatewayReadTimeout
         }
         t, h2 := newGatewayTransport(config, timeout, profile.InsecureTLS)
         applyProxyConfig(t, profile.ProxyURL)
         return t, h2
     })
     ```
     把原 `nonStreamBase` 上那段「必须各自 ConfigureTransports」的注释移到这个闭包上。
   - 删除 `Server.httpClient` 字段及其赋值（无任何读取方）。
9. `conn_quarantine.go`：`quarantineKey.proxy string` → `profile transportProfile`；`mark`/`active` 首参改为 `profile transportProfile`。
10. `gateway_helpers.go`：
    - `newEphemeralTransport(profile transportProfile, streaming bool)`：`newGatewayTransport(s.config, responseHeaderTimeout, profile.InsecureTLS)` + `applyProxyConfig(t, profile.ProxyURL)`。
    - `forwardRequest(req *http.Request, profile transportProfile, streaming bool)`：内部 `s.proxyCache.get(profile, streaming)`、`s.connQuarantine.active/mark(profile, ...)`、`s.proxyCache.closeIdle(profile, ...)`；日志字段 `"proxy": profile.ProxyURL` 保留并加 `"insecure_tls": profile.InsecureTLS`。
    - `providerCandidateRow` 加 `InsecureTLS bool`；`fromModelRoutedRow`/`fromNoModelRow` 各加一行映射。

## 4. 调用点接线

11. `gateway_flow_candidates.go`：
    - `gatewayCandidateSidecar` 加 `InsecureTLS bool`；新增方法
      ```go
      func (s gatewayCandidateSidecar) transport() transportProfile {
          return transportProfile{ProxyURL: s.ProxyURL, InsecureTLS: s.InsecureTLS}
      }
      ```
    - `buildPathCandidateSet`（用 `row.InsecureTLS`）与 `buildUnifiedCandidateSet`（用 `row.InsecureTls`，sqlc 生成名）各填该字段。
12. `gateway_flow_attempts.go:170`：`f.h.forwardRequest(prepared.Request, side.transport(), f.model.Mode.Streaming)`。
13. `handle_provider_endpoint.go`（`handleFetchModels`）：构造 `transportProfile{ProxyURL: provider.ProxyUrl.String, InsecureTLS: provider.InsecureTls}`（`ProxyUrl` 无效时 `.String` 即空串，沿用现有 `if Valid` 写法）并传入 `forwardRequest`。
14. `handle_test_direct.go`：同样构造 profile 传入 `forwardRequest`。

## 5. 测试

15. `pkg/server/proxy_transport_test.go`：
    - `newGatewayTransport` 的调用加第三个参数 `false`。
    - `TestNewGatewayTransportDisableHTTP2` 中关于 `Clone()` 保留空 `TLSNextProto` 的断言删除（缓存不再 Clone），改为断言 `insecureTLS=true` 时同样得到非 nil 空 `TLSNextProto`。
    - 新增 `TestNewGatewayTransportInsecureTLS`：`InsecureSkipVerify` 为 true，且 `TLSClientConfig.NextProtos` 含 `h2`（验证赋值顺序）。
    - 新增缓存测试：同一 `build` 下 `get({ProxyURL:"direct"}, false)` 与 `get({ProxyURL:"direct", InsecureTLS:true}, false)` 返回不同实例，且相同 key 两次调用返回同一实例。
16. `pkg/server/gateway_helpers_test.go` / `conn_quarantine_test.go`：
    - `newProxyTransportCache(streamBase, nonStreamBase, streamH2, nonStreamH2)` 改为传构造闭包，按 streaming 返回原来对应的测试 transport。
    - `s.forwardRequest(req, "direct", …)` → `s.forwardRequest(req, transportProfile{ProxyURL: "direct"}, …)`。
    - `connQuarantine.mark/active("direct", …)` 同步改为 profile。
17. 新增端到端式单测（放 `pkg/server/proxy_transport_test.go`）：用 `httptest.NewTLSServer`（自签证书）+ `newEphemeralTestServer` 风格的手搭 `Server`，验证 `InsecureTLS:false` 时 `forwardRequest` 返回证书错误、`InsecureTLS:true` 时成功。
18. 运行 `go build ./... && go test ./pkg/...`。

## 6. 契约与前端

19. `mise run openapi` 重新生成 `openapi.yaml`。
20. `pnpm --dir dashboard generate-openapi` 重新生成 `dashboard/src/openapi-types.d.ts`。
21. `dashboard/src/components/ProviderForm.vue`：
    - `form` 初值加 `insecureTls: props.provider?.insecureTls ?? false`。
    - `submit()` 的 body 加 `insecureTls: form.value.insecureTls`。
    - 模板在「代理 URL」Field 之后插入：
      ```html
      <Field label="TLS" as="div">
        <label class="inline-flex items-center gap-2 text-sm cursor-pointer">
          <input v-model="form.insecureTls" type="checkbox" class="cursor-pointer" />
          <span>不校验上游 HTTPS 证书（仅用于自签名证书的私有部署）</span>
        </label>
      </Field>
      ```
22. `pnpm --dir dashboard type-check`、`pnpm --dir dashboard lint`。

## 7. 验收

23. `docker compose up -d` + `mise run server`：启动跑通迁移 046。
24. 新建/编辑渠道勾选该框 → 重新打开表单确认状态回显。
25. 指向一个自签名证书的上游：勾选前「获取模型」与网关请求报 `x509: certificate signed by unknown authority`，勾选后成功；同时确认未勾选的其它渠道行为不变。
