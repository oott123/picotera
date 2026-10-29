# Codex 端点类型与端点前缀匹配

## 原始需求

移除 “Codex 压缩”、“Codex 搜索 v1alpha” 两个 codex 专属端点类型；改为添加一个 “codex” 类型，并为端点添加“前缀匹配”功能。比如 `/api/codex` 这个 API 端点，设置为 codex 类型之后（codex 类型必须勾选前缀匹配），请求 `/api/codex/responses` 将转发到 `{端点URL}/responses` 这个路径。对 unified 请求，如果请求 `/api/unified/codex` 前缀，则也是选择 codex 类型的端点转发。特别地，如果前缀是 `/api/unified/codex/v1` ，则移除 `/v1` 后转发到 codex 类型。

## 已确认的设计决策

- **`/api/unified/codex/responses` 保留跨格式桥接**：该子路径仍以 OpenAI Responses 为源格式，可桥接到 Anthropic / Gemini / ChatCompletions 上游；codex 类型上游同样参与该子路径的候选（字节透传）。`/api/unified/codex` 前缀下的其它子路径（`/responses/compact`、`/alpha/search` 等）只匹配 codex 类型上游，纯透传。
- **旧数据迁移**：数据库里 `endpoint_type` 为 11（codexCompact）/ 12（codexSearchV1Alpha）的端点行，迁移时改为 `general`（1）。代码里 11、12 保留为占位常量，注明已废弃、禁止复用；新的 `codex` 类型取 14。
- **请求行 `endpoint_path` 记录粒度**：前缀匹配命中的请求记「前缀 + 归一化后缀」，例如 `/api/codex/responses`、`/api/unified/codex/responses`（`/v1` 已被剥离）。据此 `completion_endpoint_path` 视图可以继续把 `/alpha/search` 排除在成功率 / 空回复统计之外。
