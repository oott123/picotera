# API 变更

唯一对外契约变化是枚举值重命名，字段名与结构不变。

## 受影响字段

| 资源 | 字段 | 旧 enum | 新 enum |
|------|------|---------|---------|
| `EndpointView` | `credentialsResolver` | `generalApiKey,bearerToken,xApiKey,searchKey,googApiKey,unknown` | `followRequest,bearerToken,xApiKey,searchKey,googApiKey,unknown` |
| `ProviderEndpointView` | `credentialsResolver` | `unknown,generalApiKey,...` | `unknown,followRequest,...` |
| `ProviderView`（含 upsert 请求体） | `modelsEndpointResolver` | `unknown,generalApiKey,...` | `unknown,followRequest,...` |

## 语义

- `followRequest`（原 `generalApiKey`，整数值仍为 `1`）：凭证发送方式与下游客户端请求携带凭证的位置保持一致；无法从源请求判断时，回退为同时写入 `Authorization`/`X-Api-Key`/`X-Goog-Api-Key` 三个 header。
- 该字段**仅**影响凭证向上游的发送，不再参与网关对客户端凭证的解析。

## 不变项

- 路径、方法、请求/响应结构、DB 列名 `credentials_resolver` / 存储整数值均不变。
- `openapi.yaml` 与 `dashboard/src/openapi-types.d.ts` 按既定流程重新生成。
