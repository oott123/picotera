# Views

创建新页面时，必须在 `src/layouts/AppLayout.vue` 的 `pageMeta` map 中添加对应的路由名称条目，否则页面不会显示标题和副标题。

```ts
const map: Record<string, { title: string; hint: string }> = {
  // 在此添加新页面的 title（标题）和 hint（副标题）
}
```

map 的 key 是路由的 `name`（定义在 `src/router/index.ts`），需与路由名完全一致。
