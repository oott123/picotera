# 执行计划

## 1. 数据库迁移

- 新建 `db/migrations/039_user_annotations.sql`：`ALTER TABLE app_user ADD COLUMN annotations JSONB NOT NULL DEFAULT '{}'::jsonb;`（含 `-- +goose Down` 的 `DROP COLUMN`）。

## 2. sqlc 查询

- `db/queries/user.sql`：
  - `UpdateUser`：在 `SET` 中追加 `annotations = $5`（参数序号顺延，置于 `updated_at = now()` 之前）。
  - `InsertUser`：改为 `INSERT INTO app_user (display_name, is_admin, annotations) VALUES ($1, $2, $3) RETURNING *;`。
- 运行 `sqlc generate`，确认 `db.AppUser` 出现 `Annotations []byte`，`InsertUserParams` / `UpdateUserParams` 出现 `Annotations []byte`。

## 3. 管理 API 契约

- `pkg/contract/user.go`：
  - `UserView` 增加 `Annotations map[string]string \`json:"annotations"\``。
  - `ToUserView` 解码 `u.Annotations`（空/失败 → 空 map，参照 `ToModelView`）。
  - `UserMutateBody` 增加 `Annotations map[string]string \`json:"annotations,omitempty"\``。

## 4. 管理 API 处理器

- `pkg/server/handle_user_admin.go`：
  - `handleCreateUser`：把 `in.Body.Annotations` 编码为 JSONB 传入 `InsertUserParams.Annotations`（nil → `{}`）；后续 disabled 的补充 `UpdateUser` 也带上 annotations。
  - `handleUpdateUser`：把 `in.Body.Annotations` 编码为 JSONB 传入 `UpdateUserParams.Annotations`。
  - 复用一个本地 helper（如 `encodeAnnotations(map[string]string) ([]byte, error)`）或沿用 `pkg/contract` 既有 `From*View` 的编码写法保持一致。

## 5. jsx 类型与上下文初始化

- `pkg/jsx/types.go`：
  - 新增 `UserSummary{ ID int64 \`json:"id"\`; Name string \`json:"name"\`; Annotations map[string]string \`json:"annotations"\`; IsAdmin bool \`json:"isAdmin"\` }`。
  - `ContextPatch` 增加 `User *UserSummary \`json:"user,omitempty"\``。
- `pkg/jsx/session.go`：`ctxInit` 的对象字面量增加 `user: null,`。

## 6. 网关认证回传 user 行

- `pkg/server/gateway_helpers.go`：
  - `authenticateClient` 签名改为同时返回已查到的 `*db.AppUser`（即 disabled 检查处的 `user`），返回 `(*db.ApiKey, *db.AppUser, error)`。
  - 新增 `userSummaryFromRow(*db.AppUser) *jsx.UserSummary`（解码 annotations，空/失败 → 空 map），与 `apiKeySummaryFromRow` 并列。
- 更新 `authenticateClient` 的其他调用点：`handle_model_list.go:24`（忽略新返回值）。

## 7. gatewayAuthState 与回填

- `pkg/server/gateway_flow.go`：
  - `gatewayAuthState` 增加 `UserJS *jsx.UserSummary` 与 `UserAnno map[string]string`。
  - `authenticateAndBackfill`：接收 `authenticateClient` 的 user 行，构建 `UserJS = userSummaryFromRow(user)`、`UserAnno = UserJS.Annotations`，存入 `f.auth`。
  - 首次 `PatchContext`（约 354 行附近）增加 `User: f.auth.UserJS`。
  - 三处 `annotations.Merge(f.model.Annotations, f.auth.APIKeyAnno)`（约 354 / 402 / 437 行）改为 `annotations.Merge(f.model.Annotations, f.auth.UserAnno, f.auth.APIKeyAnno)`。

## 8. 候选注解合并

- `pkg/server/annotations.go`：
  - `candidateAnnotationsBuilder` 增加 `userAnno map[string]string` 字段。
  - `newCandidateAnnotationsBuilder(userAnno, modelAnnoRaw, apiKeyAnno)`：增加 `userAnno` 参数（nil → `{}`）。
  - `merge`：改为 `annotations.Merge(b.modelAnno, provider, entryAnno, b.userAnno, b.apiKeyAnno)`，并更新注释中的顺序说明。
- `pkg/server/gateway_flow_candidates.go`：
  - `buildPathCandidateSet` / `buildUnifiedCandidateSet` 增加 `userAnno map[string]string` 参数，传入 `newCandidateAnnotationsBuilder`。
- 调用点：
  - `handle_gateway.go:68` → `buildPathCandidateSet(providers, auth.UserAnno, auth.APIKeyAnno, nil, endpoint)`。
  - `handle_unified_gateway.go:47` → `buildUnifiedCandidateSet(providers, auth.UserAnno, auth.APIKeyAnno, nil, virtualEndpoint)`。

## 9. 重新生成 OpenAPI 与前端类型

- `mise run openapi`（写入 `openapi.yaml`）。
- `pnpm --dir dashboard generate-openapi`（写入 `dashboard/src/openapi-types.d.ts`）。

## 10. 仪表盘

- `dashboard/src/components/UserForm.vue`：引入 `AnnotationsEditor.vue`，绑定 annotations 状态，纳入创建/更新提交体；编辑时从 `UserView.annotations` 初始化。

## 11. 测试与验证

- 扩展 `pkg/server/annotations` 相关单测（若存在 builder 测试）覆盖 user 层优先级：`user` 覆盖 `model` / `provider` / `entry` 同名键，但被 `apiKey` 覆盖；user 独有键保留。
- `go build ./...` 与 `go test ./pkg/server/... ./pkg/annotations/...`。
- `pnpm --dir dashboard type-check && pnpm --dir dashboard build`。
- 手动：脚本中读取 `ctx.user.id/name/annotations/isAdmin` 与合并后的 `ctx.annotations`。

## 验收标准

- `app_user` 有 `annotations` 列；用户 CRUD 可读写 annotations。
- jsx `ctx.user` 暴露 `id` / `name` / `annotations` / `isAdmin`。
- `ctx.annotations` 与候选注解的合并顺序为 `model < provider < entry < user < apiKey`。
- 后端构建、相关单测、前端 type-check / build 全部通过。
