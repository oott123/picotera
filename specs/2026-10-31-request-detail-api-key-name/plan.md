# Plan: 请求详情显示 API Key 名字

设计见 `design.md`。只改 `dashboard/src/components/RequestDetailsContent.vue`。

## Step 1 — 拉取 key 列表并构造标签函数

1. import 里补上 `listApiKeys`（`@/api/client`）。
2. 在 `providersMap` 附近新增：
   ```ts
   const apiKeysQuery = useQuery({ queryKey: queryKeys.apiKeys.all, queryFn: listApiKeys })
   const apiKeyNames = computed(() => {
     const m = new Map<number, string>()
     for (const k of apiKeysQuery.data.value ?? []) m.set(k.id, k.name)
     return m
   })
   ```
3. 在 `providerLabel` 旁新增 `apiKeyLabel(id: number | undefined | null)`：`!id` → `'—'`；命中 → name；否则 `` `#${id}` ``。

## Step 2 — 渲染字段

在概览 tab「基本信息」grid 中，「状态码」`Field` 之后插入：

```vue
<Field label="密钥" as="div">
  <span class="font-mono text-sm break-all">{{ apiKeyLabel(selected.apiKeyId) }}</span>
</Field>
```

## Step 3 — 验证

1. `pnpm --dir dashboard lint`、`pnpm --dir dashboard type-check`、`pnpm --dir dashboard format` 通过。
2. 手动核对（`mise run server` + `mise run web`）：
   - 正常请求的 meta 行和 upstream 行都显示 key 名；
   - 鉴权失败的请求显示 `—`；
   - 删除某个 key 后，它的历史请求显示 `#<id>`；
   - 独立详情页 `/requests/:requestId` 和请求列表侧栏都能看到该字段。
