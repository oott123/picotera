# DeepSeek 搜索端点与搜索模拟支持

## 原始需求

我们现在只有 Exa 的搜索，现在增加 DeepSeek 的搜索 https://api-docs.deepseek.com/zh-cn/api/search ，增加端点类型，并且把搜索模拟也加上 DeepSeek 的支持

## 澄清

- **后端选择**：搜索模拟按渠道（provider）优先级自动在 Exa / DeepSeek 之间选择——在所有绑定了 `exaSearch` 或 `deepseekSearch` 端点的可用渠道中，取优先级最高的那个，其端点类型决定使用哪种搜索后端。不新增配置项。
- **范围**：只做 DeepSeek Search（`POST /v1/tools/search`）。DeepSeek Fetch（`/v1/tools/fetch`）不在本次范围内。

## 参考：DeepSeek Search API

- `POST https://api.deepseek.com/v1/tools/search`，`Authorization: Bearer <key>`，`Content-Type: application/json`。
- 请求体：`query`（必填，1–128 个 Unicode 字符，不能全为空白，支持 `site:` / `-site:` / `-关键词` / `-"短语"` / `"短语"` / `lang:zh,en` / `after:` / `before:` 语法）、`count`（1–50，默认 10）、`ori_question`（1–128 字符，可为 null）、`localization`（IP，默认 `""`）、`need_content`（默认 false）。未知字段被忽略。
- 响应：`{"results": [...]}`，无匹配时为空数组；每项 `rerank_score`、`title`、`url`、`snippet`（约 100–200 字符）、`summary`（约 600–800 字符，适合给模型阅读）、`content`（`need_content=false` 时为空串）、`site_name`、`site_icon`、`date`（RFC 3339，未知为 null）、`authority`。
- 错误体为 OpenAI 兼容格式 `{"error": {"message", "type", "param", "code"}}`；400/401/402/408/413/429/500/504。
- 按次计费，只有 200 且有结果才计费；响应中无用量字段。
