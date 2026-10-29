# 端点绑定上游 URL 自动匹配回退

## 需求

在渠道-端点绑定面板（ProviderEndpointsPanel）中，当用户选中一个端点时，前端会自动根据已有的绑定推算出一个建议的上游 URL。

当前的逻辑是：在已有绑定中找到 `upstreamUrl` 以 `endpointPath` 结尾的匹配项，取最短匹配，用其前缀拼上新的 `endpointPath`。

问题是：当渠道没有任何已有绑定（或现有绑定都无法提供匹配前缀）时，建议的上游 URL 会直接回退为端点路径本身（如 `chat/completions`），而不是一个完整的可访问 URL。

## 改进

当已有绑定无法匹配出前缀时，再检查该渠道的 `modelsEndpointUrl`（模型列表 URL）：

1. 如果 `modelsEndpointUrl` 以 `/v1/models` 结尾，则移除该后缀，剩余部分作为前缀，拼上 `/` + 新的 `endpointPath`。
2. 否则，如果 `modelsEndpointUrl` 以 `/models` 结尾，则移除该后缀，剩余部分作为前缀，拼上 `/` + 新的 `endpointPath`。
3. 如果以上都不满足，仍按现有行为回退为 `endpointPath`。

例如：
- `modelsEndpointUrl = "https://api.openai.com/v1/models"`，选中端点 `v1/chat/completions` → 建议 URL 为 `"https://api.openai.com/v1/chat/completions"`。
- `modelsEndpointUrl = "https://some.provider.com/models"`，选中端点 `chat/completions` → 建议 URL 为 `"https://some.provider.com/chat/completions"`。
