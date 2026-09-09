# 设计：请求表 tool_usage / tool_cost 列

## 目标

在 `request` hypertable 上记录上游响应报告的**工具用量**（web search 次数、image gen 的 token 数等），
与既有的模型 token 列并列，并预留工具费用列。

## 数据形状

### 上游输入

上游给出一个 `tool_usage` 对象，key 是工具名，value 是该工具的用量对象。它是该格式 `usage` 对象的
**同级兄弟**（OpenAI Responses 实测样本）：

```json
{
  "type": "response.completed",
  "response": {
    "usage": { "input_tokens": 100, "output_tokens": 50 },
    "tool_usage": {
      "image_gen": { "input_tokens": 222, "output_tokens": 1630, "total_tokens": 1852, "input_tokens_details": {} },
      "web_search": { "num_requests": 1 }
    }
  }
}
```

层级随格式走：Chat Completions / Anthropic / Gemini / 非流式 body 的 `usage` 在顶层，`tool_usage`
也在顶层；Responses SSE 的两者一起被包在 `response` 信封里。所以本设计不把 `tool_usage` 并进 `usage`
再交给现有的 `setOpenAIInputTokens` 等函数（那要为每种格式各写一份合并逻辑），而是在同一批解析调用点上
扫描一张固定的候选**作用域**表 `toolUsageScopes = ["", "response"]`，先命中先用 —— 两个作用域覆盖
全部四种上游格式，一份代码即可。存作用域而不是完整路径，是因为还要在同一层里读 `tool_usage` 的另一个
兄弟 `tools`（见下）。

`tools` 是客户端声明的工具列表，也在同一层：

```json
"tools": [
  { "type": "web_search", "search_context_size": "medium" },
  { "type": "image_generation", "model": "gpt-image-2-codex", "size": "auto" }
]
```

### 落库形状

归一化成数组，key 变成 `name`，只保留四个白名单整数字段，外加从 `tools` 匹配来的 `model`：

```json
[
  { "name": "image_gen", "model": "gpt-image-2-codex", "inputTokens": 222, "outputTokens": 1630 },
  { "name": "web_search", "numRequests": 1 }
]
```

- 数组顺序 = 上游对象的字面顺序（`gjson.ForEach` 保序），可复现。
- 白名单外的字段（`total_tokens`、`*_details`）丢弃。
- **0 等同于没报**：值为 0 的白名单字段整个 omit。上游列举的是它支持的工具而非实际跑过的
  （Codex 每次都带一条全 0 的 `image_gen`），0 不承载信息。
- **四个字段全被 omit 的条目整条丢弃** —— 全 0、或本来就是空对象 `{}`，都表示这个工具没跑过。
- `tool_usage` 不是对象（数组 / 字符串 / null）→ 整体忽略，列写 NULL。
- 某个 value 不是对象 → 跳过该条目，其余条目照常。
- 白名单字段存在但不是 JSON number → 该字段视为缺席（不做字符串转数字的宽容解析）。
- `model` 取自同层 `tools` 数组里 `type` 匹配的那一项。匹配走 `toolUsageToolType` 别名表 ——
  实测 `tool_usage` 的 key 与 `tools[].type` 的词表并不一致（用量记在 `image_gen`，工具声明为
  `image_generation`），表里没有的名字按字面相等匹配。同 type 出现多次时取第一条。
  实测六种 `tools[].type` 里**只有 `image_generation` 带 `model`**，所以 `model` 缺失是常态：
  `tools` 缺失 / 不是数组 / 该 type 未声明 / `model` 不是字符串，一律留空。
- 判断「是否全 0」只看四个计数器，**不看 `model`** —— 客户端挂载了工具但没用过，依然整条丢弃。

因为「报了 0」与「没报」不再需要区分，四个字段用 `int64 + omitempty` 而非 `*int64` —— 指针在这里是
多余的复杂度。字段名用 camelCase 而非上游的 snake_case，使 JSONB 列、REST API、JS hook 三层形状完全一致，
任何一层都不需要大小写转换。这是本仓库既有约定（sqlc `emit_interface` camelCase JSON tag、所有 contract
view、所有 jsx view）。

## 数据库

新迁移 `db/migrations/049_request_tool_usage.sql`：

```sql
-- +goose Up
ALTER TABLE request ADD COLUMN tool_usage JSONB;
ALTER TABLE request ADD COLUMN tool_cost NUMERIC(20, 6);
ALTER TABLE request ADD COLUMN tool_cost_currency TEXT;

-- +goose Down
ALTER TABLE request DROP COLUMN IF EXISTS tool_cost_currency;
ALTER TABLE request DROP COLUMN IF EXISTS tool_cost;
ALTER TABLE request DROP COLUMN IF EXISTS tool_usage;
```

- 三列均可空，插入时不写（`InsertRequest` 不动），只由成功路径的 UPDATE 回填。
- **不建索引。** `annotations` 建 GIN 是因为它有 `@>` 过滤端点；`tool_usage` 本次没有任何查询/过滤需求，
  加索引只是白白付出写入成本。
- 三个连续聚合（`request_overview_bucketed` / `request_speed_bucketed` / `request_outcome_bucketed`）
  都是显式列清单的物化视图，加列不影响它们，无需重建。迁移 044 给同一张 hypertable 加 `annotations`
  已是同样的先例。
- `tool_cost` / `tool_cost_currency` 的类型与 `model_cost` / `model_cost_currency` 完全一致
  （`NUMERIC(20,6)` / `TEXT`），后续接计费时可直接复用 `costsFor` 的返回形状。

## 解析层（`pkg/server/response_extractor.go`）

新增结果类型与抽取方法：

```go
// ToolUsageEntry is one upstream tool's usage, normalized from the response's
// tool_usage object. Only non-zero counters are kept.
type ToolUsageEntry struct {
    Name         string `json:"name"`
    Model        string `json:"model,omitempty"`
    NumRequests  int64  `json:"numRequests,omitempty"`
    InputTokens  int64  `json:"inputTokens,omitempty"`
    OutputTokens int64  `json:"outputTokens,omitempty"`
    NumImages    int64  `json:"numImages,omitempty"`
}
```

`ResponseMetrics` 增加 `ToolUsage []ToolUsageEntry`。

`extractToolUsage(result gjson.Result)` 按 `toolUsageScopes` 找到含 `tool_usage` 的作用域，把它和同层的
`tools` 一起交给 `normalizeToolUsage`，后者按上面的规则产出条目数组（模型名由 `toolDeclaredModel` 查得）。
**最后一次非空出现覆盖之前的值**（last wins），与 `usage` 字段既有的覆盖语义一致。
这不是理论上的保险：Responses 流在 `response.created` / `in_progress` / `completed` 三个事件里
**都带** `tool_usage`，前两次通常全 0，只有最后一次是最终计数 —— last-wins 正是让它收敛的机制。
空数组不覆盖已有值（前两个事件因为全 0 被整体丢弃，本来就不会覆盖）。

调用点（正好是现有 usage 抽取发生的三处）：

| 调用点 | 覆盖场景 |
|---|---|
| `processSSEEvent` | 全部四种 SSE 格式（两个候选作用域覆盖） |
| `extractJSONMetrics` | 非流式 JSON body |
| `processGeminiArrayElement` | Gemini JSON 数组流 |

## 写库层

`metricsToPG` 已经返回 6 个值，再加一个返回值会让签名难读；改为新增独立辅助函数：

```go
// toolUsageJSON marshals extracted tool usage for the tool_usage JSONB column.
// Returns nil when nothing was extracted, so the column is written as SQL NULL.
func toolUsageJSON(m ResponseMetrics) []byte
```

`db/queries/request.sql`：

- `UpdateRequest` 增加三个 `CASE WHEN set_*` 分支：`tool_usage`（`::jsonb`）、`tool_cost`（`::numeric`）、
  `tool_cost_currency`（`::text`），与既有列同构。
- `ListRequests`、`ListRequestsBySpan` 的 SELECT 列表增加 `r.tool_usage, r.tool_cost, r.tool_cost_currency`。
  `GetRequest` 用 `r.*`，自动带上。
- `ListRequestTraces` 按币种聚合 `model_cost`，本次**不动** —— 工具费用还没有写入方，聚合它只会得到全 NULL。

`pkg/server/request_update.go` 增加三个 setter：`ToolUsage([]byte)`、`ToolCost(pgtype.Numeric)`、
`ToolCostCurrency(pgtype.Text)`。后两个本次没有调用方（按需求，管道先打通）。

调用 `.ToolUsage(...)` 的位置 = 现在写 token 的两条成功路径，各自的 upstream 行与 meta 行共 4 处：

- `gateway_flow_success.go` → `completeGatewaySuccess`
- `gateway_unified_helpers.go` → `unifiedStreamSuccess`

失败路径不写（没有可解析的成功响应）；脚本快速响应路径（`gateway_flow_script_response.go`）不写
（没有上游响应，且脚本 `tokens` 契约本次不扩展）。

## JS hook 层（`pkg/jsx`）

`RequestFinishedView` 增加：

```go
ToolUsage json.RawMessage `json:"toolUsage"`
```

用 `json.RawMessage` 而非再定义一份 `jsx.ToolUsageEntry`：`metaOutcome` 里存的就是要写进 JSONB 的原始字节，
`mustJSON(input)` 会把它原样内联进 hook 的初始化表达式，脚本拿到的是一个真正的 JS 数组。这样 jsx 层零新增
类型、零解码开销，且与落库内容保证逐字一致。

`gateway_flow_finish.go` 的 `metaOutcome` 增加 `toolUsage []byte`，在 `merge` 里镜像 `p.SetToolUsage`。
`runRequestFinished` 在 `toolUsage` 为空时传 `[]`，让脚本可以无条件 `view.toolUsage.forEach(...)`。

`toolCost` / `toolCostCurrency` 本次不进 `metaOutcome`、不进 `RequestFinishedView` —— 它们永远是 NULL，
镜像它们等于引入读不到值的死状态；等接计费时与 view 字段一起加。

## API 层（`pkg/contract/request.go`）

新增与 `server.ToolUsageEntry` 形状逐字段相同的 view 类型（contract 不能反向 import server）：

```go
type ToolUsageEntryView struct {
    Name         string `json:"name"`
    Model        string `json:"model,omitempty"`
    NumRequests  int64  `json:"numRequests,omitempty"`
    InputTokens  int64  `json:"inputTokens,omitempty"`
    OutputTokens int64  `json:"outputTokens,omitempty"`
    NumImages    int64  `json:"numImages,omitempty"`
}
```

`RequestView` 增加 `toolUsage` / `toolCost` / `toolCostCurrency`；`requestLike` 增加对应的
`[]byte` / `pgtype.Numeric` / `pgtype.Text` 字段；三个 `To*View` 函数透传。

`toRequestView` 里解码 `tool_usage` 采用与 `annotations` 相同的容错写法：解码失败留 nil，不改这条无错误
返回值的转换签名。写入方只有本仓库自己的抽取器，解码失败在正常运行下不可达。

## 前端（`dashboard/`）

`RequestDetailsContent.vue` 在 Token 区块与「成本」区块之间插入「工具用量」区块，`v-if` 到
`selected.toolUsage?.length`。每个工具一行 `Field`，label 是工具名，值是 `model` 与该工具报出的计数拼成的
紧凑摘要（`toolUsageSummary`），沿用现有 2 列 grid 与 `fmtNum`。

「成本」区块的 `v-if` 放宽为 `modelCost != null || toolCost != null`，并在其中增加「工具价」`MoneyDisplay`，
条件 `selected.toolCost != null`。当前 `toolCost` 永远为 NULL，所以这一项实际不会渲染，属于按需求预接管道。

类型来自 `mise run openapi` + `pnpm --dir dashboard generate-openapi` 重新生成的 `openapi-types.d.ts`，
不手写。

## 不做的事

- 不计算工具费用，不接 `pkg/pricing`。
- 不加 `tool_usage` 的查询过滤、列表列、概览聚合。
- 不扩展脚本 `beforeMetaRequest` 的 `tokens` 契约。
- 不改 `ListRequestTraces` 的费用聚合。
