# 设计

## 背景

请求列表的「端点」列当前直接渲染 `row.endpointPath`（原始路径，等宽字体），可读性差。

`endpointPath` 在请求行上的取值是确定的：

- **路径网关（普通端点）**：meta 行与上游行记录的都是端点表里的 `endpoint.path` 模板（如 `/v1/messages`），与 `endpoint` 表的 `path` 完全一致。
- **统一网关（unified）**：meta 行记录的是 unified 路由模式常量（带 `{model}` 占位符），如 `/api/unified/v1/messages`、`/api/unified/v1beta/models/{model}:generateContent`；unified 的上游行记录的是实际上游端点的表内路径（如 `/v1/messages`）。

因此：

- 普通端点路径可在已有的端点 label 列表里按 `path` 精确查到 `name`。
- unified meta 行的路径以 `/api/unified/` 为前缀，可据此识别为统一网关。

后端 `listEndpointLabels`（`pkg/server/handle_label.go`）返回的 `EndpointLabel{ path, name, endpointType }` 已经包含端点表端点和 unified 路由的合成 label。dashboard 在 `RequestsView.vue` 里已经通过 `listEndpointLabels` 加载了 `endpoints`（用于端点筛选器）。

## 方案

纯前端改动，不涉及后端、contract、OpenAPI。

### 1. 端点名字解析

- **普通端点**：用已加载的 `endpoints`（端点 label 列表）构建 `path → name` 映射，按 `row.endpointPath` 精确查找。查不到时回退显示原始路径（不静默掩盖）。
- **统一网关**：以 `row.endpointPath` 是否以 `/api/unified/` 为前缀判定。前端内置一套按 unified 路由路径键入的可读名字，渲染该名字并在旁边加一个「统一网关」`Tag`。

内置 unified 可读名字（去掉后端 label 里冗余的 “Unified” 前缀，因为旁边有 tag 表达统一网关语义）：

| 路径 | 显示名 |
| --- | --- |
| `/api/unified/v1/messages` | Anthropic Messages |
| `/api/unified/v1/responses` | OpenAI Responses |
| `/api/unified/v1/chat/completions` | OpenAI Chat Completions |
| `/api/unified/v1beta/models/{model}:generateContent` | Gemini 生成内容 |
| `/api/unified/v1beta/models/{model}:streamGenerateContent` | Gemini 流式生成 |

未命中内置表的 unified 路径回退显示原始路径，但仍打「统一网关」tag。

### 2. 放置位置

- 在 `dashboard/src/utils/requestLabels.ts` 增加内置 unified 名字表与两个纯函数 `isUnifiedEndpoint(path)`、`unifiedEndpointName(path)`，与既有的 `finishReasonLabel` 同处。
- 在 `RequestsView.vue` 内构建 `endpointNameByPath` 映射（来自 `endpoints` label）与 `endpointDisplay(path) → { name, unified }`，单元格据此渲染。

### 3. 端点列单元格

把原来等宽、`text-ink-faint`、`title=endpointPath` 的路径单元格，改为：左侧显示解析后的端点名（保留 `title=endpointPath` 以便 hover 看原始路径），unified 时右侧加 `<Tag variant="accent">统一网关</Tag>`。

### 4. 端点筛选器 label（保持一致）

端点列头的 `ColumnFilter` 选项当前 `label` 用的是原始 `path`。为与列显示一致，选项 `label` 改为经同一解析逻辑得到的可读名（unified 走内置名字），`value` 仍为 `path`（筛选条件 `filters.endpointPath` 仍按路径过滤，不变）。

## 不做

- 不改请求详情（`RequestDetailView.vue` / `RequestDetailsContent.vue` 等）。
- 不改后端、SQL、contract、OpenAPI。
