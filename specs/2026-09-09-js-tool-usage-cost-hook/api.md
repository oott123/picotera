# API：`picotera.hooks.getToolUsageCost`

面向脚本作者的输入输出规则。REST 和 OpenAPI 无变更。

## 注册

```js
picotera.hooks.getToolUsageCost.tap('price-tools', function (ctx, input) {
  const usage = input.toolUsage
  const searches = usage.find((t) => t.name === 'web_search')?.numRequests ?? 0
  if (!searches) return
  return { toolUsage: usage, toolCost: searches * 0.01, toolCostCurrency: 'USD' }
})
```

## 触发时机

上游响应读取完毕、两行请求记录写入数据库之前，每个成功请求（路径网关与 unified 路由）运行一次。`ctx` 是
当前请求的完整上下文，其中 `ctx.provider` / `ctx.providerModel` / `ctx.upstreamRequest` 都对应本次成功的
那一次尝试。

## 输入

```ts
{
  toolUsage: Array<{
    name: string
    model?: string
    numRequests?: number
    inputTokens?: number
    outputTokens?: number
    numImages?: number
  }>
  toolCost: null
  toolCostCurrency: ''
}
```

`toolUsage` 是抽取器从上游响应归一化出来的用量，上游没有报告时为 `[]`。`toolCost` 与
`toolCostCurrency` 的初始值恒为 `null` 和 `''` —— 服务端不计算工具费用。

## 输出

与输入同形。返回 `undefined`、`null`、`ctx` 或原样的 input 对象即为透传，写入数据库的就是初始值。

| 字段 | 规则 |
| --- | --- |
| `toolUsage` | 数组，元素为对象；条目只允许上表列出的键 |
| `toolUsage[].name` | 非空字符串，必填 |
| `toolUsage[].model` | 出现时必须是字符串 |
| `toolUsage[]` 的各个计数器 | 出现时必须是 `>= 0` 的安全整数 |
| `toolCost` | `null` 或未提供表示不计费；否则是 `[0, 1e14)` 范围内的有限数字 |
| `toolCostCurrency` | `toolCost` 是数字时必须是非空字符串；否则必须未提供或者是 `''` |

违反其中任意一条都会抛出异常。

## 写入语义

- 脚本返回什么就记录什么：返回 `toolUsage: []` 会把抽取到的用量清空为 SQL NULL。
- 值为 `0` 的计数器不会出现在列中；计数器全为 0 的条目被整条丢弃，即使它声明了 `model`。
- `toolCost` 为空时 `tool_cost` 与 `tool_cost_currency` 都是 NULL。
- meta 行与 upstream 行写入同一份结果。

## 出错行为

hook 抛出异常、返回值不符合上述规则、或者 session 已经因为超时失效时：记录一条 warn 级别日志，
`tool_usage` 写入抽取到的原值，两个费用列保持 NULL。请求本身不受影响。

## 与 `requestFinished` 的关系

`requestFinished` 的输入新增 `toolCost: number` 与 `toolCostCurrency: string`，读到的是本 hook 提交的值
（没有提交则是 `0` 和 `''`）；`toolUsage` 同样是本 hook 提交之后的最终数组。
