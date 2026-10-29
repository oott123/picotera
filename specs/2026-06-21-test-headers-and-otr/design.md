# 设计：测试页面自定义 Headers 与 OTR

纯前端改动，集中在 `dashboard/src/views/TestView.vue` 与 `dashboard/src/api/client.ts`。无后端、无 OpenAPI 改动：网关测试本就是发往真实网关的原始 `fetch`，自定义 header 直接生效；`X-PicoTera-OTR` 的合法性校验与 `X-PicoTera*` 前缀的上游清理均已在网关侧实现。

## 作用范围

两项控件**只在网关测试（`mode === 'gateway'`）下渲染并参与发送**。短路测试（direct）的表单、`postTestDirect` 调用、payload 结构均不变。

## OTR 选项（上方结构化输入）

- 用 `SegmentedControl` 呈现四档：`跟随设置`（值 `''`）/ `none` / `body` / `body-and-message`，与 `SettingsView.vue` 的标签文案对齐（完整记录 / 不记录内容 / 不记录内容和梗概）。
- `otrOverride = ref<'' | 'none' | 'body' | 'body-and-message'>('')`，默认 `''` 表示「跟随设置」，不生成 `X-PicoTera-OTR` 头。
- 置于网关表单上方（端点/路径选择之后），作为驱动自定义请求头的结构化输入。

## 自定义 Headers（底部高级选项，自动生成 + 手动覆盖）

形态与「原始请求体」完全对齐：根据上方结构化表单自动生成，可手动覆盖。

- `gatewayHeaders = ref<Record<string, string>>({})` —— 实际发送并供编辑的 header 表，用 `AnnotationsEditor` 编辑（已产出 `Record<string, string>`，语义契合）。
- `generatedHeaders = computed(...)` —— 从结构化表单派生。当前唯一来源是 OTR：`otrOverride` 非空时生成 `{ 'X-PicoTera-OTR': otrOverride }`，否则为空。
- `headersManualOverride = ref(false)` —— 是否已手动覆盖。
- `watch(generatedHeaders)`（`immediate` + `deep`）：未手动覆盖时把生成结果同步进 `gatewayHeaders`。
- `AnnotationsEditor` 用 `:model-value` + `@update:model-value="onHeadersInput"`（而非 `v-model`）以区分程序化同步与用户编辑：`onHeadersInput` 置 `headersManualOverride = true` 并接管编辑值。
- 「由字段重建」按钮（`rebuildHeaders`）清除手动覆盖并用 `generatedHeaders` 重填；「已手动覆盖」提示与请求体区一致。
- 仅网关模式渲染（`v-if="mode === 'gateway'"`），位于「原始请求体」区之后。

`postGatewayTest` 增加可选 `headers?: Record<string, string>` 形参，合并进 `fetch` 的 headers：

```
headers: { 'Content-Type': 'application/json', ...headers, Authorization: `Bearer ${apiKey}` }
```

合并次序：默认 `Content-Type` 在前（允许用户覆盖），用户自定义 header 居中，`Authorization` 置于末尾**强制覆盖**——网关测试以所选 API Key 鉴权，不允许用户头意外破坏鉴权。

## send() 集成

`send()` 网关分支直接把 `gatewayHeaders`（已含自动生成的 OTR 头）传入 `postGatewayTest`，不再二次注入 OTR。direct 分支不变。
