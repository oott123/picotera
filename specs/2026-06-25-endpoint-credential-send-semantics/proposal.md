# 端点凭证处理：发送语义与解析解耦

## 目标

将端点的凭证字段从“凭证解析”重新定义为“凭证发送”，并把网关对客户端 API key 的解析与该字段彻底解耦。

## 具体要求

1. **字段语义改为“凭证发送”**：端点（及 provider_endpoint 覆盖、provider 的模型列表解析器）当前的“凭证解析”字段，其语义收窄为**仅影响凭证向上游的发送方式**，不再参与网关对下游客户端凭证的解析。UI 文案相应改为“凭证发送”。

2. **枚举值重命名 `generalApiKey` → `followRequest`**：含义为“凭证发送方式与下游（客户端）请求携带凭证的方式保持一致”。仅重命名枚举字符串值与 Go 常量，DB 中存储的整数值（=1）不变，无需数据迁移。API/DB 字段标识符 `credentialsResolver` / `credentials_resolver` 保持不变。

3. **网关解析客户端 API key 兼容所有已知方式**：实际解析下游凭证时，不再依赖任何 resolver 设置，统一扫描全部已知位置——`Authorization: Bearer`、`X-Api-Key`、URL search 中的 `key`、`X-Goog-Api-Key`——按固定内部顺序逐个尝试，取首个非空值。

## 已确认的决策

- **字段重命名范围**：仅改 UI 标签 + 枚举值；保留 `credentialsResolver` / `credentials_resolver` 标识符与列名，不做 DB 迁移。
- **解析顺序**：沿用现有 fallback 顺序 `Authorization Bearer → X-Api-Key → ?key= → X-Goog-Api-Key`，取首个非空。
