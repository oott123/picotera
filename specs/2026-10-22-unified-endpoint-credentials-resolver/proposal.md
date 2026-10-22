# 原始需求

渠道配置了 openai 类型的端点，端点的鉴权类型设置为 bearerToken。用户通过 unified api 请求 Anthropic 端点，落到了该渠道上。结果鉴权变成了 X-Api-Key。检查为什么

# 补充决定

- 只修复 unified 路由未读取候选真实端点 `credentials_resolver` 的 bug。
- `followRequest` / `unknown` 的语义保持不变：即使发生跨格式转换（如 Anthropic → OpenAI），仍按客户端请求的鉴权位置发送上游凭证；需要固定鉴权方式的端点由管理员显式配置 `bearerToken` 等。
- 不修改 `CLAUDE.md`。
