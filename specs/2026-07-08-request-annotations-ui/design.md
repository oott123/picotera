# 请求 Annotations 前端界面 — 设计

## 概述

纯 dashboard 改动，不动后端、不改 openapi。复用既有能力：

- 列表过滤：`GET /api/picotera/requests` 已有 `annotations` 查询参数（URL 编码 JSON 对象，值必须是字符串，`@>` 精确包含，缺省时间窗 30 天）。`listRequests` 已透传该参数（`filters` 直接进 query）。
- 详情读取：`RequestView.annotations`（`{ [key: string]: string }`，可选）已随 `ListRequestsBySpan` 返回，`RequestDetailsContent.vue` 的 `spans`/`selected` 天然带该字段。

因此本改动集中在两处组件与一处类型定义。

## 一、列表页筛选（`RequestsView.vue`）

**类型**：`RequestsFilters`（`src/api/queryKeys.ts`）新增 `annotations?: string`。这样 `queryKeys.requests.list(...)` 会把它纳入 query key（切换筛选自动重新请求），`listRequests` 的 `filters` 也已覆盖。

**UI 状态**：`filters` reactive 新增两个字段 `annotationKey: ''`、`annotationValue: ''`（纯 UI 输入，不进 API filter 对象、不同步 URL）。

**构造 API 参数**：`requestFilters` computed 中，当 `filters.annotationKey` 非空时：

```ts
out.annotations = JSON.stringify({ [filters.annotationKey]: filters.annotationValue })
```

key 为空则不设 `annotations`。不做 trim/大小写等任何规范化（fail fast：原样传，后端严格校验）。

**接入既有筛选机制**：

- `requestFilters` 内联类型对象补 `annotations?: string`。
- 重置分页 + 同步的 `watch(() => [...])` 数组补 `filters.annotationKey`、`filters.annotationValue`。
- `activeFilterCount()`：`filters.annotationKey` 非空时 +1。
- `clearAllFilters()`：置空 `annotationKey`、`annotationValue`。
- requestId 首次置入时清空其它筛选的那个 watch：同样清空 `annotationKey`、`annotationValue`。

**布局**：在 ID 的 `Field` 之后，加一个 `Field label="Annotation"`，内含两个并排文本输入框（占位符"键"/"值"），沿用 ID 输入框的 class。行本身是 `flex-wrap`，超宽自动换行。

**30 天提示**：底部"仅显示最近 30 天"提示的 `v-if` 由 `filters.requestId && !filters.startAt` 改为 `(filters.requestId || filters.annotationKey) && !filters.startAt`——annotations 过滤同样受 30 天缺省窗约束。

## 二、详情页渲染（`RequestDetailsContent.vue`）

在 overview tab 的"基本信息"小节之后，新增一个 annotations 小节：

- `v-if`：`selected.annotations` 存在且至少一个键（`Object.keys(selected.annotations).length`）。
- 小节标题沿用既有样式（`text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]`），文案"Annotations"。
- 内容为 `grid grid-cols-2 gap-2.5`，`v-for` 遍历 `Object.entries(selected.annotations)`，每对渲染 `<Field :label="k" as="div"><span class="font-mono text-sm break-all">{{ v }}</span></Field>`。

`Field` 的 label 已是小号大写灰字（即"小标题"），slot 为值，正好满足"标题为 k、值为 v，像原来那样"。渲染当前选中 span 的注解，切换 meta/upstream 卡片即切换。

## 不做的事

- 不引入第三方库、不加 UI 变体 DSL（沿用 `src/ui/` 原语）。
- 不做多 KV 对筛选（只暴露一对）。
- 不把 annotations 筛选同步进 URL query。
- 不改后端 / SQL / openapi.yaml / 生成的 TS 类型（`annotations` 参数与字段均已存在）。
