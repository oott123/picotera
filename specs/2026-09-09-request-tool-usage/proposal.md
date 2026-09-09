# 请求表 tool_usage / tool_cost 列

为请求表增加 `tool_usage` `tool_cost` `tool_cost_currency` 三列。其中 tool_usage 是 JSONB，大概是
`Array<{name: string; num_requests?: number; input_tokens?: number; output_tokens?: number; num_images?: number;}>`
这样的。tool_cost 和 tool_cost_currency 就和 model_cost、model_cost_currency 一样就行。

然后解析 usage 的时候，除了尝试找到响应 SSE json 里的 `usage` 字段，也尝试读取并解析 `tool_usage` 字段。示例：

```json
{
  "tool_usage": {
    "image_gen": {
      "input_tokens": 222,
      "input_tokens_details": { "image_tokens": 0, "text_tokens": 222 },
      "output_tokens": 1630,
      "output_tokens_details": { "image_tokens": 1630, "text_tokens": 0 },
      "total_tokens": 1852
    },
    "web_search": { "num_requests": 1 }
  }
}
```

费用先不计算，后续再说。

## 澄清后的决定

以下是规划期间与用户确认的补充要求，作为原始需求的一部分：

1. **`tool_usage` 的位置：它是各格式 `usage` 对象的同级兄弟。** 不在 `usage` 内部。

   > 本条在实现后经真实上游数据（Codex `/responses` 请求 `dag9d1gs9a269lib21cg` 的响应 artifact）
   > 更正。此前误记为「只在事件顶层出现」，与实际相反。

   - OpenAI Chat Completions / Anthropic / Gemini / 非流式 body：`usage`（或 `usageMetadata`）在
     payload 顶层，`tool_usage` 也在顶层。
   - OpenAI Responses SSE：`usage` 在 `response.usage`，`tool_usage` 就在 `response.tool_usage`，
     同样在 `response` 信封里。

   因此原始需求中「把 tool_usage 合并到 usage json 里，一起传到解析函数里」这一实现手法仍不适用 ——
   合并要为每种格式各写一份路径。改为在同一批解析调用点扫描一个固定的候选作用域表
   （payload 顶层 → `response`，先命中先用），两个作用域覆盖全部四种格式，共用一份代码。作用域
   而非完整路径，是因为澄清 #6 还要在同一层里读 `tool_usage` 的兄弟 `tools`。

   另注：Responses 流会在 `response.created` / `response.in_progress` / `response.completed`
   **每个事件都重复** `tool_usage`，前两次通常是全 0，只有 `completed` 带最终计数 —— 这正是
   「last non-empty wins」覆盖语义存在的意义。

2. **归一化采用白名单映射。** 每个条目只取 `name` 加上 `num_requests` / `input_tokens` /
   `output_tokens` / `num_images` 四个可选整数字段，`total_tokens`、`input_tokens_details`
   等一律丢弃。上例落库为：

   ```json
   [
     { "name": "image_gen", "inputTokens": 222, "outputTokens": 1630 },
     { "name": "web_search", "numRequests": 1 }
   ]
   ```

   字段名采用 camelCase（`numRequests` / `inputTokens` / `outputTokens` / `numImages`），与仓库中
   sqlc、contract view、jsx view 的既有约定一致，使 JSONB 列、REST API、JS hook 三处形状完全相同、
   无需任何大小写转换层。需求描述里的 snake_case 只是示意字段集合。

   > 本条原本还规定「上游报了 0」与「上游没报」要用指针区分。该部分已被下面的澄清 #5 推翻：0 一律
   > 丢弃，两者不再区分。

3. **暴露范围：** `RequestView` 增加 `toolUsage` / `toolCost` / `toolCostCurrency` 字段；请求详情页
   展示工具用量；`requestFinished` hook 的 `RequestFinishedView` 增加 `toolUsage`。

4. **费用列：** 本次建列并在 `UpdateRequest` SQL 与 `requestUpdate` 构建器中打通 setter，但没有任何调用方
   —— `tool_cost` / `tool_cost_currency` 始终为 NULL，等后续计费需求再接。

5. **0 一律丢弃。** 上游列举的是它**支持**的工具而非**实际跑过**的 —— Codex 每个请求都会汇报一条
   四个字段全 0 的 `image_gen`。因此：

   - 白名单字段的值为 `0` → 该字段整个 omit，与「上游没报这个字段」完全等同。
   - 一个工具的四个字段全被 omit（全 0，或本来就是空对象 `{}`）→ **整条不记录**。

   这**推翻了**澄清 #2 里「报了 0 ≠ 没报（指针 `nil` + `omitempty` 区分二者）」的设计。既然两者
   不再需要区分，`ToolUsageEntry` / `ToolUsageEntryView` 的四个字段就从 `*int64` 简化为
   `int64 + omitempty`，指针只是多余的复杂度；OpenAPI 里对应字段也从 nullable 变成普通可选整数。

   上面 `dag9d1gs9a269lib21cg` 那条请求最终落库为 `[{"name":"web_search","numRequests":1}]`。

6. **条目带上该工具声明的 `model`。** 响应里 `tools` 数组与 `tool_usage` 同级，条目形如
   `{"type":"image_generation","model":"gpt-image-2-codex","size":"auto",…}`。按工具名匹配后把
   `model` 一并写进条目（`{"name":"image_gen","model":"gpt-image-2-codex","numImages":1}`）。

   实测（扫描本地全部 1451 份 artifact）得到两个约束：

   - **只有 `image_generation` 带 `model`**，`web_search` / `function` / `custom` / `namespace` /
     `tool_search` 都没有。所以 `model` 是可选字段，缺失是常态。
   - **`tool_usage` 的 key 与 `tools[].type` 的词表对不上**：用量记在 `image_gen` 名下，工具却声明为
     `image_generation`。因此匹配需要一张显式别名表 `toolUsageToolType`（目前只有这一条），
     表里没有的名字按字面匹配（`web_search` 就是字面相等）。

   声明了 model 但用量全 0 的工具**仍然整条丢弃** —— 客户端挂载了但没用过，不算用量。
