# Merged Model Column Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge the "模型" and "上游模型" columns in the requests table into one column with a two-line cell and side-by-side split header with two ColumnFilters.

**Architecture:** All changes are in `RequestsView.vue`. Remove the `upstreamModel` column, replace the `#header-model` slot with a flex container holding two ColumnFilters (实际模型 left, 请求模型 right), and replace both `#cell-model` and `#cell-upstreamModel` slots with a merged two-line cell.

**Tech Stack:** Vue 3, Tailwind CSS v4, existing UI primitives (`ColumnFilter`, `AutoDataTable`)

---

### Task 1: Merge model columns in RequestsView

**Goal:** Replace the two separate model/upstreamModel columns with a single merged column.

**Files:**
- Modify: `dashboard/src/views/RequestsView.vue`

**Acceptance Criteria:**
- [ ] Only one "model" column exists in the table (upstreamModel column removed)
- [ ] Column header shows two ColumnFilters side-by-side: "实际模型" (left) and "请求模型" (right)
- [ ] Left ColumnFilter filters `filters.upstreamModel`, right filters `filters.model`
- [ ] A vertical border separates the two header halves
- [ ] Header highlights when either filter is active
- [ ] Cell shows upstreamModel on top line (font-mono)
- [ ] Cell shows model below in small faint text only when model !== upstreamModel
- [ ] When upstreamModel is empty, model shows alone on top line
- [ ] When both are empty, shows "—"
- [ ] Filtering still works correctly (API params unchanged)
- [ ] `#header-upstreamModel` and `#cell-upstreamModel` slots are removed

**Verify:** `pnpm --dir dashboard type-check` passes, then `mise run web` and visually confirm in browser

**Steps:**

- [ ] **Step 1: Remove upstreamModel column from columns array**

In the `columns` computed (around line 138-147), remove the `upstreamModel` entry. Update the `model` column's `headerClass` to highlight when either filter is active:

```typescript
{
  key: 'model',
  header: '模型',
  headerClass: (filters.model || filters.upstreamModel) ? 'shadow-[inset_0_-2px_0_var(--color-accent)]' : '',
},
```

Remove these lines (the upstreamModel column):
```typescript
{
  key: 'upstreamModel',
  header: '上游模型',
  headerClass: filters.upstreamModel ? 'shadow-[inset_0_-2px_0_var(--color-accent)]' : '',
},
```

- [ ] **Step 2: Replace #header-model slot with split header**

Replace the current `#header-model` template (lines 270-276) with a flex container holding two ColumnFilters:

```vue
<template #header-model>
  <div class="flex -my-1.5 divide-x divide-surface-200">
    <div class="flex-1 pr-2">
      <ColumnFilter
        v-model="filters.upstreamModel"
        label="实际模型"
        :options="upstreamModelOptions"
        placeholder="过滤实际模型…"
      />
    </div>
    <div class="flex-1 pl-2">
      <ColumnFilter
        v-model="filters.model"
        label="请求模型"
        :options="modelOptions"
        placeholder="过滤请求模型…"
      />
    </div>
  </div>
</template>
```

- [ ] **Step 3: Remove #header-upstreamModel slot**

Delete the `#header-upstreamModel` template block (lines 278-285).

- [ ] **Step 4: Replace cell slots with merged two-line cell**

Replace both `#cell-model` and `#cell-upstreamModel` templates (lines 302-309) with a single merged cell:

```vue
<template #cell-model="{ row }">
  <div class="flex flex-col leading-tight">
    <span v-if="row.upstreamModel" class="font-mono text-ink">{{ row.upstreamModel }}</span>
    <span v-else-if="row.model" class="font-mono text-ink">{{ row.model }}</span>
    <span v-else class="text-ink-faint">—</span>
    <span
      v-if="row.model && row.upstreamModel && row.model !== row.upstreamModel"
      class="font-mono text-2xs text-ink-faint"
    >{{ row.model }}</span>
  </div>
</template>
```

Delete the `#cell-upstreamModel` template entirely.

- [ ] **Step 5: Run type-check**

Run: `pnpm --dir dashboard type-check`
Expected: PASS (no type errors)

- [ ] **Step 6: Visually verify in browser**

Run: `mise run web`
Open the requests page and confirm:
- Single column with two filter dropdowns side-by-side
- Cells show upstreamModel on top, model below (only when different)
- Both filters work independently
- Active filter highlight shows on the column header
