# API 变更

本次只扩展既有响应体，不新增/修改任何操作、路径或查询参数。

## `RequestView` 新增字段

影响以下端点的响应体（三者都返回 `RequestView`）：

- `GET /api/picotera/requests` — 列表
- `GET /api/picotera/requests/{id}` — 单条
- `GET /api/picotera/requests/{id}/spans` — 同 span 的全部行

```jsonc
{
  // ... 既有字段
  "toolUsage": [
    { "name": "image_gen", "inputTokens": 222, "outputTokens": 1630 },
    { "name": "web_search", "numRequests": 1 }
  ],
  "toolCost": 0.0123,
  "toolCostCurrency": "USD"
}
```

### `toolUsage`

`ToolUsageEntryView[]`，`omitempty`（该行没有工具用量时字段不出现）。

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `name` | `string` | 是 | 工具名，来自上游 `tool_usage` 对象的 key |
| `numRequests` | `int64` | 否 | 调用次数 |
| `inputTokens` | `int64` | 否 | 输入 token |
| `outputTokens` | `int64` | 否 | 输出 token |
| `numImages` | `int64` | 否 | 图片张数 |

**只有非 0 的计数会出现。** 上游报了 `0` 与上游没报该项一律视为同一件事，字段直接缺席（不是 `null`）；
四项全部缺席的工具整条不出现在数组里 —— 上游列举的是它支持的工具，没跑过的不记录。
数组顺序即上游 `tool_usage` 对象的字面顺序。

### `toolCost` / `toolCostCurrency`

`*float64` / `string`，`omitempty`，语义与 `modelCost` / `modelCostCurrency` 一致。

本次没有任何写入方，实际返回中恒为缺席。

## JS 脚本：`requestFinished` hook 输入

`RequestFinishedView` 增加 `toolUsage`，形状与上表相同，且**恒存在**：没有工具用量时是 `[]`，
脚本可以无条件遍历。

```js
picotera.hooks.requestFinished.tap('tool-accounting', (ctx, view) => {
  for (const t of view.toolUsage) {
    console.log(t.name, t.numRequests ?? 0, t.inputTokens ?? 0)
  }
})
```

`toolCost` / `toolCostCurrency` 不进入这个 view。

## 生成物

改完 `pkg/contract/` 后必须依次执行：

```bash
mise run openapi                        # 重写 openapi.yaml
pnpm --dir dashboard generate-openapi   # 重写 dashboard/src/openapi-types.d.ts
```
