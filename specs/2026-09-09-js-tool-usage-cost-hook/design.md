# 设计：getToolUsageCost hook

## 位置与数据流

hook 是工具用量写入数据库之前的最后一道加工：`ResponseExtractor` 归一化出来的 `[]ToolUsageEntry` 交给
脚本，脚本返回的用量与费用直接写入 `request` 表的 `tool_usage` / `tool_cost` / `tool_cost_currency`。

```
extractor.Metrics().ToolUsage
  └─ 初始值 { toolUsage: [...], toolCost: null, toolCostCurrency: "" }
       └─ picotera.hooks.getToolUsageCost 瀑布流（glue 校验）
            └─ host 复检 → []ToolUsageEntry → json.Marshal → tool_usage
                         → ratToNumeric6      → tool_cost
                         → currency           → tool_cost_currency
```

调用点在成功路径写入数据库之前：`completeGatewaySuccess`（`gateway_flow_success.go`）与
`unifiedStreamSuccess`（`gateway_unified_helpers.go`）。两处都无条件运行一次，`f.session == nil` 时跳过
（在模型解析之前就失败的请求没有 VM）。一次 hook 的结果同时写入 meta 行和 upstream 行，与现在
`toolUsage` 的写法一致。

失败路径不运行该 hook：没有上游响应就没有工具用量，也没有 `tool_usage` 列可写。`beforeMetaRequest`
短路响应同理。

## 输入输出规则

hook 的输入和输出都是 `jsx.ToolUsageCostView`：

```go
type ToolUsageCostView struct {
    ToolUsage        []ToolUsageEntry `json:"toolUsage"`
    ToolCost         *float64         `json:"toolCost"`
    ToolCostCurrency string           `json:"toolCostCurrency"`
}
```

`ToolUsageEntry` 是 `server.ToolUsageEntry` / `contract.ToolUsageEntryView` 在 jsx 层的副本，字段完全
相同，理由与 `ProviderModelEntry` 相同：jsx 不反向依赖 server 和 contract。

校验在 glue IIFE 里做一遍、host 侧 `validateToolUsageCost` 再做一遍（与 `beforeMetaRequest` 相同）。全部
严格校验，不做任何类型强制转换，遇到不合规的输入立即抛出异常：

- 结果必须是对象，不能是数组；返回 `undefined`、`null`、`ctx` 或原样的 input 对象表示透传，保持初始值。
- `toolUsage` 必须是数组，元素必须是对象；条目只允许 `name` / `model` / `numRequests` / `inputTokens` /
  `outputTokens` / `numImages` 这些键，出现其他键即报错 —— 字段名拼写错误会导致计费数据被静默丢弃。
- `name` 必须是非空字符串；`model` 出现时必须是字符串；各个计数器出现时必须满足
  `Number.isSafeInteger(v) && v >= 0`。
- `toolCost` 为 `null` 或未提供表示不计费；否则必须是有限数字，且满足 `>= 0` 与 `< 1e14`（列类型是
  `NUMERIC(20, 6)`，整数部分只有 14 位，超出会在写入数据库时报错）。
- `toolCostCurrency`：`toolCost` 是数字时必须是非空字符串；`toolCost` 为空时必须未提供或者是空字符串。
  币种本身不查 `exchange_rate` 表，与 pricing 直接写入 `p.Currency` 的做法一致。

## 序列化

脚本返回的条目转回 `[]server.ToolUsageEntry` 之后，用同一个 marshaller 写入数据库，因此列的形状与抽取器
产出的完全一致：值为 0 的计数器被 `omitempty` 丢弃，计数器全为 0 的条目被整条丢弃（声明了 `model` 也
一样），最终为空数组时写入 SQL NULL。

丢弃逻辑放在 `marshalToolUsage` 里统一执行，抽取器归一化时的同名丢弃保持原样 —— 它还要据此决定是否
跳过 model 查找。全零条目不算校验错误，直接静默丢弃。

`toolCost` 经 `ratToNumeric6`（`big.Rat.SetFloat64` 后保留 6 位小数、四舍五入）转换为 numeric，与
`model_cost` 精度相同。`toolCost` 为空时这两列显式写入 NULL。

## 错误处理

hook 抛出异常、校验失败、或者 session 因为超时失效时，统一记录一条 warn 级别日志，`tool_usage` 写入抽取
值，两个费用列保持 NULL。响应此时已经发送给客户端，没有可以让其失败的对象；也不改写 `finish_reason`，
避免让一次成功的上游响应影响成功率统计。

## 下游暴露

`metaOutcome` 增加 `toolCost float64` / `toolCostCurrency string`，从 `SetToolCost` /
`SetToolCostCurrency` 合并（numeric 转 float64 的取值方式与 `modelCost` 相同）；`RequestFinishedView`
增加同名字段。`getToolUsageCost` 在写入数据库之前运行、`requestFinished` 在 meta 行终态写入之后运行，
所以后者读到的是脚本自己提交的值。

与 `requestFinished` 相同的已知边界依然存在：meta 响应 artifact 在这两条路径上都先于数据库写入完成上传，
所以本 hook 里的 `console.*` 输出会进入 logx，但不会出现在 artifact 的日志列表中。

## 不在本次范围

概览与 trace 的成本聚合仍然只统计 `model_cost`；`tool_cost` 只在请求详情页展示（dashboard 与
`RequestView` 的读取侧已经就绪，本次不修改前端、openapi 和 sqlc）。
