# 执行计划

## 1. `dashboard/src/utils/requestLabels.ts`

新增内置 unified 名字表与辅助函数：

```ts
const UNIFIED_ENDPOINT_NAMES: Record<string, string> = {
  '/api/unified/v1/messages': 'Anthropic Messages',
  '/api/unified/v1/responses': 'OpenAI Responses',
  '/api/unified/v1/chat/completions': 'OpenAI Chat Completions',
  '/api/unified/v1beta/models/{model}:generateContent': 'Gemini 生成内容',
  '/api/unified/v1beta/models/{model}:streamGenerateContent': 'Gemini 流式生成',
}

export function isUnifiedEndpoint(path: string | undefined | null): boolean {
  return !!path && path.startsWith('/api/unified/')
}

export function unifiedEndpointName(path: string): string {
  return UNIFIED_ENDPOINT_NAMES[path] ?? path
}
```

## 2. `dashboard/src/views/RequestsView.vue`

### 2.1 引入辅助函数

`import { finishReasonLabel } from '@/utils/requestLabels'` 改为同时引入
`isUnifiedEndpoint`、`unifiedEndpointName`。

### 2.2 名字解析（script 部分）

新增：

```ts
const endpointNameByPath = computed(() => {
  const m = new Map<string, string>()
  for (const e of endpoints.value) m.set(e.path, e.name)
  return m
})

function endpointDisplay(path: string | undefined | null): { name: string; unified: boolean } {
  if (!path) return { name: '—', unified: false }
  if (isUnifiedEndpoint(path)) return { name: unifiedEndpointName(path), unified: true }
  return { name: endpointNameByPath.value.get(path) || path, unified: false }
}
```

### 2.3 端点列单元格（template）

将 `#cell-endpointPath` 模板替换为名字 + 可选 tag（每行只解析一次）：

```html
<template #cell-endpointPath="{ row }">
  <div class="flex items-center gap-1.5 min-w-0">
    <span class="truncate text-ink" :title="row.endpointPath">{{
      endpointDisplay(row.endpointPath).name
    }}</span>
    <Tag v-if="endpointDisplay(row.endpointPath).unified" variant="accent">统一网关</Tag>
  </div>
</template>
```

（`Tag` 已在该文件 import；variant `accent` 已有。）

### 2.4 端点筛选器选项 label 与列显示一致

`endpointOptions` 改为用解析后的名字作为 `label`，`value` 仍为 `path`：

```ts
const endpointOptions = computed<ColumnFilterOption<string>[]>(() =>
  endpoints.value.map((e) => ({ value: e.path, label: endpointDisplay(e.path).name })),
)
```

## 3. 校验

- `pnpm --dir dashboard type-check`
- `pnpm --dir dashboard lint`

## 不触碰

- 后端、SQL、contract、OpenAPI 均不变（端点 label 已含 `name` 字段与 unified 合成 label）。
- 请求详情视图不改。
