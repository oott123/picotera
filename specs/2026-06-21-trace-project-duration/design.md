# 设计

## 范围

纯前端改动。后端的 `RequestTraceView`（`firstRequestAt` / `lastRequestAt`）与 `ProjectView`（`firstSeenAt` / `lastSeenAt`）已经携带所需的首次/最近时间字段，无需改动后端、SQL、contract 或 OpenAPI。「持续时间」在前端由这两个时间戳相减得到并格式化展示。

## 第三方库

引入 `date-fns`（dashboard 当前未使用任何日期库，时间格式化均为原生 `Date`）。理由：用户明确要求用 date-fns 配合中文语言包。

用到的 API：

- `intervalToDuration({ start, end })` — 把毫秒区间拆成 `{ years, months, days, hours, minutes, seconds }`。
- `formatDuration(duration, { locale, format, delimiter })` — 按中文语言包格式化为「1 小时 30 分钟」之类的文本。
- `locale/zh-CN` — 中文语言包。

按需子路径导入（`date-fns` / `date-fns/locale`），由 Vite 做 tree-shaking。

## 共享格式化函数

在 `src/utils/duration.ts` 新增 `formatDuration(from?: string, to?: string): string`，供两个视图复用：

- 任一入参为空、非法日期，或 `to < from` 时，返回 `'—'`。
- `to === from`（持续 0 秒）返回 `'0 秒'`。
- 否则用 `intervalToDuration` + date-fns 的 `formatDuration` 输出中文文本，并限制显示精度：只取从最大非零单位起的前两个单位（`format: ['years','months','days','hours','minutes','seconds']` 截断），避免出现「3 天 4 小时 12 分 7 秒」这类过长串，保持表格可扫读。

该函数名与现有视图内的 `formatDuration` 语义不同名冲突无关——视图内将直接调用此工具函数。

## 展示

### 追踪（`TracesView.vue`）

在 `columns` 的「首次请求」列之后插入 `{ key: 'duration', header: '持续时间', align: 'right' }`，并提供 `#cell-duration` 插槽：以等宽数字（`font-mono tabular-nums`）渲染 `formatDuration(row.firstRequestAt, row.lastRequestAt)`，无值时显示 `—`（`text-ink-faint`）。`duration` 不是 `RequestTraceView` 的真实字段，仅作为列 key，单元格内容完全由插槽计算，符合 `AutoDataTable` 的用法。

### 项目（`ProjectsView.vue`）

在「最近出现」列之后增加 `<Th>持续时间</Th>` 表头与对应 `<Td>`，渲染 `formatDuration(p.firstSeenAt, p.lastSeenAt)`，样式与相邻时间列一致（`text-2xs text-ink-muted tabular-nums`）。
