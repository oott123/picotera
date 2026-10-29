# 执行计划

## 1. `pkg/server/handle_test_direct.go` —— 转发自定义头，移除硬编码版本头

- `testDirectRequest` 增加字段：
  ```go
  Headers map[string]string `json:"headers"`
  ```
- 在 `req.Header.Set("Content-Type", "application/json")` 之后、`applyCredentials(...)` 之前，写入自定义头：
  ```go
  for k, v := range in.Headers {
      req.Header.Set(k, v)
  }
  ```
- 删除以下硬编码块（含其上方注释）：
  ```go
  if endpoint.EndpointType == contract.EndpointType_AnthropicMessages {
      req.Header.Set("anthropic-version", "2023-06-01")
  }
  ```
  若删除后 `contract` 包不再被引用，移除其 import。
- 更新「No client headers are copied」注释，说明现在转发测试页传入的自定义头，凭据仍在最后强制写入。

## 2. `dashboard/src/api/client.ts` —— `postTestDirect` 携带 headers

- `TestDirectPayload` 增加 `headers?: Record<string, string>`。
- `postTestDirect` 序列化 payload 时一并带上 `headers`（payload 直接 `JSON.stringify`，无需改动 body 拼装）。

## 3. `dashboard/src/views/TestView.vue` —— 头编辑器对两种模式生效

- 状态更名：`gatewayHeaders` → `customHeaders`（连同 `watch` / `onHeadersInput` / `rebuildHeaders` / 模板引用）。
- `generatedHeaders` 改为：
  ```ts
  const generatedHeaders = computed<Record<string, string>>(() => {
    const h: Record<string, string> = {}
    if (baseFormat.value === 'anthropicMessages') h['anthropic-version'] = '2023-06-01'
    if (mode.value === 'gateway' && otrOverride.value) h['X-PicoTera-OTR'] = otrOverride.value
    return h
  })
  ```
- 模板中「自定义请求头（高级）」`Field` 去掉 `v-if="mode === 'gateway'"`，对两种模式都渲染（OTR 控件仍保留在 gateway 分支内不动）。
- `send()` 的 direct 分支把 `customHeaders.value` 传入 `postTestDirect`：
  ```ts
  res = await postTestDirect(
    {
      providerId: directProviderId.value!,
      endpointPath: directEndpointPath.value,
      stream: effectiveStream.value,
      pathVars: effectivePathVars(),
      headers: customHeaders.value,
      body,
    },
    controller.signal,
  )
  ```
  gateway 分支继续传 `customHeaders.value` 给 `postGatewayTest`。

## 4. 验证

- `go build ./...`
- `pnpm --dir dashboard type-check`
- `pnpm --dir dashboard lint`
- 手动：
  - 短路测试选 Anthropic Messages 端点，确认底部出现自定义请求头编辑器且自动含 `anthropic-version: 2023-06-01`；发送后上游收到该头。
  - 短路测试添加自定义头并发送，确认上游收到；移除 `anthropic-version` 后发送，确认后端不再兜底注入。
  - 网关测试 Anthropic 格式下生成头同时含 `anthropic-version` 与（设置 OTR 时）`X-PicoTera-OTR`。
  - 短路测试不出现 OTR 控件。
