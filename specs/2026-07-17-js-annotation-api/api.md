# API 设计

本次没有管理 REST API 变更；全部是脚本（JS hook）可见的 SDK API、jsx Go 接口与 sqlc 查询。

## JS SDK（`globalThis.picotera`）

### picotera.request

```js
picotera.request.setAnnotation(requestId, key, value)
```

- `requestId`: 非空字符串（request 行 id，meta 或 upstream 均可），否则 `TypeError`。
- `key`: 非空字符串，否则 `TypeError`。
- `value`: 字符串（含空串）→ 写入；`null` / `undefined` → 删除该 key；其余类型 → `TypeError`。
- 同步直写 DB；目标行不存在时抛 `Error`（`request <id> not found`）。
- 返回 `undefined`。

### picotera.provider

```js
picotera.provider.get(providerId)            // -> ProviderSummary | null
picotera.provider.setAnnotation(providerId, key, value)
```

- `providerId`: 整数（`Number.isInteger`），否则 `TypeError`。
- `get` 返回与 `ctx.provider` 相同的 Summary 形状（凭据不出 JS 边界）；id 不存在返回 `null`：

```js
{ id: 1, name: "openai", priority: 10, annotations: { tier: "gold" }, disabled: false }
```

- `setAnnotation` 语义同上；写 `provider.annotations` JSONB；行不存在抛 `Error`。

### picotera.apiKey

```js
picotera.apiKey.get(apiKeyId)                // -> ApiKeySummary | null
picotera.apiKey.setAnnotation(apiKeyId, key, value)
```

- `apiKeyId`: 整数，否则 `TypeError`。
- `get` 返回与 `ctx.apiKey` 相同的 Summary 形状（不含原始 key）；不存在返回 `null`：

```js
{ id: 3, name: "team-a", annotations: {}, disabled: false }
```

- `setAnnotation` 写 `api_key.annotations` 并刷新 `updated_at`；行不存在抛 `Error`。

### ctx 变化

- `ctx.metaRequest`——只读标识字段，gateway flow 在认证与 trace upsert 之后 patch 进来（fetch-models 会话为 `null`）。`annotations` 代理移除：

```js
{
  id: "cr9q...",            // meta 行 id
  spanId: "cr9q...",        // 等于 meta 行 id（meta 行的 span_id）
  parentSpanId: "sess-1",   // 入站 session 头；缺失时 null
  traceId: "d1f3...",       // traces.id；无 trace 时 null
}
```

- `ctx.upstreamRequest`：每次尝试开始时复位为 `null`；upstream 行插入后变为同形状对象——`id` 是 upstream 行 id，`spanId` / `parentSpanId` / `traceId` 与 `ctx.metaRequest` 相同（upstream 行的 span_id 即 meta id）。`annotations` 代理移除。

### 新 hook：requestFinished

```js
picotera.hooks.requestFinished.tap("usage", function (ctx, info) {
  picotera.request.setAnnotation(info.requestId, "billing", String(info.modelCost));
});
```

- 触发时机：meta 行的完成原因（finish_reason）更新落库之后、session 销毁之前；每个 gateway/unified 请求至多一次。session 创建之前就失败的请求不触发。
- 返回值忽略（纯观察）。hook 内错误 / 超时仅记日志。
- 输入 `info`（零值表示该字段未发生）：

```ts
{
  requestId: string,          // meta 行 id
  statusCode: number,
  finishReason: number,       // db.FinishReason* 数值码（1=internal, 2=cancelled, 3=eof, 4=headersTimeout, 5=readTimeout, 6=streamError, 7=dashboardCancelled）
  errorMessage: string,       // 成功时 ""
  timeSpentMs: number,
  ttftMs: number,
  inputTokens: number,
  outputTokens: number,
  cacheReadTokens: number,
  cacheWriteTokens: number,
  cacheWrite1hTokens: number,
  modelCost: number,
  modelCostCurrency: string,
  providerId: number,         // 仅成功路径有值
  model: string,
  upstreamModel: string,
}
```

## 宿主函数（`__picotera_*`，内部管线，非公开 API）

沿用「可失败返回 `(value, error)`、void 返回 `error`」的注册惯例；value 用 JSON 编码传递（`""` = 删除 / 不存在）：

```
__picotera_anno_request(requestId string, key string, valueJSON string) error
__picotera_anno_provider(id int, key string, valueJSON string) error
__picotera_anno_apikey(id int, key string, valueJSON string) error
__picotera_get_provider(id int) (json string, error)   // "" = not found
__picotera_get_apikey(id int) (json string, error)     // "" = not found
```

原 `__picotera_anno_get/set/del/keys/has`（slot 路由）与 `__picotera_makeAnnotationsProxy` 删除。

## jsx Go 接口

```go
// 新增（pkg/jsx）
type RequestRef struct {
    ID           string  `json:"id"`
    SpanID       string  `json:"spanId"`
    ParentSpanID *string `json:"parentSpanId"` // null = 无入站 session 头
    TraceID      *string `json:"traceId"`      // null = 无 trace
}

type RequestFinishedView struct {
    RequestID          string  `json:"requestId"`
    StatusCode         int32   `json:"statusCode"`
    FinishReason       int32   `json:"finishReason"`
    ErrorMessage       string  `json:"errorMessage"`
    TimeSpentMs        int32   `json:"timeSpentMs"`
    TtftMs             int32   `json:"ttftMs"`
    InputTokens        int32   `json:"inputTokens"`
    OutputTokens       int32   `json:"outputTokens"`
    CacheReadTokens    int32   `json:"cacheReadTokens"`
    CacheWriteTokens   int32   `json:"cacheWriteTokens"`
    CacheWrite1hTokens int32   `json:"cacheWrite1hTokens"`
    ModelCost          float64 `json:"modelCost"`
    ModelCostCurrency  string  `json:"modelCostCurrency"`
    ProviderID         int32   `json:"providerId"`
    Model              string  `json:"model"`
    UpstreamModel      string  `json:"upstreamModel"`
}

type HostAPI interface {
    SetRequestAnnotation(ctx context.Context, requestID, key string, value *string) error
    SetProviderAnnotation(ctx context.Context, providerID int32, key string, value *string) error
    SetApiKeyAnnotation(ctx context.Context, apiKeyID int32, key string, value *string) error
    GetProvider(ctx context.Context, providerID int32) (*ProviderSummary, error) // (nil, nil) = 不存在
    GetApiKey(ctx context.Context, apiKeyID int32) (*ApiKeySummary, error)       // (nil, nil) = 不存在
}

func NewEngine(cfg Config, store ScriptStore, kvStore kv.Store, hostAPI HostAPI) Engine

// Session 接口变更
SetUpstreamRequest(ref *RequestRef) error                  // nil => ctx.upstreamRequest = null
RunRequestFinished(input RequestFinishedView) error        // 结果忽略
// 移除：MetaAnnotations / UpstreamAnnotations / ResetUpstreamAnnotations

// ContextPatch 新增字段
MetaRequest *RequestRef `json:"metaRequest,omitempty"`
```

## sqlc 查询

```sql
-- db/queries/request.sql
-- name: SetRequestAnnotation :execrows
UPDATE request SET annotations = CASE
    WHEN sqlc.narg('value')::text IS NULL
      THEN NULLIF(COALESCE(annotations, '{}'::jsonb) - sqlc.arg('key')::text, '{}'::jsonb)
    ELSE COALESCE(annotations, '{}'::jsonb) || jsonb_build_object(sqlc.arg('key')::text, sqlc.narg('value')::text)
  END
WHERE id = sqlc.arg('id')::text;

-- db/queries/provider.sql
-- name: SetProviderAnnotation :execrows
UPDATE provider SET annotations = CASE
    WHEN sqlc.narg('value')::text IS NULL THEN annotations - sqlc.arg('key')::text
    ELSE annotations || jsonb_build_object(sqlc.arg('key')::text, sqlc.narg('value')::text)
  END
WHERE id = sqlc.arg('id')::int;

-- db/queries/api_key.sql
-- name: GetApiKeyByID :one
SELECT * FROM api_key WHERE id = $1 LIMIT 1;

-- name: SetApiKeyAnnotation :execrows
UPDATE api_key SET annotations = CASE
    WHEN sqlc.narg('value')::text IS NULL THEN annotations - sqlc.arg('key')::text
    ELSE annotations || jsonb_build_object(sqlc.arg('key')::text, sqlc.narg('value')::text)
  END,
  updated_at = now()
WHERE id = sqlc.arg('id')::int;
```

同时从 `UpdateRequest`（db/queries/request.sql）删除 `annotations = CASE WHEN sqlc.arg('set_annotations') ...` 行。
