# Unified 网关 Gemini 上游 URL 的 `{model}` 标记未替换

## 问题

当某个模型仅配置了 Gemini 接口（`geminiGenerateContent` / `geminiStreamGenerateContent`），
而客户端请求的是 unified 接口（如 `/api/unified/v1/messages` 的 Anthropic 源、
或 `/api/unified/v1/chat/completions` 的 OpenAI 源），需要进行跨格式转换时，
发往上游的请求 URL 中的 `{model}` 标记没有被替换为模型名。

上游收到的 URL 形如：

```
https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent
```

（实际经过 URL 编码后为 `.../models/%7Bmodel%7D:generateContent`），导致上游返回错误。

## 任务

1. 写计划。
2. 写测试验证这个问题。
3. 用 `/execute-file-based-plan` 修复。
