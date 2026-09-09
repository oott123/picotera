# 执行计划

## 1. 数据库迁移

**新建 `db/migrations/050_request_usage_raw.sql`**

```sql
-- +goose Up
ALTER TABLE request ADD COLUMN usage_raw JSONB;
ALTER TABLE request ADD COLUMN tool_usage_raw JSONB;

-- +goose Down
ALTER TABLE request DROP COLUMN IF EXISTS tool_usage_raw;
ALTER TABLE request DROP COLUMN IF EXISTS usage_raw;
```

不建索引，不动任何连续聚合与 `completion_endpoint_path` 视图。

## 2. sqlc 查询

**改 `db/queries/request.sql`：**

1. `UpdateRequest`：在 `tool_cost_currency = ...` 之后插入两行
   ```sql
   usage_raw = CASE WHEN sqlc.arg('set_usage_raw')::bool THEN sqlc.narg('usage_raw')::jsonb ELSE usage_raw END,
   tool_usage_raw = CASE WHEN sqlc.arg('set_tool_usage_raw')::bool THEN sqlc.narg('tool_usage_raw')::jsonb ELSE tool_usage_raw END,
   ```
2. `ListRequests`：SELECT 列表里 `r.tool_usage, r.tool_cost, r.tool_cost_currency,` 之后加
   `r.usage_raw, r.tool_usage_raw,`
3. `ListRequestsBySpan`：同样加这两列。
4. `GetRequest` 用 `r.*`，不改。`InsertRequest`、`ListRequestTraces`、`SetRequestAnnotation` 不改。

**跑 `sqlc generate`**，确认 `pkg/db/` 里出现 `UsageRaw []byte` / `ToolUsageRaw []byte`，以及
`UpdateRequestParams` 的 `SetUsageRaw` / `SetToolUsageRaw` 两个布尔。

## 3. 解析层（`pkg/server/response_extractor.go`）

1. `ResponseMetrics` 增加两个字段，注释写明「原始对象、末次覆盖、未报告时为 nil」：
   ```go
   // UsageRaw is the last non-empty usage object the upstream reported, stored
   // verbatim. Unlike the five token fields it is not accumulated key-by-key: a
   // later occurrence replaces the whole object.
   UsageRaw []byte
   // ToolUsageRaw is the last non-empty tool_usage object, stored verbatim —
   // including entries normalizeToolUsage drops for being all-zero.
   ToolUsageRaw []byte
   ```

2. 新增路径表与采集函数：
   ```go
   // usageRawPaths are the objects that hold the format's usage counters. First
   // match per payload wins; the four paths cover all supported upstream formats.
   var usageRawPaths = []string{"usage", "response.usage", "message.usage", "usageMetadata"}

   // captureUsageRaw records the payload's usage object verbatim. Last non-empty
   // occurrence wins — the whole object, never a key-by-key merge, so the column
   // stays byte-faithful to one upstream event.
   func (e *ResponseExtractor) captureUsageRaw(result gjson.Result) {
       for _, path := range usageRawPaths {
           v := result.Get(path)
           if !v.IsObject() || !hasKeys(v) {
               continue
           }
           e.metrics.UsageRaw = []byte(v.Raw)
           return
       }
   }

   // hasKeys reports whether an object has at least one member. An empty object
   // counts as "not reported", same as an absent one.
   func hasKeys(v gjson.Result) bool {
       found := false
       v.ForEach(func(_, _ gjson.Result) bool { found = true; return false })
       return found
   }
   ```

3. 在**三个**既有的 `extractToolUsage` 调用点旁边各加一次 `captureUsageRaw`（保持两者调用点严格同构）：
   - `processSSEEvent`：`e.captureUsageRaw(gjson.Parse(payload))`（可与既有的 `gjson.Parse` 共用一个变量，
     避免重复解析）
   - `processGeminiArrayElement`：`e.captureUsageRaw(result)`
   - `extractJSONMetrics`：`e.captureUsageRaw(result)`

4. 把 `toolUsageScopes` 换成成对路径表 `toolUsagePaths`，与 `usageRawPaths` 同构：
   ```go
   // toolUsagePaths are the paths a payload's tool_usage object can sit at, each
   // paired with the sibling tools declaration array read from the same object
   // (that pairing is why this is a table of pairs and not a plain []string like
   // usageRawPaths). tool_usage sits beside the format's usage object: the
   // payload's top level for OpenAI Chat, Anthropic, Gemini and non-stream
   // bodies, and the "response" envelope for OpenAI Responses SSE events, where
   // usage likewise lives at response.usage. First match wins; no payload
   // carries both.
   var toolUsagePaths = []struct{ usage, tools string }{
       {usage: "tool_usage", tools: "tools"},
       {usage: "response.tool_usage", tools: "response.tools"},
   }
   ```

5. 改写 `extractToolUsage`：去掉 `scope := result; if path != "" { scope = result.Get(path) }` 这段拐弯，
   把空对象判断收紧，并无条件记录原文：
   ```go
   func (e *ResponseExtractor) extractToolUsage(result gjson.Result) {
       for _, p := range toolUsagePaths {
           tu := result.Get(p.usage)
           if !tu.IsObject() || !hasKeys(tu) {
               continue
           }
           e.metrics.ToolUsageRaw = []byte(tu.Raw)
           if entries := normalizeToolUsage(tu, result.Get(p.tools)); len(entries) > 0 {
               e.metrics.ToolUsage = entries
           }
           return
       }
   }
   ```
   同时更新函数头注释，说明 raw 的记录不受归一化结果影响。三个调用点的传参形态不变（都已经是
   `gjson.Result`），无需改动。

## 4. 写库（`pkg/server`）

1. **`request_update.go`**：在 `ToolCostCurrency` 之后加两个 setter
   ```go
   func (u *requestUpdate) UsageRaw(v []byte) *requestUpdate {
       u.p.SetUsageRaw = true
       u.p.UsageRaw = v
       return u
   }

   func (u *requestUpdate) ToolUsageRaw(v []byte) *requestUpdate { /* 同构 */ }
   ```

2. **`gateway_flow_success.go` `completeGatewaySuccess`**：upstream 行与 meta 行两处链式调用里，
   在 `.ToolCostCurrency(toolCcy)` 之后各加
   ```go
   .UsageRaw(m.UsageRaw).
   ToolUsageRaw(m.ToolUsageRaw).
   ```
   直接取 `m`，不经过 `resolveToolUsageCost`。

3. **`gateway_unified_helpers.go` `unifiedStreamSuccess`**：同样两处，同样加两行。

4. 其他路径（失败路径、`gateway_flow_script_response.go` 的 `beforeMetaRequest` 短路、
   `failUnifiedSuccess`）一律不写这两列，保持 NULL。

## 5. metaOutcome 快照（`pkg/server/gateway_flow_finish.go`）

1. `metaOutcome` 增加两个字段：
   ```go
   usageRaw     []byte
   toolUsageRaw []byte
   ```
2. `merge` 增加两个分支（放在 `SetToolUsage` 之后）：
   ```go
   if p.SetUsageRaw {
       o.usageRaw = p.UsageRaw
   }
   if p.SetToolUsageRaw {
       o.toolUsageRaw = p.ToolUsageRaw
   }
   ```
3. `runRequestFinished` 把两者塞进 view：
   ```go
   UsageRaw:     json.RawMessage(o.usageRaw),
   ToolUsageRaw: json.RawMessage(o.toolUsageRaw),
   ```
   nil / 空切片交给 jsx 层归一成 `null`（见第 6 步），这里不做处理 —— 与 `toolUsage` 在此处补 `[]` 的做法不同，
   因为 raw 的「没有」就是 `null`，没有第二种表示。

## 6. jsx 层（`pkg/jsx`）

1. **`types.go`**：
   - `ToolUsageCostView` 增加
     ```go
     // UsageRaw / ToolUsageRaw are the upstream's own usage / tool_usage objects,
     // verbatim. Read-only: the hook's result is rebuilt from toolUsage / toolCost
     // / toolCostCurrency alone, so returning them changes nothing. null when the
     // upstream reported none.
     UsageRaw     json.RawMessage `json:"usageRaw"`
     ToolUsageRaw json.RawMessage `json:"toolUsageRaw"`
     ```
   - `RequestFinishedView` 增加同样两个字段（放在 `ToolUsage` 之后），注释说明取自内存快照、失败请求恒为 `null`。
   - 文件已 import `encoding/json`，无需改 import。

2. **`session.go`**：
   - 新增小工具（放在 `mustJSON` 附近）：
     ```go
     // rawOrNull normalizes a possibly-empty RawMessage to valid JSON. An empty
     // (non-nil) RawMessage would fail json.Marshal; "not reported" is null.
     func rawOrNull(b json.RawMessage) json.RawMessage {
         if len(b) == 0 {
             return json.RawMessage("null")
         }
         return b
     }
     ```
   - `RunGetToolUsageCost` 在 `mustJSON(initial)` 之前：
     ```go
     initial.UsageRaw = rawOrNull(initial.UsageRaw)
     initial.ToolUsageRaw = rawOrNull(initial.ToolUsageRaw)
     ```
   - `RunRequestFinished` 同样在 `mustJSON(input)` 之前归一两个字段。
   - glue IIFE **不改**：`getToolUsageCost` 的返回值仍然只由 `toolUsage` / `toolCost` /
     `toolCostCurrency` 三项重建，raw 字段天然被忽略；`validateToolUsageCost` 也不需要改。

3. **`iface.go`**：`RunGetToolUsageCost` / `RunRequestFinished` 的接口注释补一句输入含只读 raw 字段。

## 7. hook 输入组装（`pkg/server/gateway_flow_tool_cost.go`）

`resolveToolUsageCost` 的返回值签名不变（仍是三列），只在构造 hook 输入时补上 raw：

```go
out, err := f.session.RunGetToolUsageCost(jsx.ToolUsageCostView{
    ToolUsage:    toolUsageEntriesToJSX(m.ToolUsage),
    UsageRaw:     json.RawMessage(m.UsageRaw),
    ToolUsageRaw: json.RawMessage(m.ToolUsageRaw),
})
```

函数头注释补一句：raw 两项只读，脚本无法改写，写库时取的是 `ResponseMetrics` 的原值。

## 8. 管理 API（`pkg/contract/request.go`）

1. `RequestView` 在三个 tool 字段之后加
   ```go
   UsageRaw     map[string]any `json:"usageRaw,omitempty"`
   ToolUsageRaw map[string]any `json:"toolUsageRaw,omitempty"`
   ```
2. `requestLike` 加 `UsageRaw []byte` / `ToolUsageRaw []byte`。
3. `toRequestView` 在 `ToolUsage` 解码块之后加同样的宽容解码（失败留空，不改无错误签名）：
   ```go
   if len(r.UsageRaw) > 0 {
       var u map[string]any
       if err := json.Unmarshal(r.UsageRaw, &u); err == nil {
           view.UsageRaw = u
       }
   }
   // ToolUsageRaw 同构
   ```
4. `ToRequestView` / `ToListRequestRowView` / `ToListRequestsBySpanRowView` 三个构造函数各补两行字段传递。

5. 跑 `mise run openapi` 重新生成 `openapi.yaml`。

## 9. 仪表盘

1. `pnpm --dir dashboard generate-openapi` 重新生成 `src/openapi-types.d.ts`。

2. **`src/composables/useRequestDetailUiState.ts`**：`DetailTab` 加 `'usage'`。

3. **新建 `src/components/UsageRawView.vue`**：
   - props：`{ usageRaw?: Record<string, unknown> | null; toolUsageRaw?: Record<string, unknown> | null }`
   - 两个 section，标题分别是「原始用量」「原始工具用量」，标题样式沿用详情页既有的
     `text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]`。
   - 每个 section 内容是 `JSON.stringify(v, null, 2)` 渲染进 `<pre>`，样式沿用「错误信息」块的
     `font-mono text-xs whitespace-pre-wrap bg-surface-50 border border-line-soft rounded-md p-3 m-0 text-ink`，
     再补 `max-h-96 overflow-auto`。
   - 某一份缺失时该 section 不渲染；两份都缺失时用 `<StateText :dashed="false" compact>无用量数据</StateText>`
     兜底（与 `LogsArtifactView.vue` 的空态一致）。
   - 只用 `src/ui/` 里的既有原语，不引入任何第三方组件。

4. **`src/components/RequestDetailsContent.vue`**：
   - import `UsageRawView`。
   - 加 `const hasUsageRaw = computed(() => !!selected.value?.usageRaw || !!selected.value?.toolUsageRaw)`。
   - `detailTabs` 在 `logs` 之后追加：`if (hasUsageRaw.value) base.push({ value: 'usage', label: '用量' })`。
     （既有的 `watch(detailTabs)` 已经负责在 tab 消失时回落到 `overview`，无需额外处理。）
   - 模板末尾在 `LogsArtifactView` 之后加
     ```vue
     <UsageRawView
       v-else-if="detailTab === 'usage'"
       :usage-raw="selected.usageRaw"
       :tool-usage-raw="selected.toolUsageRaw"
     />
     ```
   - 概览 tab 的「Token」「工具用量」「成本」三个 section 保持不变。

## 10. 文档

1. **`docs/scripting.md`**：
   - `getToolUsageCost` 小节的 `ToolUsageCost` 接口块加 `usageRaw` / `toolUsageRaw` 两行并标注只读；
     在「写入语义」里加一条：两个 raw 字段是上游原文，脚本改不了，落库取的是抽取到的原值。
   - `requestFinished` 小节的 `RequestFinishedView` 接口块加同样两行，并在下方说明段补一句：
     未报告时为 `null`（不同于 `toolUsage` 的恒为数组），失败请求恒为 `null`。
   - 两处都点明 `usageRaw` 的「整对象末次覆盖」语义，以及 Anthropic 流式因此只留下 `message_delta` 的 usage。

2. **新建 `docs/example-scripts/raw-usage-accounting.js`**：一个脚本同时演示两个 hook 读 raw ——
   `getToolUsageCost` 里按 `toolUsageRaw.web_search.num_requests` 与 `image_gen.num_images` 计价，
   `requestFinished` 里把 `usageRaw.output_tokens_details.reasoning_tokens` 打成请求注解。
   头部注释按目录内既有脚本的风格写明用途与适用上游。
   本次改动是纯新增字段，既有示例脚本无需迁移。

3. **根 `CLAUDE.md`**：
   - 「Scripts」章节的 `getToolUsageCost` 与 `requestFinished` 两个 bullet 补上新字段与只读语义。
   - 「Database Schema」段落在 migration 049 的描述之后补一句 migration 050：两列可空 JSONB、无索引无过滤端点、
     只在两条成功路径写入、`usage_raw` 整对象末次覆盖、`tool_usage_raw` 保留归一化丢弃的全零条目。
   - 同一段里那句 `hence toolUsageScopes stores scopes — "" and "response" — rather than full paths` 已随本次
     重构失效，改成成对路径表的说法。（后文另一句本来就写的是 `toolUsagePaths tries tool_usage then
     response.tool_usage` —— 那是既有的文档漂移，本次重构正好让代码与它对齐，不必再改。）

4. `dashboard/CLAUDE.md` 的 `useRequestDetailUiState` 描述里把 tab 列表补上 `usage`。

## 11. 测试

1. **`pkg/server/response_extractor_test.go`** 新增：
   - OpenAI Chat 非流式：`usage_raw` 命中顶层 `usage`，原文含归一化丢弃的 `prompt_tokens_details` 明细。
   - OpenAI Responses SSE：命中 `response.usage`；且 `tool_usage` 在 `response.created` / `response.completed`
     重复出现时，raw 取最后一次。
   - Anthropic SSE：`message_start` 先命中 `message.usage`，`message_delta` 的 `usage` **整个覆盖**它 ——
     断言 raw 里没有 `input_tokens`，同时断言归一化的 `InputTokens` 仍然是 `message_start` 的值（证明两条路互不影响）。
   - Gemini SSE：只带 `trafficType` 的首帧被末帧完整 `usageMetadata` 覆盖。
   - `usage: null` / `usage: {}` 不算命中（`UsageRaw` 保持 nil）。
   - `tool_usage_raw` 保留全零的 `image_gen`，而同一次提取的 `ToolUsage` 为空。
   - 顶层 `tool_usage: {}` 不再挡住 `response.tool_usage`（`toolUsagePaths` 收紧后的行为）。

   既有的 `tool_usage` 归一化用例应当在重构后原样通过 —— 若有用例依赖旧的作用域写法，只改测试的构造方式，
   不改断言。

2. **`pkg/jsx/tool_usage_cost_test.go`** 新增：tap 能读到 `input.usageRaw.xxx` 与 `input.toolUsageRaw.xxx`；
   未提供时两者为 `null`；tap 把 raw 字段原样带回返回值不影响落库结果（校验不报错、结果仍是三项）。

3. **`pkg/jsx/annotations_test.go`** 的 `TestRunRequestFinished_TapReadsEveryField` 扩展两个新字段。

4. 检查 `pkg/server/gateway_flow_tool_cost_test.go` 里的 fake session，按需补上新字段的断言。

## 12. 验证

```bash
go build ./...
go test ./...
mise run openapi                        # 之后确认 openapi.yaml 有 usageRaw / toolUsageRaw
pnpm --dir dashboard generate-openapi
pnpm --dir dashboard type-check
pnpm --dir dashboard lint
pnpm --dir dashboard build
```

手工验证：`docker compose up -d` + `mise run server`（迁移自动执行），打一次带 web 搜索的 Codex 请求，
在请求详情页确认「用量」tab 出现并展示两份原始 JSON，且概览 tab 的归一化数据与之对得上。
