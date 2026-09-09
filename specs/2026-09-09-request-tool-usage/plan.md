# 执行计划

## 1. 数据库迁移

**新建 `db/migrations/049_request_tool_usage.sql`**

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

不建索引，不动任何连续聚合。

## 2. sqlc 查询

**改 `db/queries/request.sql`：**

1. `UpdateRequest`：在 `model_cost_currency = ...` 之后插入三行
   ```sql
   tool_usage = CASE WHEN sqlc.arg('set_tool_usage')::bool THEN sqlc.narg('tool_usage')::jsonb ELSE tool_usage END,
   tool_cost = CASE WHEN sqlc.arg('set_tool_cost')::bool THEN sqlc.narg('tool_cost')::numeric ELSE tool_cost END,
   tool_cost_currency = CASE WHEN sqlc.arg('set_tool_cost_currency')::bool THEN sqlc.narg('tool_cost_currency')::text ELSE tool_cost_currency END,
   ```
2. `ListRequests`：SELECT 列表里 `r.model_cost, r.model_cost_currency,` 之后加
   `r.tool_usage, r.tool_cost, r.tool_cost_currency,`
3. `ListRequestsBySpan`：同样加这三列。
4. `GetRequest` 用 `r.*`，不改。`InsertRequest`、`ListRequestTraces` 不改。

**跑 `sqlc generate`**，确认 `pkg/db/` 里出现 `ToolUsage []byte` / `ToolCost pgtype.Numeric` /
`ToolCostCurrency pgtype.Text`，以及 `UpdateRequestParams` 的三个 `SetTool*` 布尔。

## 3. 解析层

**改 `pkg/server/response_extractor.go`：**

1. 在 `ResponseMetrics` 上方新增 `ToolUsageEntry` 类型（`Name string` + 四个 `*int64`，
   JSON tag 分别 `name` / `numRequests` / `inputTokens` / `outputTokens` / `numImages`，
   后四个 `omitempty`）。
2. `ResponseMetrics` 增加字段 `ToolUsage []ToolUsageEntry`。
3. 新增方法 `extractToolUsage(result gjson.Result)`：
   - `tu := result.Get("tool_usage")`；`!tu.IsObject()` 直接返回。
   - `tu.ForEach`：value 非对象则跳过该条目；否则用辅助函数 `toolUsageInt(v, key)` 取四个白名单字段
     （`f.Type != gjson.Number` 返回 nil，否则返回 `&f.Int()` 的副本）。
   - 收集到的条目为空则返回（不覆盖已有值）；否则整体赋给 `e.metrics.ToolUsage`（last wins）。
4. 三处调用点，都传 `gjson.Parse(payload)` / 已有的 `result`：
   - `processSSEEvent`：在 `e.inferProvider(payload)` 之前加 `e.extractToolUsage(gjson.Parse(payload))`。
   - `extractJSONMetrics`：在函数末尾（Gemini 分支之后）加 `e.extractToolUsage(result)`。
   - `processGeminiArrayElement`：在 `e.setGeminiUsage(...)` 之后加 `e.extractToolUsage(result)`。

## 4. 写库层

**改 `pkg/server/gateway_helpers.go`：** 在 `metricsToPG` 之后新增

```go
// toolUsageJSON marshals extracted tool usage for the tool_usage JSONB column.
// Returns nil when nothing was extracted, so the column is written as SQL NULL.
func toolUsageJSON(m ResponseMetrics) []byte {
    if len(m.ToolUsage) == 0 {
        return nil
    }
    b, err := json.Marshal(m.ToolUsage)
    if err != nil {
        return nil
    }
    return b
}
```

（该文件若尚未 import `encoding/json`，一并加上。）

**改 `pkg/server/request_update.go`：** 在 `ModelCostCurrency` 之后新增三个 setter
`ToolUsage([]byte)`、`ToolCost(pgtype.Numeric)`、`ToolCostCurrency(pgtype.Text)`，写法与既有 setter 一致。
后两个本次无调用方，在注释里点明「管道已通，待接计费」。

**改 `pkg/server/gateway_flow_success.go`：** `completeGatewaySuccess` 里
`modelCost, modelCcy := ...` 之后取 `toolUsage := toolUsageJSON(m)`，并在 upstream 行与 meta 行两个
`newRequestUpdate(...)` 链上、`ModelCostCurrency(modelCcy)` 之后各加 `.ToolUsage(toolUsage)`。

**改 `pkg/server/gateway_unified_helpers.go`：** `unifiedStreamSuccess` 中同样的两处
（约 645 / 665 行的两条链）做同样的改动。

## 5. requestFinished hook

**改 `pkg/jsx/types.go`：** `RequestFinishedView` 增加
`ToolUsage json.RawMessage \`json:"toolUsage"\``，并更新该结构体上方的注释说明它恒为数组。

**改 `pkg/server/gateway_flow_finish.go`：**
- `metaOutcome` 增加 `toolUsage []byte`。
- `merge` 里增加 `if p.SetToolUsage { o.toolUsage = p.ToolUsage }`。
- `runRequestFinished` 里：`tu := json.RawMessage(o.toolUsage); if len(tu) == 0 { tu = json.RawMessage("[]") }`，
  传给 `ToolUsage`。（该文件需 import `encoding/json`。）
- 在文件顶部 `metaOutcome` 的注释里补一句：`toolCost` / `toolCostCurrency` 故意不镜像，因为没有写入方
  也不进 view。

## 6. contract / OpenAPI

**改 `pkg/contract/request.go`：**
1. 新增 `ToolUsageEntryView` 类型（形状见 `api.md`）。
2. `RequestView` 增加
   ```go
   ToolUsage        []ToolUsageEntryView `json:"toolUsage,omitempty"`
   ToolCost         *float64             `json:"toolCost,omitempty"`
   ToolCostCurrency string               `json:"toolCostCurrency,omitempty"`
   ```
3. `requestLike` 增加 `ToolUsage []byte` / `ToolCost pgtype.Numeric` / `ToolCostCurrency pgtype.Text`。
4. `toRequestView` 里：`ToolCost` 走与 `ModelCost` 相同的 `numericToFloat` 写法；`ToolCostCurrency`
   走 `.Valid` 判断；`ToolUsage` 走与 `Annotations` 相同的容错解码（`len(...) > 0` → `json.Unmarshal`
   到 `[]ToolUsageEntryView`，失败留 nil）。
5. `ToRequestView` / `ToListRequestRowView` / `ToListRequestsBySpanRowView` 三个函数各透传三个新字段。

**跑 `mise run openapi`**，确认 `openapi.yaml` 里出现 `ToolUsageEntryView` schema 与三个新字段。

**跑 `pnpm --dir dashboard generate-openapi`**。

## 7. 前端

**改 `dashboard/src/components/RequestDetailsContent.vue`：**

1. 在 Token `<section>` 与「成本」`<section>` 之间插入工具用量区块：
   ```html
   <section v-if="selected.toolUsage?.length" class="flex flex-col gap-2.5">
     <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]">工具用量</span>
     <div class="grid grid-cols-2 gap-2.5">
       <Field v-for="t in selected.toolUsage" :key="t.name" :label="t.name" as="div">
         <span class="font-mono tabular-nums text-sm">{{ toolUsageSummary(t) }}</span>
       </Field>
     </div>
   </section>
   ```
2. `<script setup>` 里加纯函数 `toolUsageSummary(t)`：按 `numRequests` → `请求 N`、
   `inputTokens` → `输入 N`、`outputTokens` → `输出 N`、`numImages` → `图片 N` 的顺序，
   只拼接非 `null`/`undefined` 的项，用 ` · ` 连接；全为空时返回 `'—'`。数字走既有 `fmtNum`。
3. 「成本」区块的 `v-if` 改为 `selected.modelCost != null || selected.toolCost != null`，
   「模型价」`Field` 外层加 `v-if="selected.modelCost != null"`，并在其后新增
   ```html
   <Field v-if="selected.toolCost != null" label="工具价" as="div">
     <span class="font-mono tabular-nums text-sm">
       <MoneyDisplay :amount="selected.toolCost ?? null" :currency="selected.toolCostCurrency ?? ''" />
     </span>
   </Field>
   ```

## 8. 测试

**改 `pkg/server/response_extractor_test.go`**，按文件既有的表驱动风格补：

- OpenAI Responses SSE：`response.completed` 事件带顶层 `tool_usage`（用 proposal 里的示例 payload）
  → `ToolUsage` 等于两个条目，`image_gen` 只有 `InputTokens`/`OutputTokens`，`web_search` 只有
  `NumRequests`，`total_tokens` / `*_details` 未落入；同时断言 `response.usage` 的 token 抽取不受影响。
- 非流式 JSON body 带 `tool_usage` → 同样解析。
- Anthropic SSE `message_delta` 事件带顶层 `tool_usage` → 解析成功（验证顶层路径与格式无关）。
- 无 `tool_usage` → `ToolUsage` 为 nil。
- `tool_usage` 是数组 / 字符串 → 忽略，`ToolUsage` 为 nil。
- 某个 value 非对象（`{"a": 1, "web_search": {...}}`）→ 只产出 `web_search`。
- 某个 value 是空对象 → 产出 `{Name: "..."}`，四个指针全 nil。
- 白名单字段是字符串（`{"num_requests": "1"}`）→ 该字段为 nil。
- 顺序：`{"z": {...}, "a": {...}}` → 数组顺序为 `z, a`。
- last wins：两个 SSE 事件先后带不同 `tool_usage`，取后者；后者为空对象时保留前者。

**改 `pkg/server/gateway_flow_finish_test.go`**：补 `metaOutcome.merge` 对 `SetToolUsage` 的镜像用例
（set 时覆盖、未 set 时保留）。

**新增/改 `pkg/server/gateway_helpers_test.go`**：`toolUsageJSON` 的空输入返回 nil、非空输入产出预期 JSON
（含 `omitempty` 生效，缺席字段不出现在输出里）。

## 9. 文档

**改 `CLAUDE.md`：**
- 「Database Schema」段落里，在 `annotations` JSONB 列的说明之后补一句：`request` 另有
  `tool_usage` JSONB（上游报告的工具用量归一化数组，唯一写入方是成功路径的 `ResponseExtractor`，无索引、
  无过滤端点）与 `tool_cost` / `tool_cost_currency`（形状同 `model_cost` 系列，当前无写入方、恒 NULL）。
- 「Scripts」段 `requestFinished` 那一条的字段清单里加上 `toolUsage`，并注明它恒为数组（无用量时 `[]`），
  且 `toolCost` 不在 view 里。

`README.md` 不动。

## 10. 验证

```bash
go build ./... && go test ./pkg/server/... ./pkg/jsx/...
docker compose up -d && mise run server     # 确认迁移 049 正常执行
pnpm --dir dashboard type-check
pnpm --dir dashboard lint
pnpm --dir dashboard build
```

另外确认 `git status` 里 `openapi.yaml` 与 `dashboard/src/openapi-types.d.ts` 都已随改动更新。
