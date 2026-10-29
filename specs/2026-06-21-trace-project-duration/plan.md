# 执行计划

## 1. 安装 date-fns

```bash
pnpm --dir dashboard add date-fns
```

## 2. 新增持续时间格式化工具

新建 `dashboard/src/utils/duration.ts`：

```ts
import { intervalToDuration, formatDuration as fnsFormatDuration } from 'date-fns'
import { zhCN } from 'date-fns/locale'

const UNITS = ['years', 'months', 'days', 'hours', 'minutes', 'seconds'] as const

// 返回首次 → 最近的人类可读持续时间；无效或负区间返回 '—'
export function formatDuration(from?: string, to?: string): string {
  if (!from || !to) return '—'
  const start = new Date(from)
  const end = new Date(to)
  if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) return '—'
  if (end.getTime() < start.getTime()) return '—'
  if (end.getTime() === start.getTime()) return '0 秒'

  const duration = intervalToDuration({ start, end })
  // 从最大的非零单位起，最多取两个单位，避免过长
  const nonZero = UNITS.filter((u) => (duration[u] ?? 0) > 0)
  const format = nonZero.slice(0, 2)
  return fnsFormatDuration(duration, { locale: zhCN, format, delimiter: ' ' })
}
```

## 3. 追踪视图 `dashboard/src/views/TracesView.vue`

- 顶部 `import { formatDuration } from '@/utils/duration'`。
- 在 `columns`（约第 53–63 行）的 `{ key: 'firstRequestAt', header: '首次请求' }` 之后插入：
  ```ts
  { key: 'duration', header: '持续时间', align: 'right' },
  ```
- 在模板中 `#cell-firstRequestAt` 插槽之后增加：
  ```vue
  <template #cell-duration="{ row }">
    <span
      class="font-mono tabular-nums"
      :class="formatDuration(row.firstRequestAt, row.lastRequestAt) === '—' ? 'text-ink-faint' : 'text-ink'"
    >
      {{ formatDuration(row.firstRequestAt, row.lastRequestAt) }}
    </span>
  </template>
  ```

## 4. 项目视图 `dashboard/src/views/ProjectsView.vue`

- 顶部 `import { formatDuration } from '@/utils/duration'`。
- 表头（约第 77–78 行）在 `<Th>最近出现</Th>` 之后插入 `<Th>持续时间</Th>`。
- 表体在「最近出现」`<Td>`（约第 97–101 行）之后插入：
  ```vue
  <Td>
    <span class="text-2xs text-ink-muted tabular-nums">{{
      formatDuration(p.firstSeenAt, p.lastSeenAt)
    }}</span>
  </Td>
  ```

## 5. 验证

```bash
pnpm --dir dashboard type-check
pnpm --dir dashboard lint
```

确认两个列表新增「持续时间」列、显示中文持续时间文本，无类型与 lint 错误。
