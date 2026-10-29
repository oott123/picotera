# 请求自定义 Annotations — API 设计

## 脚本 API（`ctx.metaRequest.annotations` / `ctx.upstreamRequest.annotations`）

两个 Proxy 对象，读写直通 Go 侧累加器，像普通对象一样操作：

```js
// meta 行（type=0）注解：session 全程可用，请求终结时持久化一次
ctx.metaRequest.annotations.agent = 'claude-code';       // 写入/覆盖
const v = ctx.metaRequest.annotations.agent;             // 读取，缺失为 undefined
delete ctx.metaRequest.annotations.agent;                // 删除
Object.keys(ctx.metaRequest.annotations);                // 枚举
JSON.stringify(ctx.metaRequest.annotations);             // 序列化快照

// upstream 行（type=1）注解：自每次 attempt 开始（beforeRequest 起）可用，
// attempt 终结时持久化到该 attempt 的 upstream 行；每个 attempt 从空开始
ctx.upstreamRequest.annotations.route = 'fallback';
```

语义与校验（fail fast）：

- 写入：key 必须是非空字符串，value 必须是字符串；违规抛 `TypeError`（不做隐式转字符串）。无条数/长度上限。
- `ctx.upstreamRequest` 在第一次 attempt 开始前为 `undefined`（`sortProviders` / `rewriteModel` 阶段只有 `ctx.metaRequest` 可用）。
- 每次 attempt 开始时 upstream 注解重置为空；`beforeRequest` 返回 `next: true` 跳过的 attempt 没有 upstream 行，其间写入的 upstream 注解被丢弃。
- `afterUpstreamError`（含流式 `streamed=true`）中的写入归属当前 attempt 的 upstream 行。
- 与 `ctx.annotations`（model/provider/apiKey 配置层注解的合并只读视图）无关，是两套东西。

示例：

```js
picotera.hooks.beforeRequest.tap('tag', (ctx, input) => {
  const ua = (ctx.request.headers['User-Agent'] || [])[0] || '';
  if (ua.includes('claude-cli')) {
    ctx.metaRequest.annotations.agent = 'claude-code';
  }
  ctx.upstreamRequest.annotations.attempt_provider = ctx.provider.name;
});
```

## 管理 API 变更

### `GET /api/picotera/requests` — 新增查询参数 `annotations`

| 参数 | 类型 | 说明 |
|---|---|---|
| `annotations` | string（URL 编码的 JSON 对象） | 按请求注解过滤。多对为 AND 精确匹配；meta 行与 upstream 行均可命中，可叠加 `type` 参数区分。 |

示例：列出最近 30 天所有 `agent == "claude-code"` 的请求：

```
GET /api/picotera/requests?annotations=%7B%22agent%22%3A%22claude-code%22%7D
```

多条件（AND）：

```
GET /api/picotera/requests?annotations={"agent":"claude-code","team":"infra"}   （URL 编码后发送）
```

行为：

- 校验：`annotations` 必须是合法 JSON 对象、至少一对、所有值为 JSON 字符串；否则 `400`。
- 时间窗：携带 `annotations` 且未传 `startAt` 时，默认查询最近 30 天；显式传入 `startAt`/`endAt` 原样生效（与 `requestId` 检索行为一致）。
- 与其余过滤参数（`model`、`projectId`、`finishReason`、游标分页等）可自由组合。

### `RequestView` — 新增字段

```jsonc
{
  "id": "...",
  // ...现有字段...
  "annotations": { "agent": "claude-code" }   // 可选；未打标的行省略
}
```

出现在 `GET /api/picotera/requests`（列表）、`GET /api/picotera/requests/{id}`（详情）、`GET /api/picotera/requests/{id}/spans` 的响应中；meta 行与 upstream 行各自返回自己的注解。

### OpenAPI

按既有流程重新生成：`mise run openapi` → `pnpm --dir dashboard generate-openapi`。dashboard 仅更新生成的 TS 类型，无 UI 改动。
