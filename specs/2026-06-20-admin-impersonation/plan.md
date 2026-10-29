# 执行计划：管理员扮演用户

## 服务端

### 1. `pkg/auth/auth.go`
- 新增常量 `ImpersonationHeader = "X-PicoTera-Impersonation-User-Id"`。
- 新增错误：`ErrImpersonationForbidden`、`ErrImpersonationBadID`、`ErrImpersonationTargetNotFound`。
- 新增 import `strconv`。
- 新增方法 `ResolveWithImpersonation(ctx, req) (*db.AppUser, error)`：先 `Resolve` 取真实用户；Header 空 → 返回真实用户；非管理员 → `ErrImpersonationForbidden`；`strconv.ParseInt` 失败 → `ErrImpersonationBadID`；`GetUserByID` 为 `pgx.ErrNoRows` → `ErrImpersonationTargetNotFound`；成功 → 返回目标用户（不校验 `disabled`）。

### 2. `pkg/auth/middleware.go`
- 改为调用 `resolver.ResolveWithImpersonation(r.Context(), r)`。
- 按错误类型映射状态码：`ErrImpersonationForbidden`→403、`ErrImpersonationBadID`→400、`ErrImpersonationTargetNotFound`→404、其余→401；分别写对应 JSON 响应体。

## 前端

### 3. `src/stores/impersonation.ts`（新增）
- Pinia store：state `target: { userId: number; displayName: string } | null`；getter `isImpersonating`；action `start(user)`、`stop()`。内存态，不持久化。

### 4. `src/api/plugin.ts`
- 在 `createApi` 内对 client 调用 `.use({ onRequest })`：读取 `useImpersonationStore()`，若 `target` 非空则 `request.headers.set('X-PicoTera-Impersonation-User-Id', String(target.userId))`，返回 `request`。

### 5. `src/ui/icons/paths.ts`
- 新增 `mask`（tabler `mask`）图标路径，并加入 `IconName` 与 `iconComponents`。

### 6. `src/views/UsersView.vue`
- 引入 `useRouter`、`useImpersonationStore`、`useMe`。
- 每行操作区新增“扮演”`IconButton`（`Icon name="mask"`），`u.id === me?.id` 时禁用，`title`/`aria-label` 为“扮演此用户”。
- 点击处理：`impersonation.start({ id, displayName })` → `await router.push({ name: 'overview' })` → `await queryClient.invalidateQueries()`。

### 7. `src/components/AppSidebar.vue`
- 引入 `useImpersonationStore` 与 `Tag`、`IconButton`、`Icon`。
- 用户名区域：`isImpersonating` 时显示 `target.displayName` + `Tag`（accent，“扮演中”）+ “还原身份”`IconButton`（`arrow-left`）；否则维持现状显示 `me.displayName`。
- 还原点击：`impersonation.stop()` → `await queryClient.invalidateQueries()`。

## 验证

- `go build ./...` 通过。
- `pnpm --dir dashboard type-check` 与 `pnpm --dir dashboard lint` 通过。
- 手动验证（http-header 鉴权 + 管理员）：
  1. 用户页点击扮演普通用户 → 跳转 overview，用户名变为该用户，出现“扮演中”标识与还原按钮，管理员导航消失，数据（请求/概览/项目）为该用户范围。
  2. 点击还原 → 用户名恢复管理员，管理员导航恢复，数据恢复。
  3. 扮演期间“测试”请求不带 Header（抓包/服务端确认按真实管理员处理）。
  4. 直接对管理接口伪造非管理员 + Header → 403。
- 无需 `mise run openapi` / `generate-openapi`（无 contract 改动）。
