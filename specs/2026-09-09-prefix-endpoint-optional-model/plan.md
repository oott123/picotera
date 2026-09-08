# 执行计划

## 1. 模型提取器改签名（`pkg/server/gateway_helpers.go`）

新增 body 分支的公共实现：

```go
// modelFromBody resolves the body's model field into a routing decision.
// optional (prefix-style entries) turns an absent field into no-model routing;
// a present-but-invalid value is always a 400.
func modelFromBody(body []byte, modelPath string, optional bool) (gatewayModelMode, error)
```

- `!res.Exists()` 且 `optional` → 返回零值 `gatewayModelMode{}`（`HasModel = false`），无错误。
- `!res.Exists()` 且非 optional → 400 `MODEL_NOT_FOUND`，`message: "model not found in request body"`。
- `res.Str == ""`（含空串、`null`、数字、对象等非字符串）→ 400 `MODEL_NOT_FOUND`，`message: "model in request body must be a non-empty string"`。
- 否则 → `gatewayModelMode{OriginalModel: res.Str, HasModel: true}`。

`extractModel` 改为 `func extractModel(body []byte, modelPath string, pathVars map[string]string, optional bool) (gatewayModelMode, error)`：`{name}` 分支保持原样（取到变量返回 `HasModel: true`，取不到返回原有 400），其余委托给 `modelFromBody`。同步更新函数上的注释。

## 2. 路径网关接线（`pkg/server/handle_gateway.go`）

`newPathGatewayFlowConfig` 的 `ExtractModel` 闭包：

```go
ExtractModel: func(_ *http.Request, body []byte, vars map[string]string) (gatewayModelMode, error) {
    if endpoint.ModelPath == "" {
        return gatewayModelMode{}, nil
    }
    return extractModel(body, endpoint.ModelPath, vars, endpoint.PrefixMatch)
},
```

## 3. 端点配置校验（`pkg/server/handle_endpoint.go`）

在 `handleUpsertEndpoint` 已有的 `if input.Body.PrefixMatch { … }` 分支内追加：`modelPath` 若匹配 `pathVarRe`（单个 `{name}`），返回 400，说明前缀端点的路径不含变量、该配置永远取不到模型。

## 4. 统一路由标记前缀挂载（`pkg/server/unified_routes.go`）

`unifiedRoute` 新增字段：

```go
// PrefixMount reports whether this route came from a wildcard prefix mount,
// whose sub-paths are open-ended: a request body without a model field routes
// as no-model instead of 400.
PrefixMount bool
```

`codexUnifiedRoute` 置 `PrefixMount: true`；`unifiedRoutes` 中的六条固定路由不置位。

## 5. 统一路由提取与候选解析（`pkg/server/gateway_unified_helpers.go`）

- `extractUnifiedModel(route, r, body) (gatewayModelMode, error)`：Gemini 分支保持原有 400；其余委托 `modelFromBody(body, "model", route.PrefixMount)`。更新注释。
- `resolveProvidersByTypes` 签名改为 `(ctx context.Context, mode gatewayModelMode, types []int32, srcType int32)`：`mode.HasModel` 为真时走 `GetProvidersByEndpointTypesAndModel`，否则走新的 `GetProvidersByEndpointTypes` 并用 `fromNoModelTypesRow` 投影；空结果的错误消息按分支区分（`no provider available for model` / `no provider available`）。去重、有效性过滤、优先级排序保持在函数尾部共用。
- 新增 `fromNoModelTypesRow(r db.GetProvidersByEndpointTypesRow) db.GetProvidersByEndpointTypesAndModelRow`，逐字段搬运（列结构一致）。

## 6. 无模型的类型集合查询（`db/queries/routing.sql` → `sqlc generate`）

紧邻 `GetProvidersByEndpointTypesAndModel` 新增姊妹查询：

```sql
-- name: GetProvidersByEndpointTypes :many
-- Sister query to GetProvidersByEndpointTypesAndModel for requests that carry
-- no model (prefix-style unified mounts). Model-related columns are flattened
-- to constants so both shapes project onto one row type.
SELECT
  ''::text AS model_name,
  p.id AS provider_id,
  pe.endpoint_path,
  e.endpoint_type AS endpoint_type,
  e.prefix_match AS prefix_match,
  ''::text AS upstream_model_name,
  0::int AS priority,
  '{}'::jsonb AS annotations,
  p.name AS provider_name,
  p.credentials AS provider_credentials,
  p.priority AS provider_priority,
  pe.upstream_url,
  pe.credentials_resolver AS send_credentials_resolver,
  p.proxy_url,
  p.insecure_tls,
  p.annotations AS provider_annotations,
  '{}'::jsonb AS model_annotations,
  p.supports_native_web_search
FROM provider AS p
JOIN provider_endpoint AS pe ON pe.provider_id = p.id
JOIN endpoint AS e ON e.path = pe.endpoint_path
WHERE e.endpoint_type = ANY(sqlc.arg('endpoint_types')::int[])
  AND p.disabled = FALSE;
```

跑 `sqlc generate`，确认 `pkg/db/querier.go` 出现新方法、生成的行类型字段与 `GetProvidersByEndpointTypesAndModelRow` 一一对应。

## 7. 统一网关接线（`pkg/server/handle_unified_gateway.go`）

`newUnifiedGatewayFlowConfig`：`ExtractModel` 闭包直接返回 `extractUnifiedModel(route, req, body)`；`ResolveCandidates` 改为把 `mode` 传给 `resolveProvidersByTypes`。

## 8. 透传 body 不注入空模型（`pkg/server/gateway_flow_attempts.go`）

`buildRewrittenUpstreamRequest` 的统一路由分支里，`SetBodyModel` 仅在 `input.UpstreamModel != ""` 时调用；为空则保持 `body = f.body`。补一行注释说明无模型请求不得被塞进 `"model": ""`。

## 9. 测试

- `pkg/server/gateway_helpers_test.go` 新增 `modelFromBody` / `extractModel` 表驱动测试：字段缺失 × optional 真假、`{"model":""}`、`{"model":123}`、`{"model":null}`、嵌套 `modelPath`、`{name}` 变量取不到值时 optional 仍为 400。
- `pkg/server/handle_unified_gateway_test.go` 更新 `TestExtractUnifiedModel`、`TestExtractUnifiedModel_Passthrough`、`TestExtractUnifiedModel_GeminiFromPath` 以适配新返回值；把 codex 两条子路径的 `{}` 用例改为断言 `HasModel == false` 且无错误，`{"model":""}` 仍断言 400；embeddings 路由两种坏 body 仍断言 400。
- `pkg/server/gateway_flow_attempts_test.go` 新增用例：统一 codex 透传路由 + `UpstreamModel == ""` 时，`buildRewrittenUpstreamRequest` 产出的 body 与入参逐字节相同，且不含 `model` 键（沿用现有测试的空脚本 `jsx` 会话构造方式）。
- `go build ./... && go test ./pkg/...`。

## 10. Dashboard 文案（`dashboard/src/components/EndpointForm.vue`）

"模型字段路径" 字段下补一句仅在 `form.prefixMatch` 为真时显示的说明：请求体中没有该字段时按无模型路由。无契约变更，不需要重新生成 openapi 类型。

## 11. 文档（`CLAUDE.md`）

- "Prefix matching" 段落补一句：前缀端点上模型字段缺失时降级为无模型路由，字段存在但非法仍 400；前缀端点不接受路径变量形式的 `modelPath`。
- "Unified generation routes" 的 codex 挂载段落补一句：codex 挂载是前缀挂载，缺失 `model` 时按无模型路由并走 `GetProvidersByEndpointTypes`；固定路由不变。
