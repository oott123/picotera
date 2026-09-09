# 设计：请求表 usage_raw / tool_usage_raw 列

## 目标

把上游响应里的 `usage` 与 `tool_usage` **原始 JSON 对象**原样落库，并交给两个脚本 hook 读取。

现有的 token 列（`input_tokens` 等五列）和 `tool_usage` 列都是**归一化**产物：前者把四种上游格式压成五个
整数，后者把 `tool_usage` 压成六字段白名单数组并丢弃全零条目。归一化必然丢信息 —— 上游报告的
`input_tokens_details.text_tokens`、`output_tokens_details.reasoning_tokens`、Gemini 的
`promptTokensDetails` 模态明细、Codex 全零的 `image_gen`，现在都无处可查。这两列就是把那份原始事实存下来，
让脚本能按上游真实计价规则算钱，也让排查时不必去翻 MinIO 里的响应 artifact。

## 采集语义

### `usage_raw`

在解析器现有的三个解析调用点上，扫描一张固定的路径表，**先命中先用**：

```go
var usageRawPaths = []string{"usage", "response.usage", "message.usage", "usageMetadata"}
```

四条路径覆盖全部四种上游格式，一份代码即可：

| 上游形态 | 命中路径 |
| --- | --- |
| OpenAI Chat Completions（SSE 末帧 / 非流式） | `usage` |
| OpenAI Responses SSE（`response.completed`） | `response.usage` |
| Anthropic SSE `message_start` | `message.usage` |
| Anthropic SSE `message_delta` / 非流式 body | `usage` |
| Gemini（SSE / JSON 数组流 / 非流式） | `usageMetadata` |

**命中条件是「非空 JSON 对象」**：`usage: null`（Chat Completions 绝大多数增量帧）、`{}`、以及非对象值都
不算命中。命中后把 gjson 的 `Result.Raw` 原文拷一份存进 `ResponseMetrics.UsageRaw`。

**整对象后到覆盖**：后一次命中整个替换前一次，不做逐键合并。所以：

- Gemini 前几帧只带 `trafficType` 的 `usageMetadata` 会被末帧带完整计数的那个覆盖 —— 结果正确。
- OpenAI Responses 只有 `response.completed` 带 usage，只命中一次 —— 结果正确。
- Anthropic 流式最终留下的是 `message_delta` 的 `usage`，`message_start` 里的 `input_tokens` /
  `cache_read_input_tokens` 明细不会出现在 raw 列里。这是「字节级忠实于某一个上游事件」换来的代价，已确认接受。
  归一化的五个 token 列不受影响 —— 它们本来就是逐字段累积的。

不设体积上限。usage 对象天然只有几百字节，且完整响应体本来就已经存进了 artifact；为它加一层截断阈值只会
引入「静默丢数据」这一类更难排查的问题。

### `tool_usage_raw`

`tool_usage` 是 `usage` 的同级兄弟，扫描表因此与 `usageRawPaths` 一一对应地短一截（Anthropic 的
`message.usage` 与 Gemini 的 `usageMetadata` 没有 tool_usage 对应物）。与 `usage_raw` 一样：非空对象才算命中、
末次覆盖。

顺带把既有的 `toolUsageScopes = ["", "response"]` 改成路径表。原先它存的是**作用域**而不是完整路径，理由是
还要在同一层读 `tool_usage` 的另一个兄弟 `tools`（`model` 从那里来）。这个理由用一张成对的路径表就能消解，
换来的是与 `usageRawPaths` 完全同构的扫描写法，也省掉 `scope := result; if path != "" { scope = result.Get(path) }`
这段拐弯：

```go
var toolUsagePaths = []struct{ usage, tools string }{
    {usage: "tool_usage", tools: "tools"},
    {usage: "response.tool_usage", tools: "response.tools"},
}
```

关键差别在于**它不受归一化结果的影响**：

```go
tu := result.Get(p.usage)
if !tu.IsObject() || !hasKeys(tu) { continue }
e.metrics.ToolUsageRaw = []byte(tu.Raw)              // 无条件记录
if entries := normalizeToolUsage(tu, result.Get(p.tools)); len(entries) > 0 {
    e.metrics.ToolUsage = entries                     // 归一化仍然可能一条不剩
}
return
```

Codex 每次响应都带一个四个计数器全为 0 的 `image_gen`，归一化会整条丢弃、`tool_usage` 列写 NULL，而
`tool_usage_raw` 会如实记下它。两列因此可以「一个有值一个 NULL」，这是设计意图，不是 bug。

同时把循环的空对象判断从「是对象即停止扫描」收紧为「非空对象才停止」：一个顶层 `tool_usage: {}` 不再挡住
`response.tool_usage`。两条路径同时出现在一个 payload 里本就不存在，这一改动只是让两列的命中条件保持同构。

## 落库

新增两列，形状与 `tool_usage` 完全一致（可空 JSONB、无索引、无过滤端点）：

```sql
ALTER TABLE request ADD COLUMN usage_raw JSONB;
ALTER TABLE request ADD COLUMN tool_usage_raw JSONB;
```

不建索引：没有任何查询按它们过滤，索引是纯写入开销。不动任何连续聚合。

**写入点与 `tool_usage` 完全相同** —— 只有两条成功路径（`completeGatewaySuccess`、
`unifiedStreamSuccess`），一次提取的结果同时写进 upstream 行和 meta 行。失败路径没有上游响应，
`beforeMetaRequest` 短路的请求根本没有上游，两列保持 NULL。

**两列不经过脚本**。`getToolUsageCost` 可以改写 `tool_usage` 和两个费用列，但 raw 列是「上游说了什么」的
原始事实，脚本改写它就没有意义了。写库时直接取 `ResponseMetrics` 的值。hook 的输出对象由 glue IIFE
重新构造（只含 `toolUsage` / `toolCost` / `toolCostCurrency`），所以脚本即使把 raw 字段原样带回来也会被忽略 ——
只读性是结构上保证的，不需要额外校验分支。

## 脚本暴露

两个 hook 的输入各加两个字段，类型都是 `json.RawMessage`，走 `mustJSON` 内联进 glue IIFE（与
`RequestFinishedView.ToolUsage` 同一手法）—— 不在 jsx 层定义任何 usage 形状的 Go 类型，上游报什么脚本就看到
什么。

```ts
usageRaw: object | null       // 上游原始 usage 对象；未报告时为 null
toolUsageRaw: object | null   // 上游原始 tool_usage 对象；未报告时为 null
```

`null` 是「上游没报告」的表示。这里刻意不学 `toolUsage` 的「恒为数组」约定：`toolUsage` 那么做是为了让脚本能
无脑 `for` 循环，而 raw 是任意形状的对象，`{}` 和「没有」是两回事，用 `null` 区分最直白。

`requestFinished` 的值同样来自 `gatewayFlow.metaFinal` 内存快照（`metaOutcome` 增加两个 `[]byte` 字段，
由 `merge` 按 `SetUsageRaw` / `SetToolUsageRaw` 镜像），不回读数据库。因为 raw 列只在成功路径写入，失败请求
的 `requestFinished` 读到的就是两个 `null`。

## 管理 API 与仪表盘

`RequestView` 增加 `usageRaw` / `toolUsageRaw` 两个 `map[string]any` 字段（OpenAPI 里是
`additionalProperties: true` 的自由对象），解码方式与既有的 `annotations` / `toolUsage` 一致 —— 这两列只由我们
自己的解析器写入，解码失败在正常运行中不可达，失败就留空而不改变转换函数的无错误签名。

仪表盘在请求详情页「日志」之后加一个「用量」tab，展示两份格式化后的原始 JSON。tab 仅在当前选中行至少有一份
raw 数据时出现（与「日志」tab 只对 meta 行出现同理）。概览 tab 里既有的「Token」「工具用量」两个 section 不动 ——
那里是归一化后的可扫读摘要，raw 是给排查用的下钻内容，分层展示符合「渐进密度」原则。

## 不做的事

- 不为两列建索引、不加过滤端点。
- 不改任何连续聚合、不改 `completion_endpoint_path` 视图。
- 不做体积截断。
- 不允许脚本改写这两列。
- 不改归一化列 `tool_usage` 与五个 token 列的既有语义。
