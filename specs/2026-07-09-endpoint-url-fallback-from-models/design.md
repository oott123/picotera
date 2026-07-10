# 设计文档

## 概述

本改动仅涉及前端代码：在 `ProviderEndpointsPanel.vue` 中增强 `guessUpstreamUrl` 的 fallback 逻辑，使其在已有绑定无法提供前缀时，能够从渠道的 `modelsEndpointUrl` 中推断出合理的基础 URL。

## 组件变更

### ProvidersView.vue

`toggleBindings(p: ProviderView)` 向 `ProviderEndpointsPanel` 多传递一个可选 prop：`modelsEndpointUrl`（取自 `p.modelsEndpointUrl`）。

### ProviderEndpointsPanel.vue

1. **Props 扩展**：增加 `modelsEndpointUrl?: string`。
2. **`guessUpstreamUrl` 增强**：
   - 保持现有逻辑不变（已有 binding 匹配 → 取最短匹配 → 拼接前缀）。
   - 当现有 binding 无匹配时，进入 fallback 分支：
     - 若 `modelsEndpointUrl` 非空且以 `/v1/models` 结尾，移除该后缀，结果 + `'/'` + `endpointPath`。
     - 否则，若 `modelsEndpointUrl` 非空且以 `/models` 结尾，移除该后缀，结果 + `'/'` + `endpointPath`。
     - 顺序很重要：`/v1/models` 必须在 `/models` 之前检查，否则 `https://api.openai.com/v1/models` 会被错误地截断为 `https://api.openai.com/v1/`。
   - 若 fallback 也无法匹配，仍返回 `endpointPath`（与现有行为一致）。

## 无变更范围

- 后端 API、数据库 schema、OpenAPI 规范均无需修改。
- `ProviderView` 已包含 `modelsEndpointUrl`，dashboard 类型系统天然支持。
