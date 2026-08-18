# 执行计划：unified OpenAI Embeddings 端点

## 1. 端点类型常量与枚举

**`pkg/contract/endpoint.go`**

1. 常量块追加 `EndpointType_OpenAIEmbedding int32 = 13`（续在 `EndpointType_CodexSearchV1Alpha = 12` 后）。
2. `ToEndpointType`：加 `case "openaiEmbedding": return EndpointType_OpenAIEmbedding`。
3. `FromEndpointType`：加 `case EndpointType_OpenAIEmbedding: return "openaiEmbedding"`。
4. `EndpointView.EndpointType` 的 `enum` tag 在 `codexSearchV1Alpha` 后、`unknown` 前插入 `openaiEmbedding`。

**`pkg/contract/label.go`**

5. `EndpointLabel.EndpointType` 的 `enum` tag 同样插入 `openaiEmbedding`（两处 tag 必须保持一致，否则 openapi 里两个 schema 的枚举会分叉）。

## 2. 路由表

**`pkg/server/unified_routes.go`**

6. `unifiedRoutes` 追加：

```go
// OpenAI Embeddings: no llmbridge format exists (and none is needed) — the
// request and response bytes are forwarded verbatim. Non-streaming only.
{Path: "/api/unified/v1/embeddings", Name: "Unified OpenAI Embeddings", Format: llmbridge.FormatUnknown, SourceType: contract.EndpointType_OpenAIEmbedding},
```

无需改动 `candidateEndpointTypes` / `extractUnifiedModel` / `setUnifiedModel` / `handleUnifiedGenerate` / `upstreamFormatFor`：`passthrough()` 由 `Format == FormatUnknown` 派生，既有透传分支已全部覆盖。`registerEndpoints` 的循环自动注册 POST + OPTIONS。

## 3. 用户消息预览

**`pkg/server/user_message_preview.go`**

7. `extractUserMessage` 的 switch 加分支：

```go
case contract.EndpointType_OpenAIEmbedding:
    return extractEmbeddingUserMessage(body)
```

8. 新增函数（`extractQueryUserMessage` 附近）：

```go
// extractEmbeddingUserMessage pulls a preview out of an OpenAI Embeddings
// body. `input` is either a single string or an array; for an array we take
// the first non-empty string element (token-id arrays yield no preview).
func extractEmbeddingUserMessage(body []byte) (string, bool) {
    root, ok := decodeJSONObject(body)
    if !ok {
        return "", false
    }
    switch input := root["input"].(type) {
    case string:
        if input == "" {
            return "", false
        }
        return input, true
    case []any:
        for _, item := range input {
            if s, ok := item.(string); ok && s != "" {
                return s, true
            }
        }
    }
    return "", false
}
```

## 4. 测试

**`pkg/server/response_extractor_test.go`**

9. 新增 `TestExtractEmbeddingUsage`：喂入真实 embedding JSON 响应体（`{"object":"list","data":[…],"model":"text-embedding-3-small","usage":{"prompt_tokens":8,"total_tokens":8}}`），断言 `InputTokens == 8`、`OutputTokens == nil`、`CacheReadTokens == nil`、`TTFTMs == nil`、`InferredModel == "text-embedding-3-small"`。这条测试是「不改抽取代码」这一决策的守门人。

**`pkg/server/handle_unified_gateway_test.go`**

10. 路由表用例（约 69–73 行的表）追加一行：`{"/api/unified/v1/embeddings", llmbridge.FormatUnknown, contract.EndpointType_OpenAIEmbedding, true}`。
11. `TestCandidateEndpointTypesPassthrough` 的 map 追加 `"/api/unified/v1/embeddings": contract.EndpointType_OpenAIEmbedding`。
12. `TestExtractUnifiedModel_Passthrough` 的路径列表追加 `"/api/unified/v1/embeddings"`（用 `{"model":"text-embedding-3-small","input":"hi"}` 作为 body）；约 378 行的另一处透传路径列表同样追加。

**`pkg/server/user_message_preview_test.go`**

13. 加用例：字符串 `input` 取到原文；字符串数组取首个非空元素；`input` 为数字数组时无预览。

14. 跑 `go test ./pkg/...`。

## 5. OpenAPI 与前端

15. `mise run openapi` 重新生成 `openapi.yaml`（`endpointType` 枚举扩容）。
16. `pnpm --dir dashboard generate-openapi` 重新生成 `dashboard/src/openapi-types.d.ts`。
17. **`dashboard/src/api/index.ts`**：
    - `ENDPOINT_TYPES_MODEL_ROUTED` 追加 `'openaiEmbedding'`；
    - `ENDPOINT_TYPE_LABELS` 追加 `openaiEmbedding: 'OpenAI 特征提取'`。
18. **`dashboard/src/utils/requestLabels.ts`**：`UNIFIED_ENDPOINT_NAMES` 追加 `'/api/unified/v1/embeddings': 'OpenAI 特征提取'`。
19. **`dashboard/src/lib/testBody.ts`**：只更新 `endpointTypeToFormat` 上方注释里「测试台无法构造请求体的类型」清单，把 `openaiEmbedding` 列进去（代码走 `default` 分支，无需改逻辑）。
20. `pnpm --dir dashboard type-check`、`pnpm --dir dashboard lint`。

## 6. 文档

21. **`CLAUDE.md`**：
    - 「Unified generation routes」章节：把「Eight chi routes」改为「Nine chi routes」，路由清单追加 `POST /api/unified/v1/embeddings — OpenAI Embeddings source, **passthrough** (non-streaming)`；「Passthrough routes」段落把类型清单从 `codexCompact = 11, codexSearchV1Alpha = 12` 扩为含 `openaiEmbedding = 13`。
    - 「Database Schema」章节末尾关于 migration 047 / `completion_endpoint_path` 的描述补一句：`openaiEmbedding` (13) 与 `/api/unified/v1/embeddings` 同样留在视图外，理由与 `codexSearchV1Alpha` 相同。
22. **`README.md`**：若其中列出了 unified 路由清单，同步追加一条。

## 不做的事

- 不新增数据库迁移（`completion_endpoint_path` 的白名单是显式枚举，类型 13 与新路径天然在外）。
- 不改 `pkg/server/response_extractor.go`（现有 `prompt_tokens` 分支已覆盖，实测确认）。
- 不改 `pkg/llmbridge/` / `pkg/llmbridgeimpl/`（无格式、无转换器）。
- 不改 `pkg/server/gateway_unified_helpers.go`、`handle_unified_gateway.go`、`server.go`（透传分支已泛化）。
- 不改 `EndpointForm.vue` / `TestView.vue` / `conversation.ts`。
