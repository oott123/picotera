# 端点测试功能

增加测试功能。用户可以编辑模型、系统提示词、最大 tokens、用户消息、选择是否流式，然后发送真实请求进行测试。

测试分为两种：

## 1. 短路测试

直接选择上游（provider）、端点（provider_endpoint），后端直接向上游发送测试请求，不经过任何脚本、前置/后置处理，也不记录日志（不写 request 行、不写 artifact），只在界面上进行反馈。需要后端新增一个接口来适配这类测试。

## 2. 网关测试

用户选择一个 API key 和一个端点（可以是 unified 端点或者网关端点），真实发送完整请求做测试。这种测试就是完整正常发的，走完整网关管线（脚本、前置/后置处理、日志全部生效），不需要新增后端接口。

## 补充确认（规划期间澄清）

- **请求体构建位置**：两种测试的请求体均由前端统一按目标格式构建，作为单一来源。短路测试把构建好的 body 连同 provider + endpoint 传给后端薄代理；网关测试前端直接 fetch 发送。
- **短路流式反馈**：短路测试接口用原始 chi handler 实时透传上游 SSE 给 dashboard，复用 `useSSEParser` 实时展示。
- **界面入口**：新建独立视图 `/test`，内含「短路测试」「网关测试」两种模式。
- **原始 body 编辑**：除结构化字段外，提供高级原始 JSON body 编辑器，可覆盖由结构化字段生成的 body。
- 支持的格式：`anthropicMessages`、`openaiChatCompletions`、`openaiResponses`、`geminiGenerateContent` / `geminiStreamGenerateContent`。
