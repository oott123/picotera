# 携带有效 API key 的未匹配路由：返回 JSON 并记录

把检查 api key 的逻辑提前，在寻找路由的时候，如果携带了有效的 api key，即使是浏览器、404 也永远不返回 SPA html，而是返回一个 json，并将这个请求处理为一条 meta request 记录下来。

## 澄清（规划期决定）

- **"有效"的判定**：`authenticateClient` 完整通过——token 能取到、`api_key` 行存在、key 未禁用、owner 存在且未禁用。无 token、key 不存在、key 或 owner 被禁用都不算有效，维持现有行为（浏览器导航回落 SPA，其余返回 JSON 404 且不记录）。
- **不记录无主的 404**：没有有效 key 就没有 `user_id`，请求列表按 `user_id` 强制过滤，这样的行对任何人都不可见，因此不写。
- **不跑 JS 钩子**：没有匹配到端点就没有 endpoint/model/候选，这条流程在鉴权之后直接终止，不创建 JS session，`rewriteModel` 等钩子（含 `requestFinished`）都不触发。
- **覆盖范围**：catch-all 网关的路由未命中，以及 codex 通配挂载 `/api/unified/codex/*`（含 `/backend-api/codex` 别名）子路径归一化失败的 404。两者都是"寻找路由"阶段的未命中。
