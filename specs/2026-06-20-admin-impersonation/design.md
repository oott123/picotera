# 设计：管理员扮演用户

## 概述

管理员可临时以另一个用户身份访问管理 API。机制由两段构成：

1. **服务端**：`auth` 中间件在解析出真实用户后，若真实用户是管理员且请求带有 `X-PicoTera-Impersonation-User-Id`，则将写入请求上下文的用户替换为被扮演用户。下游所有按 `user_id` 隔离的查询、`requireAdmin` 能力闸门、`me` 接口因此全部以被扮演用户为准。
2. **前端**：openapi-fetch 客户端中间件根据全局状态（Pinia store）注入该 Header；用户界面提供发起/还原入口与视觉标识。

不引入任何第三方库；不新增数据库表或迁移；不新增 Huma 操作或 contract 类型——Header 在 chi 层中间件中按原始字符串读取，位于 Huma 之下。

## 信任模型

真实身份永远由既有鉴权来源（single-user-mode / http-header，通常由反向代理注入）解析得出，服务端可信。扮演 Header 是不可信的客户端附加提示，**只有当真实用户已被鉴权为管理员时才生效**。因此即使非管理员伪造 Header 也无法越权——其真实身份仍是非管理员，会被直接拒绝。

## 服务端设计

### `pkg/auth/auth.go`

新增常量与错误类型：

```go
const ImpersonationHeader = "X-PicoTera-Impersonation-User-Id"

var (
    ErrImpersonationForbidden      = errors.New("impersonation forbidden")        // 非管理员携带 Header
    ErrImpersonationBadID          = errors.New("invalid impersonation user id")  // Header 非数字
    ErrImpersonationTargetNotFound = errors.New("impersonation target not found") // 目标用户不存在
)
```

新增方法 `ResolveWithImpersonation`，封装既有 `Resolve` 并叠加扮演逻辑：

```go
func (r *Resolver) ResolveWithImpersonation(ctx context.Context, req *http.Request) (*db.AppUser, error) {
    user, err := r.Resolve(ctx, req)
    if err != nil {
        return nil, err
    }
    raw := req.Header.Get(ImpersonationHeader)
    if raw == "" {
        return user, nil // 未扮演
    }
    if !user.IsAdmin {
        return nil, ErrImpersonationForbidden
    }
    id, perr := strconv.ParseInt(raw, 10, 64)
    if perr != nil {
        return nil, ErrImpersonationBadID
    }
    target, gerr := r.queries.GetUserByID(ctx, id)
    if errors.Is(gerr, pgx.ErrNoRows) {
        return nil, ErrImpersonationTargetNotFound
    }
    if gerr != nil {
        return nil, gerr
    }
    return &target, nil // 允许目标为已禁用用户（管理员排查用途）
}
```

`Resolve` 保持不变（继续解析真实身份）。被扮演用户不做 `disabled` 校验——管理员可借此排查被禁用用户的数据。

### `pkg/auth/middleware.go`

`Middleware` 改为调用 `ResolveWithImpersonation`，并按错误类型映射状态码：

| 错误 | 状态码 | 响应体 |
| --- | --- | --- |
| `ErrImpersonationForbidden` | 403 | `{"message":"forbidden"}` |
| `ErrImpersonationBadID` | 400 | `{"message":"invalid impersonation user id"}` |
| `ErrImpersonationTargetNotFound` | 404 | `{"message":"impersonation target not found"}` |
| 其它（含 `ErrUnauthorized`、`nil` user） | 401 | `{"message":"unauthorized"}` |

成功时 `WithUser` 写入的是被扮演用户，下游逻辑（数据隔离、`requireAdmin`、`handleGetMe`）无需改动即可生效。

### 作用范围

`auth.Middleware` 仅挂载在 `/api/picotera` 子路由（`mgmtRouter`）。网关 catch-all、`/api/unified`、静态资源都不经过它，因此扮演机制天然只对管理接口生效，网关不受影响——无需任何额外开关。

## 前端设计

### 全局状态：`src/stores/impersonation.ts`（新增 Pinia store）

```ts
state: { target: { userId: number; displayName: string } | null }
getters: isImpersonating  // target !== null
actions:
  start(user: { id: number; displayName: string })  // 写入 target
  stop()                                             // 清空 target
```

仅存于内存，不持久化。openapi-fetch 中间件直接 `useImpersonationStore()` 读取（请求发生在 app 挂载后，pinia 已激活）。

### 客户端中间件：`src/api/plugin.ts`

在 `createApi` 内对返回的 client 调用 `.use({ onRequest })`，使所有实例统一注入 Header：

```ts
client.use({
  onRequest({ request }) {
    const store = useImpersonationStore()
    if (store.target) {
      request.headers.set('X-PicoTera-Impersonation-User-Id', String(store.target.userId))
    }
    return request
  },
})
```

`fetchMe` 经由该 client，因此扮演期间 `me` 也返回被扮演用户——`isAdmin`、管理员导航、路由守卫自动切换视角。

“测试”请求（`postTestDirect`、`postGatewayTest`）使用原生 `fetch`、不经过此 client，天然不带 Header，符合需求。

### 发起入口：`src/views/UsersView.vue`

每行新增“扮演”`IconButton`（图标 `mask`），对自己（`u.id === me?.id`）禁用。点击：

```ts
impersonation.start({ id: u.id, displayName: u.displayName })
await router.push({ name: 'overview' })   // 离开被扮演用户无权访问的管理页
await queryClient.invalidateQueries()     // 刷新所有缓存（含 me）
```

先跳转 `/overview` 再失效缓存，避免停留在管理页时 `me` 翻转为非管理员导致守卫与查询冲突。

### 标识与还原：`src/components/AppSidebar.vue`

侧边栏底部用户名区域：当 `impersonation.isImpersonating` 为真时，

- 用户名显示 `impersonation.target.displayName`，并加一个 `Tag`（`variant="accent"`，文案“扮演中”）；
- 用户名右侧显示“还原身份”`IconButton`（图标 `arrow-left`）。点击：

```ts
impersonation.stop()
await queryClient.invalidateQueries()  // me 重新拉取回真实管理员，管理员导航恢复
```

还原按钮可见性由 store 驱动（不依赖 `isAdmin`），因此即便扮演普通用户使 `isAdmin` 翻转为 `false`、管理员导航隐藏，该按钮仍常驻可见。

### 图标：`src/ui/icons/paths.ts`

新增 `mask`（tabler `mask`，威尼斯面具，扮演语义）图标路径并加入 `IconName` 与 `iconComponents`。“还原”复用既有 `arrow-left`。

## 不改动项

- 无数据库迁移、无 sqlc 改动（复用既有 `GetUserByID`）。
- 无 OpenAPI / contract 改动——扮演 Header 在 chi 中间件读取，不是 Huma 操作参数，无需重新生成 `openapi.yaml` 与 TS 类型。
- 不做扮演审计日志：被扮演用户对数据的写入自然归属该用户，符合“以该用户身份操作”的语义。
