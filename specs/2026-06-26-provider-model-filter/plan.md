# 执行计划

1. 新增 `dashboard/src/ui/MultiColumnFilter.vue`：
   - 复用 `SelectMenu.vue` 作为浮层和搜索容器。
   - 复用 `ColumnFilterOption` 类型。
   - Props 包含 `label`、`modelValue`、`options`、`allLabel`、`placeholder`、`align`、`searchable`、`formatActive`。
   - Emits 为 `update:modelValue: [V[]]`。
   - 实现 `isActive`、`activeLabel`、`toggleValue(value)`、`clear(e)`、`pickAll()`。
   - 触发器样式对齐 `ColumnFilter.vue`，选中摘要显示单项 label 或「N 项」。
   - 浮层 header 提供「全部」清空项，列表项点击切换选中并保持浮层打开。

2. 在 `dashboard/src/ui/index.ts` 导出 `MultiColumnFilter`。

3. 编辑 `dashboard/src/views/ProvidersView.vue` 的脚本：
   - import `SegmentedControl`、`MultiColumnFilter`、`ColumnFilterOption` 类型。
   - 增加 `selectedModels = ref<string[]>([])`。
   - 增加 `modelMatchMode = ref<'or' | 'and'>('or')`。
   - 增加 `modelMatchModeOptions`，选项为 `{ value: 'or', label: '或' }` 和 `{ value: 'and', label: '与' }`。
   - 增加 `modelFilterOptions`，从完整 `providers` 的 `modelNames(p)` 去重、排序后生成。
   - 增加 `providerMatchesModelFilter(p)`，按空筛选、`or`、`and` 三种情况严格判断。
   - 增加 `filteredProviders`，在现有排序后的 `providers` 基础上应用模型筛选。
   - 将 `count` 改为过滤后的数量，另保留 `totalCount` 表示完整渠道数。
   - 保持 `duplicateProvider()` 的名称查重使用完整 `providers`。

4. 编辑 `ProvidersView.vue` 的模板：
   - 顶部计数在有筛选时显示 `{{ count }} / {{ totalCount }} 个渠道`，无筛选时显示 `{{ count }} 个渠道`。
   - “模型”表头内容改为 `MultiColumnFilter`。
   - 当 `selectedModels.length > 1` 时，在模型表头内显示小尺寸 `SegmentedControl` 切换「或 / 与」。
   - 表格行循环从 `providers` 改为 `filteredProviders`。
   - 空状态区分加载、未筛选无渠道、已筛选无匹配。

5. 检查样式细节：
   - 表头筛选触发器在表格内不撑宽、不遮挡清除按钮。
   - 多选摘要长文本截断。
   - 「或 / 与」切换控件在表头内保持紧凑，不影响其他列对齐。

6. 运行前端校验：
   - `pnpm --dir dashboard type-check`
   - `pnpm --dir dashboard lint`

7. 手动验证渠道页：
   - 未筛选时列表和排序与当前行为一致。
   - 单选模型时仅显示包含该模型的渠道。
   - 多选并选择「或」时，显示包含任一选中模型的渠道。
   - 多选并选择「与」时，显示同时包含全部选中模型的渠道。
   - 点击清除后恢复完整渠道列表。
   - 筛选状态下禁用、模型面板、端点绑定、复制、编辑、删除等行操作仍作用于正确渠道。

## 范围说明

- 无后端、数据库、OpenAPI、生成类型改动。
- 不引入第三方依赖。
- 不把筛选状态写入 URL。
