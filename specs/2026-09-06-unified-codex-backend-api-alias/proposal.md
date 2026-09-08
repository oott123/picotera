# Unified Codex 挂载点增加 `/backend-api/codex` 别名

## 原始需求

修改 /api/unified 的路由匹配，将 `/api/unified/backend-api/codex` 也视为和 `/api/unified/codex` 一样，转发到 codex 路由。

## 已确认的设计决策

- **别名与本体完全等价**：`/api/unified/backend-api/codex/*` 与 `/api/unified/codex/*` 走同一个处理器、同一套后缀归一化（含 `/v1` 段剥离）、同一套候选端点解析。空后缀（裸挂载点、结尾斜杠、裸 `/v1`）同样返回 404 `route_not_found`。
- **记录路径统一归一到本体**：命中别名的请求，`request.endpoint_path` 仍记 `/api/unified/codex` + 归一化后缀。因此 `completion_endpoint_path` 视图、连续聚合、请求列表的端点前缀筛选、端点标签列表全部无需改动，也不需要新迁移。
- **标签列表不新增条目**：既然没有任何请求行会记录别名路径，端点筛选器里仍只保留 `/api/unified/codex` 一条。
