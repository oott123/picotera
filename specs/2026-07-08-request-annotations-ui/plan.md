# 执行计划

## 1. 类型：`dashboard/src/api/queryKeys.ts`

- `RequestsFilters` 增加 `annotations?: string`（放在 `finishReason?` 之后）。

## 2. 列表页：`dashboard/src/views/RequestsView.vue`

### script

1. `filters` reactive 增加 `annotationKey: ''`、`annotationValue: ''`。
2. `requestFilters` computed：
   - 内联类型对象补 `annotations?: string`。
   - 末尾（`return out` 前）加：
     ```ts
     if (filters.annotationKey) {
       out.annotations = JSON.stringify({ [filters.annotationKey]: filters.annotationValue })
     }
     ```
3. 重置分页/同步的 `watch(() => [ ... ], ...)` 依赖数组补 `filters.annotationKey`、`filters.annotationValue`。
4. `activeFilterCount()` 增加 `if (filters.annotationKey) n++`。
5. `clearAllFilters()` 增加 `filters.annotationKey = ''`、`filters.annotationValue = ''`。
6. `watch(() => filters.requestId, ...)` 的清空块补 `filters.annotationKey = ''`、`filters.annotationValue = ''`。

### template

7. 在 ID 的 `<Field label="ID">…</Field>` 之后，新增：
   ```vue
   <Field label="Annotation">
     <div class="flex items-center gap-1.5">
       <input
         v-model="filters.annotationKey"
         type="text"
         placeholder="键"
         class="w-28 rounded-md border border-line bg-surface-0 px-2 py-1.5 text-sm text-ink outline-none focus:border-accent focus-visible:ring-1 focus-visible:ring-accent"
       />
       <input
         v-model="filters.annotationValue"
         type="text"
         placeholder="值"
         class="w-32 rounded-md border border-line bg-surface-0 px-2 py-1.5 text-sm text-ink outline-none focus:border-accent focus-visible:ring-1 focus-visible:ring-accent"
       />
     </div>
   </Field>
   ```
8. 底部 30 天提示：
   ```vue
   <div v-if="(filters.requestId || filters.annotationKey) && !filters.startAt" ...>
   ```

## 3. 详情页：`dashboard/src/components/RequestDetailsContent.vue`

9. 在 overview tab（`detailTab === 'overview'`）的"基本信息" `</section>` 之后、"性能" `<section>` 之前，插入：
   ```vue
   <section
     v-if="selected.annotations && Object.keys(selected.annotations).length"
     class="flex flex-col gap-2.5"
   >
     <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]">Annotations</span>
     <div class="grid grid-cols-2 gap-2.5">
       <Field
         v-for="(v, k) in selected.annotations"
         :key="k"
         :label="k"
         as="div"
       >
         <span class="font-mono text-sm text-ink break-all">{{ v }}</span>
       </Field>
     </div>
   </section>
   ```

## 4. 验证

- `pnpm --dir dashboard type-check`（`RequestsFilters` 新字段、`selected.annotations` 索引访问均需类型通过）。
- `pnpm --dir dashboard lint`。
- 手动：列表页填 key/value 筛选可命中已打标请求；详情页选中带注解的 span 显示 Annotations 小节，每对 KV 为小标题+值。

无需 `sqlc generate` / `mise run openapi` / `generate-openapi`（不触后端与 spec）。
