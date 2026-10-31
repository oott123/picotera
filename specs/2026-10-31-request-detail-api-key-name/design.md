# Design: 请求详情显示 API Key 名字

## 现状

- `RequestView.apiKeyId` 已经在 `GET /api/picotera/requests/{id}/spans` 的返回中：meta 行在鉴权后由 `UpdateRequestOnHeader` 回填，upstream 行在 `insertUpstreamAttempt` 插入时写入，成功路径再写一次。鉴权前就失败的请求（如缺 key、无效 key）没有 `apiKeyId`。
- 请求与 API Key 都按 `user_id` 隔离，且没有 admin 旁路：当前用户能看到的请求，其 `apiKeyId` 必然指向当前用户自己的 key。因此用户级的 `GET /api/picotera/api-keys`（`listApiKeys`）就能解析出所有可见请求的 key 名。
- `DeleteApiKey` 是物理删除，历史请求行的 `api_key_id` 可能已经没有对应的 key。
- 请求详情的内容组件是 `dashboard/src/components/RequestDetailsContent.vue`，独立页面 `RequestDetailView.vue` 和请求列表的侧栏 `RequestDetailsPanel.vue` 共用它，所以两处都会显示。

## 方案

纯前端改动，不改后端、不改 OpenAPI。

- `RequestDetailsContent.vue` 内部用 `useQuery({ queryKey: queryKeys.apiKeys.all, queryFn: listApiKeys })` 拉取当前用户的 key 列表。这个查询键与 `ApiKeysView`、`OverviewView` 共用，缓存可复用，key 增删改后的 `invalidateApiKeys` 也会让这里自动刷新。
- 由列表构造 `Map<number, string>`（id → name），新增 `apiKeyLabel(id)`，规则与现有的 `providerLabel` 一致：
  - `apiKeyId` 缺失或为 0 → `—`
  - 在 map 里找到 → key 的 `name`
  - 找不到（key 已删除，或列表还没加载完）→ `#<id>`
- 在概览 tab 的「基本信息」grid 里，紧跟在「状态码」之后加一个 `Field label="密钥"`，样式与「渠道」「端点」一致（`font-mono text-sm break-all`）。标签沿用侧边导航和 Overview 维度里的「密钥」。
- 不加跳转或筛选链接：请求列表目前没有 `apiKeyId` 筛选参数。

`listApiKeys` 返回的 `ApiKeyView` 含 key 明文，但它们是当前用户自己的 key，且 `ApiKeysView` 本来就会展示，这里只取用 `name`，不新增任何暴露面。
