# Merged Model Column Design

## Summary

Merge the "模型" and "上游模型" columns in the requests table into a single column with a two-line cell layout and a side-by-side split header containing two independent ColumnFilters.

## Current State

Two separate columns:
- **模型** (`model`) — shows the user-requested model name
- **上游模型** (`upstreamModel`) — shows the actual upstream model name (dimmer text)

Each has its own ColumnFilter in the header.

## New Design

### Column Structure

Remove the `upstreamModel` column. Keep a single column (key: `model`) that represents both fields.

### Header

The `#header-model` slot renders a flex container with two ColumnFilters side-by-side:

- **Left — 实际模型**: `v-model="filters.upstreamModel"`, options from `upstreamModelOptions`
- **Right — 请求模型**: `v-model="filters.model"`, options from `modelOptions`

A vertical divider separates the two halves. Active filter highlight (`shadow-[inset_0_-2px_0_var(--color-accent)]`) applies when either filter is active.

### Cell

The `#cell-model` slot renders:

- **Top line (normal weight, font-mono)**: `upstreamModel` (实际模型)
- **Bottom line (small, faint, font-mono)**: `model` (请求模型) — only shown when `model !== upstreamModel`
- If `upstreamModel` is empty, show `model` alone on top line
- If both are empty, show "—"

### Filter Logic

- API query params remain unchanged: `filters.model` → `query.model`, `filters.upstreamModel` → `query.upstreamModel`
- `activeFilterCount()` still counts both filters
- `clearAllFilters()` still resets both

### Files Changed

- `dashboard/src/views/RequestsView.vue` — all changes are here:
  - Remove `upstreamModel` from `columns` array
  - Replace `#header-model` slot with two ColumnFilters in flex layout
  - Remove `#header-upstreamModel` slot
  - Replace `#cell-model` and `#cell-upstreamModel` slots with merged two-line cell
  - Update `headerClass` on model column to highlight when either filter is active

### What Does NOT Change

- `AutoDataTable`, `ColumnFilter`, `DataTable`, `Th`, `Td` — no changes
- API types or query parameters — no changes
- `RequestDetailsPanel` — no changes (though showing upstreamModel there could be a follow-up)
- Filter state shape (`filters.model`, `filters.upstreamModel`) — no changes
