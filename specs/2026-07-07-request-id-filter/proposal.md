# 请求ID 筛选器

为请求列表界面（RequestsView）增加一个输入框，放在「结束时间」右边，标签为「请求ID」。

填入内容后，扫描请求表的以下 4 个栏位，精确匹配（exact match），找到所有满足任一栏位的条目并显示：

1. `id`（请求ID）
2. `parent_span_id`（parent span id）
3. `external_request_id`（external request id）
4. `external_response_id`（external response id）

## 设计澄清

- 该筛选器与其他筛选器（类型、时间范围、渠道等）以 AND 叠加，行为与现有 `traceId` 筛选器一致。
- 当用户从「无」到「有」填入请求ID 时（即从空变为非空），自动将「类型」切换为「全部」并清空其他筛选器（渠道、端点、模型、上游模型、traceId、项目、时间范围、空响应、完成原因），以避免默认 `meta` 类型隐藏携带 external ID 的上游请求。此后用户修改该输入框（非空→非空）或清空它（非空→空）不再触发自动切换。
- 该搜索仅检索最近 30 天的请求：前端若传入 `startAt` 则尊重它（不钳制）；前端未传 `startAt`（空）时后端默认 `created_at >= now() - 30 天`。索引侧无需做时间界定的部分索引——`request` 是 TimescaleDB hypertable，查询携带 `created_at` 谓词后由 chunk exclusion 自动只扫描对应时间区间的数据块（详见 design.md）。
- 该筛选器支持 URL 查询参数持久化（与 `traceId`、`projectId`、`startAt`、`endAt` 一致），刷新页面或分享链接时保留。
