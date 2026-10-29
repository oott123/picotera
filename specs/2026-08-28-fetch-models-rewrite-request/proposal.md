# 需求：fetchModels 拉取上游模型列表时走 rewriteRequest Hook

让 fetchModels 请求上游 models 列表的时候也走 rewriteRequest Hook 吧。

## 澄清（规划阶段确认）

- **判别标记用 `ctx.endpointType = "fetchModels"`**：复用现有的「路由形态」字段，取值扩为 `"gateway" | "unified" | "fetchModels"`，已有的 rewriteRequest 脚本一行 `if (ctx.endpointType === 'fetchModels') return` 即可跳过这条链路。
- **`ctx.endpoint` 保持 `null`**：models 列表地址来自 `provider.modelsEndpointUrl`，没有对应的 `endpoint` 行；脚本从 `ctx.provider` 与 `pending.url` 取信息。
