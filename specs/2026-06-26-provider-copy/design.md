# 渠道复制功能设计

## 范围

复制入口添加在 `dashboard/src/views/ProvidersView.vue` 的渠道表格行操作区。该功能复制当前渠道本身，也就是 `ProviderView` 记录，并复制该渠道的 endpoint 绑定；新渠道创建成功且绑定复制完成后立即打开 `ProviderForm` 编辑侧栏。

## 行为

点击 duplicate 按钮后，前端基于当前行的 `ProviderView` 构造一个创建请求：

- `id` 固定为 `0`，触发现有 `PUT /api/picotera/providers` 的创建路径。
- `name` 使用 `原名 (n)` 格式生成。`n` 从 `1` 开始递增，直到不与当前已加载渠道名称重复。
- `credentials`、`priority`、`providerModels`、`annotations`、`disabled`、`proxyUrl`、`modelsEndpointUrl`、`modelsEndpointResolver`、`supportsNativeWebSearch` 按原渠道复制。

创建成功后，前端调用 `listProviderEndpoints(sourceProviderId)` 读取原渠道的 endpoint 绑定，并逐条调用 `upsertProviderEndpoint` 写入新渠道：

- `providerId` 使用新渠道 id。
- `endpointPath`、`upstreamUrl`、`credentialsResolver` 按原绑定复制。

所有绑定写入完成后，使用接口返回的新 `ProviderView` 打开 `ProviderForm`，侧栏 key 使用新渠道 id 的编辑 key。列表通过现有 `invalidateProviders` 刷新，绑定缓存通过现有 `invalidateProviderEndpoints` 刷新。

复制流程不是后端事务。若新渠道已创建但绑定复制失败，前端保留新渠道，打开新渠道编辑侧栏，并显示“渠道已创建，但端点绑定复制失败”的错误信息。

## UI

渠道表格每行增加一个 duplicate 图标按钮，使用现有 `IconButton` 与 `Icon name="copy"`。按钮放在禁用、模型、端点绑定、编辑、删除这些操作旁边，中文文案为「复制渠道」。创建期间禁用当前行的 duplicate 按钮，避免重复提交。

失败时在渠道页面显示与现有 CRUD 一致的错误信息。复制不是破坏性操作，不弹确认框。

## API

不新增后端 API。前端复用现有 `upsertProvider`、`listProviderEndpoints`、`upsertProviderEndpoint`。后端 `handleUpsertProvider` 在 `id=0` 时创建新渠道并返回创建后的记录。

## 约束

名称生成只基于当前已加载的渠道列表，不做宽松输入归一化，不修改后端校验。若后端因为并发或其他约束拒绝创建，前端显示接口错误。

不引入第三方库。
