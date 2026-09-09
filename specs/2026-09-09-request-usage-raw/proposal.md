# 请求表 usage_raw / tool_usage_raw 列

为请求表增加 `usage_raw` 和 `tool_usage_raw` 两个 JSONB，并将提取到的对应字段保存到表中；同时将这两个字段
暴露给 getToolUsageCost 脚本（放到输入里）和 requestFinished 脚本中（好像是 ctx 里？）。

## 澄清后的决定

以下是规划期间与用户确认的补充要求，作为原始需求的一部分：

1. **`usage_raw` 采用「整对象后到覆盖」语义。** 只保留最后一个非空 `usage` 对象，原样存储，不做任何跨事件
   的合并。字节级忠实于上游某一个事件的原始内容。副作用是 Anthropic 流式会丢掉 `message_start` 里的
   input / cache 明细（`message_delta` 的 `usage` 覆盖了它），这是接受的取舍。

2. **`tool_usage_raw` 保留归一化时被丢弃的全零工具。** 只要上游报告了非空 `tool_usage` 对象就原样记录
   （末次覆盖），包括 Codex 每次响应都带的全零 `image_gen`。归一化列 `tool_usage` 丢掉的信息，正是 raw 列
   存在的意义。

3. **两列同时进入管理 API 与仪表盘。** `RequestView` 增加 `usageRaw` / `toolUsageRaw` 字段；请求详情页在
   「日志」tab 旁边新增一个「用量」tab 展示这两份原始 JSON。

4. **脚本侧的暴露位置是 hook 的 input，不是 `ctx`。** `requestFinished` 本来就没有 ctx 通道 —— 它的输入是
   `RequestFinishedView`；`getToolUsageCost` 的输入是 `ToolUsageCostView`。两处都加同名的
   `usageRaw` / `toolUsageRaw` 字段，与既有的 `toolUsage` 并列。这两个字段对脚本**只读**。

5. **顺带把 `toolUsageScopes` 改成路径表 `toolUsagePaths`**，与新增的 `usageRawPaths` 用同一套扫描写法，
   消除「一个存作用域、一个存路径」的不对称。
