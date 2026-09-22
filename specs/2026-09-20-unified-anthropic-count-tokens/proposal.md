# 需求：unified 网关支持 Anthropic Messages 的 count_tokens

为 `/api/unified/v1/messages/count_tokens` 加入支持。

## 澄清（规划阶段与用户确认）

- **路由路径**：`/api/unified/v1/messages/count_tokens`，与现有 unified 路由同前缀（Anthropic SDK 把 `base_url` 指向 `…/api/unified` 时正好命中）。
- **上游候选只认 `anthropicCountTokens`（5）**：纯透传，不引入「给 `anthropicMessages` 端点的 upstream_url 追加 `/count_tokens`」这类按类型推断 URL 的隐含约定。运营者照常单独建一条计数端点行、绑定渠道，并在 `provider_models` 里让模型可见。
- **计数结果不入库**：上游返回的 `{"input_tokens": N}` 不写入请求行，保持与路径网关现有 count_tokens 处理一致 —— 响应没有 `usage` 对象，抽取器不产出任何 token 字段；顺带避免在免费计数端点上凭空产生 `model_cost`。

## 端到端验证时的补充事实（执行阶段）

- **候选集合只认 `anthropicCountTokens` 的验证前提**：`provider_models` 条目若不带 `endpoints` 白名单，该模型对所有已绑定端点可见（既有行为），因此同一个渠道上的模型会同时命中计数端点与 messages 端点。要验证「同模型打计数路由必须 404」这一条，需把该模型的条目白名单到具体端点 path；不带白名单时返回 200 属配置结果，不是路由缺陷。