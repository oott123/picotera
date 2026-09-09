# getToolUsageCost JS hook

增加 `getToolUsageCost` js hook 点，输入和输出为 `{ toolUsage, toolCost, toolCostCurrency }`。经过 js hook
之后，重新序列化成 toolUsage 格式，并且以 js 提交的结果为准，提交到数据库中。

## 澄清后的决定

以下是规划期间与用户确认的补充要求，作为原始需求的一部分：

1. **触发范围：成功路径上无条件运行。** `completeGatewaySuccess`（路径网关）与 `unifiedStreamSuccess`
   （unified 路由）在写入数据库之前各运行一次，抽取不到工具用量时初始值为 `toolUsage: []`，脚本可以在
   上游没有报告用量的情况下补记用量与费用。代价是每个成功请求多一次 QuickJS eval，开销与
   `requestFinished` 相当。

2. **hook 抛出异常或返回值校验不通过：记录日志并回退到抽取值。** 与 `requestFinished` /
   `afterUpstreamError` 一致 —— 响应此时已经发送给客户端，无法再让请求失败。`tool_usage` 写入抽取到的
   原值，`tool_cost` / `tool_cost_currency` 保持 NULL，logx 记录一条 warn 级别日志。

3. **抽取器的「计数器全为 0 就丢弃整条」规则同样适用于脚本输出。** 脚本返回的条目里，如果
   `numRequests` / `inputTokens` / `outputTokens` / `numImages` 全部为 0 或者未提供，这一条不写入数据库，
   声明了 `model` 也一样。

4. **`tool_cost` 同步暴露给 `requestFinished`。** `metaOutcome` 增加 `toolCost` / `toolCostCurrency`
   镜像字段，`RequestFinishedView` 增加同名字段。CLAUDE.md 里「nothing writes them yet」的理由此次不再
   成立，一并补上。
