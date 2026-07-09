# 执行计划

## Phase 1 — 基础设施

1. **添加 `filter-plus` 图标**
   - 编辑 `dashboard/src/ui/icons/paths.ts`
   - 导入 `IconFilterPlus`
   - 在 `IconName` 类型和 `iconComponents` 映射中追加 `filter-plus`

2. **创建 `DynamicFilterBar.vue`**
   - 新建 `dashboard/src/ui/DynamicFilterBar.vue`
   - Props: `available`（筛选项定义数组）、`modelValue`（已激活 key 列表）
   - Emits: `update:modelValue`、`remove(key)`
   - 渲染已激活插槽，每项包裹 `flex flex-col gap-1 min-w-0`，label 行带 close 按钮
   - 使用 `SelectMenu` 实现"添加筛选"下拉菜单，触发器按钮显示 `filter-plus` + "添加筛选"文字
   - 在 `dashboard/src/ui/index.ts` 导出该组件（若存在 barrel file 则追加）

## Phase 2 — 概览页面（OverviewView）

3. **引入状态**
   - 在 `<script setup>` 中新增 `const visibleFilters = ref<string[]>([])`

4. **定义筛选项元数据**
   - 创建 `availableFilters` 常量数组，包含 8 项：
     `range`（时间范围）、`granularity`（统计粒度）、`currency`（货币）、`apiKey`（密钥）、`model`（请求模型）、`upstreamModel`（上游模型）、`provider`（渠道）、`project`（项目）

5. **实现 remove 处理**
   - 编写 `onRemoveFilter(key)` 函数，根据 key 将对应 `filters` 字段重置为默认值

6. **替换模板**
   - 将 Controls bar 中原有 8 个 `Field`-like 区块替换为 `<DynamicFilterBar>`
   - 为每个筛选项创建具名插槽，内部放置原有控件（`SegmentedControl`、`Select`、`TimeRangeFilter` 等）
   - 保留右侧刷新按钮在同一行

## Phase 3 — 全局概览页面（AdminOverviewView）

7. **同步改动**
   - 与 Phase 2 完全相同的模式
   - `availableFilters` 7 项：`range`、`granularity`、`currency`、`user`、`model`、`upstreamModel`、`provider`
   - 实现对应的 `onRemoveFilter` 重置逻辑
   - 替换 Controls bar 模板

## Phase 4 — 请求页面（RequestsView）

8. **引入状态**
   - 新增 `const visibleFilters = ref<string[]>([])`

9. **定义筛选项元数据**
   - `availableFilters` 4 项：`type`（类型）、`timeRange`（时间范围）、`requestId`（ID）、`annotation`（标注）

10. **实现 remove 处理**
    - `onRemoveFilter(key)` 重置对应 `filters` 字段：
      - `type` → `'meta'`
      - `requestId` → `''`
      - `annotationKey`、`annotationValue` → `''`
      - `startAt`、`endAt` → `''`

11. **替换模板**
    - 将顶部左侧筛选区替换为 `<DynamicFilterBar>`
    - 4 个具名插槽分别放置 `SegmentedControl`、`TimeRangeFilter`、ID input、标注 input 组合
    - 右侧的清除筛选按钮、计数、刷新按钮保持不动
    - 已有的 traceId banner 逻辑不受影响

## Phase 5 — 验证

12. **类型检查**
    - 运行 `pnpm --dir dashboard type-check`
    - 修复类型错误

13. **Lint**
    - 运行 `pnpm --dir dashboard lint`
    - 修复 lint 错误

14. **构建**
    - 运行 `pnpm --dir dashboard build`
    - 确保无构建错误
