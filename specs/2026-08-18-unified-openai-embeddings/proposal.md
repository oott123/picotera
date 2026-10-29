# 需求：unified 网关 OpenAI Embeddings 端点

给 unified 网关增加 embedding 接口；`pkg/contract/endpoint.go` 端点类型增加 `openaiEmbedding`，中文名叫 "OpenAI 特征提取"。

找一下我们提取 tokens usage 的地方，适配这类 tokens usage：

```json
"usage": {
  "prompt_tokens": 8,
  "total_tokens": 8
}
```

不需要支持流式。

## 澄清（规划阶段确认）

- **路由路径用复数 `/api/unified/v1/embeddings`**，与 OpenAI 官方端点一致，这样 openai-python / openai-node 把 `base_url` 设成 `…/api/unified/v1` 就能直接用。只挂这一条，不额外挂单数路径。
- **`output_tokens` 保持 NULL，不改动响应抽取代码。** 规划阶段实测确认：`ResponseExtractor` 现有的 `setOpenAIInputTokens` 分支已经能从 `usage.prompt_tokens` 提取出 `input_tokens = 8`；`total_tokens` 对 embedding 而言与 `prompt_tokens` 恒等，是冗余字段，不需要读取。embedding 没有输出，`output_tokens` / `ttft_ms` 留空即为事实。
- **embedding 端点不纳入 `completion_endpoint_path` 视图**（成功率 / 空回复 / finish_reason 统计口径），与 `codexSearchV1Alpha` 一致：`output_tokens` 天然为 0，纳入会被空回复统计误判为失败。
