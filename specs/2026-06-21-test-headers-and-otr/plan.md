# 执行计划

## 1. `dashboard/src/api/client.ts` —— 扩展 `postGatewayTest`

- 在 `body` 与 `signal` 之间增加形参 `headers?: Record<string, string>`。
- 合并 headers：`{ 'Content-Type': 'application/json', ...headers, Authorization: \`Bearer ${apiKey}\` }`。
- `postTestDirect` 不动。

## 2. `dashboard/src/views/TestView.vue` —— 控件与状态

- import `AnnotationsEditor`（`@/components/AnnotationsEditor.vue`）。
- OTR 状态与选项常量（标签对齐 `SettingsView.vue`）：
  ```ts
  type OtrOverride = '' | 'none' | 'body' | 'body-and-message'
  const otrOverride = ref<OtrOverride>('')
  const otrOptions = [
    { value: '', label: '跟随设置' },
    { value: 'none', label: '完整记录' },
    { value: 'body', label: '不记录内容' },
    { value: 'body-and-message', label: '不记录内容和梗概' },
  ]
  ```
- 自定义请求头状态（自动生成 + 手动覆盖，镜像 `rawBody`）：
  ```ts
  const gatewayHeaders = ref<Record<string, string>>({})
  const headersManualOverride = ref(false)
  const generatedHeaders = computed<Record<string, string>>(() => {
    const h: Record<string, string> = {}
    if (otrOverride.value) h['X-PicoTera-OTR'] = otrOverride.value
    return h
  })
  ```
- 同步 watch 与处理函数（紧跟 `rebuildBody` 之后）：
  ```ts
  watch(generatedHeaders, (h) => {
    if (headersManualOverride.value) return
    gatewayHeaders.value = { ...h }
  }, { immediate: true, deep: true })

  function onHeadersInput(v: Record<string, string>) {
    gatewayHeaders.value = v
    headersManualOverride.value = true
  }
  function rebuildHeaders() {
    headersManualOverride.value = false
    gatewayHeaders.value = { ...generatedHeaders.value }
  }
  ```
- `send()` 网关分支直接传 `gatewayHeaders.value`：
  ```ts
  res = await postGatewayTest(substitutePath(targetPath), selectedApiKey.value!.key, body, gatewayHeaders.value, controller.signal)
  ```

## 3. `dashboard/src/views/TestView.vue` —— 模板

- **OTR 控件**：在网关模式块（`<template v-else>`，端点/路径选择之后）增加 `Field`（`as="div"`，label「数据记录（OTR）」）包裹 `SegmentedControl`（`v-model="otrOverride"` `:options="otrOptions"`），附说明文案与 `X-PicoTera-OTR` 提示。
- **自定义请求头**：在「原始请求体」`Field` 之后增加 `Field`（`v-if="mode === 'gateway'"`，`as="div"`，label「自定义请求头（高级）」），内部：
  - `<AnnotationsEditor :model-value="gatewayHeaders" @update:model-value="onHeadersInput" />`
  - 「由字段重建」`Button`（`@click="rebuildHeaders"`）+ `v-if="headersManualOverride"` 的「已手动覆盖」提示，布局与请求体区一致。

## 4. 验证

- `pnpm --dir dashboard type-check`
- `pnpm --dir dashboard lint`
- 手动：网关测试下添加自定义 header 与各档 OTR，确认请求带上对应头；短路测试界面不出现这两个控件。
