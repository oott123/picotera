# unified 网关新增 Codex 三端点

## 原始需求

为 unified 网关增加：`/codex/responses`、`/codex/responses/compact`、`/v1/alpha/search` 三个端点；第一个映射到 OpenAI 响应类型的端点；后两个新增一下端点类型（codex 压缩、codex 搜索 v1alpha）并映射。

## 已确认的设计决策

- **挂载路径**：统一带 `/api/unified` 前缀，与现有 5 条 unified 路由同级：
  - `/api/unified/codex/responses`
  - `/api/unified/codex/responses/compact`
  - `/api/unified/v1/alpha/search`

  Codex 侧把 base_url 配成 `https://<host>/api/unified/codex` 即可命中前两条。
- **路由依据**：三个端点的请求体里都带 `model` 字段，全部按 `model` + endpoint_type 解析候选（复用现有 `GetProvidersByEndpointTypesAndModel`，不新增 SQL）。
- **跨格式转换**：新增的两个类型（codex 压缩 / codex 搜索 v1alpha）**纯透传**——候选集合只含该类型本身，请求与响应字节原样转发，不经过 llmbridge。`/api/unified/codex/responses` 与现有 `/api/unified/v1/responses` 行为一致（源格式 OpenAI Responses，可跨格式转换到其它上游）。
- **统计口径**：`codex 压缩` 计入补全端点范围（`completion_endpoint_path` 视图，参与成功率 / 空回复统计）；`codex 搜索 v1alpha` 不计入。
