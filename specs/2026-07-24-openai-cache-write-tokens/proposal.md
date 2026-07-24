# OpenAI 缓存写入 tokens 提取

为 OpenAI Responses 和 Chat Completions 的 tokens 提取逻辑，增加缓存写入 tokens 的提取：

- Chat Completions：`usage.prompt_tokens_details.cache_write_tokens`
- Responses：`usage.input_tokens_details.cache_write_tokens`

作为缓存写入 tokens（`CacheWriteTokens`）计算。`cache_write_tokens` 是 OpenAI 官方新增的用量字段；当响应未返回该字段时，`CacheWriteTokens` 保持不设置（`nil`）。

输入 tokens 除了减去缓存读取的 tokens（`cached_tokens`），也要减去这部分缓存写入的 tokens。
