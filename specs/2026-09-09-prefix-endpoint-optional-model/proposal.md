# 前缀端点：缺失模型字段时按无模型路由

对前缀匹配类型的端点，如果模型字段本身在请求中不存在，不要拒绝，而是直接按没有模型进行路由。

## 澄清（与用户确认）

- **适用范围**：所有前缀风格入口。既包括 `endpoint.prefix_match = true` 的路径端点，也包括统一路由的 codex 通配挂载 `/api/unified/codex/*`（含 `/backend-api/codex` 别名），以及将来新增的前缀挂载。固定的统一路由（`/v1/messages`、`/v1/responses`、`/v1/chat/completions`、两个 Gemini 路由、`/v1/embeddings`）不在范围内。
- **判定严格度**：仅当模型字段缺失（body 中 `modelPath` 指向的键不存在）时降级为无模型路由。字段存在但不是非空字符串（空串、`null`、数字、对象等）仍返回 400 `MODEL_NOT_FOUND`。
- **rewriteModel 钩子**：沿用现有无模型端点的不变量——降级为无模型后，`rewriteModel` 返回非空模型名仍视为错误（`failHook`，502）。
