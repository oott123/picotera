# 执行计划

## 1. jsx 层类型（`pkg/jsx/types.go`）

- 新增 `ToolUsageEntry`：`Name` / `Model` / `NumRequests` / `InputTokens` / `OutputTokens` / `NumImages`，
  JSON 标签与 `server.ToolUsageEntry` 完全一致（`name` 必定输出，其余带 `omitempty`）。
- 新增 `ToolUsageCostView`：`ToolUsage []ToolUsageEntry` / `ToolCost *float64` / `ToolCostCurrency string`。
- `RequestFinishedView` 增加 `ToolCost float64 \`json:"toolCost"\`` 与
  `ToolCostCurrency string \`json:"toolCostCurrency"\``，并修改该类型上「tool_cost 不在视图里」的注释。

## 2. Session 接口与实现

- `pkg/jsx/iface.go`：`Session` 增加 `RunGetToolUsageCost(initial ToolUsageCostView) (ToolUsageCostView, error)`。
- `pkg/jsx/sdk.js`：在 `hooks` 中注册 `getToolUsageCost: new Waterfall()`。
- `pkg/jsx/session.go`：实现 `RunGetToolUsageCost`，glue 文件名 `hook-getToolUsageCost.js`。
  - 透传判定：`r === globalThis.ctx || r === input || undefined || null` 时返回 `undefined`，
    调用方保持初始值。
  - glue 内按 design.md 的规则逐条校验并抛出异常；条目的键使用白名单，出现其他键即报错。
  - 归一化返回 `{ toolUsage: [...], toolCost: <number|null>, toolCostCurrency: <string> }`，
    再 `json.Unmarshal` 进 `ToolUsageCostView`。
  - host 侧 `validateToolUsageCost(*ToolUsageCostView) error` 复检同一组规则（参照
    `validateResponseShape`），失败时返回 error。

## 3. jsx 测试（`pkg/jsx/tool_usage_cost_test.go`）

沿用 `newTestSession*` 的写法：

- 透传（没有 tap、返回 undefined、原样返回 input）时保持初始值。
- 改写用量、补充 `toolCost` 与 `toolCostCurrency`，检查解码结果。
- 清空为 `[]`。
- 校验失败的各种情况：`name` 为空、计数器为负数或小数、条目含多余的键、`toolCost` 为 `NaN` / 负数 /
  `>= 1e14`、有 cost 无 currency、有 currency 无 cost、结果是数组。
- hook 抛出异常时返回 error，且不改动初始值。

## 4. server 侧胶水代码（新建 `pkg/server/gateway_flow_tool_cost.go`）

- `toolUsageEntriesToJSX([]ToolUsageEntry) []jsx.ToolUsageEntry` 与反向的 `toolUsageEntriesFromJSX`。
- `marshalToolUsage([]ToolUsageEntry) []byte`：丢弃计数器全为 0 的条目，结果为空时返回 nil（列写入
  NULL）。它替换 `gateway_helpers.go` 中的 `toolUsageJSON(ResponseMetrics)`，删除后者，同步修改
  `gateway_helpers_test.go` 的 `TestToolUsageJSON`。
- `toolCostToNumeric(*float64) (pgtype.Numeric, pgtype.Text)`：nil 时返回两个 invalid 值；否则
  `ratToNumeric6(new(big.Rat).SetFloat64(v))` 加 currency。
- `(f *gatewayFlow) resolveToolUsageCost(m ResponseMetrics) (usage []byte, cost pgtype.Numeric, ccy pgtype.Text)`：
  组装初始值 → `f.session.RunGetToolUsageCost` → 出错时用 logx 记录 warn 并回退到
  `marshalToolUsage(m.ToolUsage)` 加空费用；`f.session == nil` 时直接走回退分支，不记录日志。

## 5. 接入成功路径

- `pkg/server/gateway_flow_success.go` 的 `completeGatewaySuccess`：把 `toolUsage := toolUsageJSON(m)`
  换成 `toolUsage, toolCost, toolCcy := input.Flow.resolveToolUsageCost(m)`，两处 `newRequestUpdate`
  调用链补上 `.ToolCost(toolCost).ToolCostCurrency(toolCcy)`。
- `pkg/server/gateway_unified_helpers.go` 的 `unifiedStreamSuccess`：同样替换，调用方是 `a.flow`。
- 删除 `pkg/server/request_update.go` 中 `ToolCost` / `ToolCostCurrency` 上「没有调用方」的注释。

## 6. requestFinished 视图（`pkg/server/gateway_flow_finish.go`）

- `metaOutcome` 增加 `toolCost float64` / `toolCostCurrency string`，更新类型注释。
- `merge` 增加 `SetToolCost`（numeric 转 float64，取值方式与 `SetModelCost` 分支相同）与
  `SetToolCostCurrency`。
- `runRequestFinished` 填充 `ToolCost` 与 `ToolCostCurrency`。

## 7. server 测试

- `pkg/server/gateway_flow_finish_test.go`：补充 `metaOutcome.merge` 对这两列的镜像（包含 NULL 合并为
  零值的情况）。
- 新建 `pkg/server/gateway_flow_tool_cost_test.go`：`marshalToolUsage` 的空输入、非空输入、全零条目丢弃；
  `toolCostToNumeric` 的 nil 与取整；jsx 条目的双向转换。

## 8. 文档

`CLAUDE.md`：

- Scripts 小节的 hook 列表增加 `getToolUsageCost` 条目（触发点、输入输出规则、失败回退、写入两行记录）。
- `requestFinished` 条目补充 `toolCost` 与 `toolCostCurrency`，删除「deliberately not in this view /
  nothing writes them yet」的表述。
- Database Schema 小节里 `tool_cost` / `tool_cost_currency`「有 setter 无调用方、始终 NULL」的描述改为由
  `getToolUsageCost` 写入。

## 9. 验证

```bash
go build ./... && go test ./pkg/...
```

不涉及 sqlc、openapi 和 dashboard 的改动：`RequestView.toolCost` 与请求详情页的展示已经就绪。
