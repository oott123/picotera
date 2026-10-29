# 执行计划

1. 在 `dashboard/src/views/ProvidersView.vue` 增加复制 mutation，复用 `upsertProvider` 创建渠道副本，成功后调用 `invalidateProviders(queryClient)`。

2. 在同一文件增加 `nextDuplicatedProviderName(sourceName: string)`，从当前 `providers` 列表收集已占用名称，按 `原名 (1)`、`原名 (2)` 递增生成第一个未占用名称。

3. 在 `ProvidersView.vue` 引入 `listProviderEndpoints`、`upsertProviderEndpoint`、`invalidateProviderEndpoints`。

4. 增加 `duplicateProvider(p: ProviderView)`：
   - 清空页面级错误状态。
   - 构造 `{ ...p, id: 0, name: nextDuplicatedProviderName(p.name) }` 请求体。
   - 调用复制 mutation。
   - 调用 `listProviderEndpoints(p.id)` 读取原渠道 endpoint 绑定。
   - 对每条绑定调用 `upsertProviderEndpoint({ ...binding, providerId: created.id })`。
   - 调用 `invalidateProviderEndpoints(queryClient)` 刷新绑定缓存。
   - 使用 mutation 返回的新渠道调用 `panel.open(ProviderForm, { provider: created }, { key: editKey(created.id) })`。
   - 若渠道已创建但绑定复制失败，保留新渠道，打开新渠道编辑侧栏，并显示“渠道已创建，但端点绑定复制失败”。

5. 给渠道表格行操作区增加 duplicate 按钮：
   - 使用 `IconButton` 和 `Icon name="copy"`。
   - `title` 与 `aria-label` 设为「复制渠道」。
   - 当该行正在复制时禁用按钮。

6. 在 `ProvidersView.vue` 增加页面级错误展示。复制失败时显示接口错误或「复制渠道失败」。

7. 运行前端校验：
   - `pnpm --dir dashboard type-check`
   - `pnpm --dir dashboard lint`

8. 手动检查渠道页面交互：
   - 点击复制后新渠道名称为 `原名 (1)`。
   - 当 `原名 (1)` 已存在时，新名称为 `原名 (2)`。
   - 新渠道包含原渠道的 endpoint 绑定。
   - 复制成功后打开新渠道编辑界面。
   - 渠道创建失败时页面显示错误且不打开新渠道编辑界面。
   - 渠道已创建但绑定复制失败时页面显示错误并打开新渠道编辑界面。
