# 缺失 Content-Type 时的 SSE 探测

在解析 usage 、还有前端尝试渲染 events 的时候，如果 Response headers 里没有 Content-Type 的话，也尝试做 SSE 探测，如果解析成功就当是 SSE 处理，失败才放弃。本地测试数据库里有个 dafsmhos9a202ci1jl60 请求，这个响应就属于这种情况，预期能探测到 usage tokens ，前端也能出现“Events”tab。

## 澄清（规划期补充）

- **后端「聚合」artifact 一并覆盖**：`llmbridge.StreamAggregationKind` 这一侧在 Content-Type 缺失且探测到 SSE 时也按 SSE 处理，让无 Content-Type 的 anthropic / openai / gemini 上游同样能生成聚合结果。
- **codex 端点也要出聚合结果**（执行期修正，原先写的「codex 不做聚合、不受影响」是错的）：实测 `dag1qqos9a24hbsi4dc0` 走的是**路径网关**上的 codex 前缀端点（`endpoint_path = /backend-api/codex/responses`），tokens 已正常，但 `responseAggregationFormat` 对 `EndpointType_Codex` 直接返回 `false`，`buildAggregatedArtifact` 根本没被调用，所以探测再准也出不了聚合。需要让路径网关对齐 `codexUnifiedRoute` 的规则：只有 `/responses` 子路径按 `FormatOpenAIResponses` 聚合，其余子路径（`/responses/compact`、`/alpha/search` 等）不聚合。
- **验证方式**：用该响应体写 Go 单测验证能解析出 input/output tokens；前端改动天然对历史 artifact 生效，打开 dafsmhos9a202ci1jl60 即可看到 Events tab。数据库里这条旧行的 token 列保持为空，不做历史回填；聚合同理只对新请求生效。
