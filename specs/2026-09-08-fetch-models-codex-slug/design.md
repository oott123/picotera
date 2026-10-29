# 设计：fetchModels 解析 Codex 模型列表

## 现状

`pkg/server/handle_provider_endpoint.go` 的 `parseModelsResponse` 按固定顺序试四条路径，第一条非空即采用：

1. `$.data[].id`（OpenAI `/v1/models`）
2. `$.data[].name`
3. `$[].id`（裸数组）
4. `$[].name`

四条都不中就落 `422 could not parse models from upstream response`。

Codex 的模型列表形如 `{"models":[{"slug":"gpt-5.5", …}]}`（见 `fixtures/codex-models.json`），四条路径全不命中，运营者在渠道里点「拉取模型」只能拿到 422。

## 解析链

`extractFieldFromData` 里的容器 key `"data"` 提为参数，改名 `extractFieldFromKey(raw any, containerKey, field string)`；`extractFieldFromTopLevel` / `extractStrings` 不变。新链：

| 顺序 | 路径 | 上游形态 |
| --- | --- | --- |
| 1 | `$.data[].id` | OpenAI |
| 2 | `$.data[].name` | |
| 3 | `$.models[].slug` | Codex |
| 4 | `$[].id` | 裸数组 |
| 5 | `$[].name` | |

`data` 与 `models` 两个容器 key 互斥，所以第 3 条插在哪里都不改变任何一种上游的解析结果；放在两条 `data` 之后，是让「对象包裹数组」的形态在链上连续。

`extractStrings` 的既有语义原样复用：非字符串与空串的 `slug` 跳过，按值去重，最终结果字典序排序。fixture 的输出即 `codex-auto-review, gpt-5.4-mini, gpt-5.5, gpt-5.6-luna, gpt-5.6-terra, gpt-reserve`——`visibility: "hide"` 的两个也在内（见 proposal 的澄清）。

拿到名字之后的链路完全不动：`aggregateProviderModels` 与老配置做增删合并、`rewriteProviderModels` 钩子仍可改写、响应结构与前端面板不变。契约无改动，无需重新生成 `openapi.yaml`。

## 响应体读取上限

`handleFetchModels` 用 `io.LimitReader(decoded.Body, 1024*1024)` 读上游响应。Codex 的 `/models` 每个模型内嵌一份完整的 `model_messages.instructions_template`，fixture 6 个模型就有 255 KiB，1 MiB 只够约 24 个模型；超限后截断出的半截 JSON 会以 `422 invalid JSON response` 报出来，指向的是错误的原因。

上限提到 8 MiB。这是本次支持 Codex 格式的直接前提，不是独立优化。
