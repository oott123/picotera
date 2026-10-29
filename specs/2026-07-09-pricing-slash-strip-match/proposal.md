# Proposal: Pricing Match with Slash Stripping

在匹配价格的时候，检索 `pricing.json`，对 `pricing.json` 里面的模型列表里或者输入里面，带斜线的（例如 `openai/gpt-5.5`），尝试移除最后一个斜线和前面的内容，再重新计算编辑距离。取所有编辑距离结果中最低者作为最终编辑距离返回给前端。
