# 设计

两个互相独立的小改动：前端列宽，后端 unified meta path 模板化。无 contract / OpenAPI 变更，无数据库迁移。

## 1. 端点栏最大宽度（前端）

`dashboard/src/views/RequestsView.vue` 中 `cell-endpointPath` 插槽当前无宽度限制：

```vue
<span class="font-mono text-ink-faint">{{ row.endpointPath }}</span>
```

"用户消息"栏用的是 `block max-w-[18rem] truncate`（18rem = 288px）。端点栏取**稍窄的 16rem（256px）**，加 `truncate` 截断并用 `title` 提供 hover 全文：

```vue
<span class="block max-w-[16rem] truncate font-mono text-ink-faint" :title="row.endpointPath">
  {{ row.endpointPath }}
</span>
```

## 2. unified meta path 模板化（后端）

### 现状

`pkg/server/handle_unified_gateway.go` 的 `newUnifiedGatewayFlowConfig` 构造虚拟 endpoint 时用了实际请求 path：

```go
virtualEndpoint := db.Endpoint{
    Name: "(unified)",
    Path: r.URL.Path,   // gemini → /api/unified/v1beta/models/gemini-2.5-flash:generateContent
    ...
}
```

`f.config.Endpoint.Path` 这个值会被两处读取并写入 `request.endpoint_path`：
- meta 行插入：`gateway_flow.go` 的 `insertMetaRequest`（`EndpointPath: f.config.Endpoint.Path`）。
- meta 行 success 更新：`gateway_unified_helpers.go` 的 `unifiedStreamArgsFromSuccess`（`metaEndpointPath: input.Flow.config.Endpoint.Path`）。

对没有 path var 的三条路由（messages / responses / chat completions），`r.URL.Path` 本就等于注册的路由模式，无差别；只有两条 gemini 路由带 `{model}`，于是把具体模型名写进了 `endpoint_path`。

### 方案

`unified_routes.go` 已有 `unifiedRoutePath(srcFormat)`，返回该 format 注册的路由模式（含 `{model}` 占位符），也正是 `handle_label.go` 暴露给端点筛选下拉的同一个值。把虚拟 endpoint 的 path 改为它：

```go
Path: unifiedRoutePath(srcFormat),
```

一处改动同时覆盖 meta 插入与 meta success 更新（两者都读 `config.Endpoint.Path`）。`PathVars: chiURLParams(r)` 不变——上游 URL 仍靠它把具体模型名替换进 provider_endpoint 模式，上游请求不受影响。

### 连带收益

- unified gemini 请求的 `endpoint_path` 现在与端点筛选下拉的值**完全一致**，筛选才真正匹配得上（此前存的是具体 path，按 unified gemini 端点筛选选不出任何请求）。
- overview 的小时连续聚合按 `endpoint_path` 分组，模板化后同一 unified gemini 端点的请求会正确归并，而不是按每个模型名散开。

### path-based 无需改动

path-based 网关全程使用 `endpoint.Path`（操作员配置的模式，含 `{model}`）：`handle_gateway.go` → `newPathGatewayFlowConfig` → meta 插入、`buildPathCandidateSet` 的 sidecar、`markPathHeadersReceived` 的 meta/upstream 更新，全部用模式而非具体 path。已符合预期。
