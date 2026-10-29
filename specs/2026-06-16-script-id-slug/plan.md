# 执行计划

## 1. 数据层

1. 编辑 `db/queries/script.sql`：`UpdateScript` 增加 `id = $2`，其余参数顺延（`name=$3, source=$4, enabled=$5`，`WHERE id=$1`）。
2. 运行 `sqlc generate`，确认 `pkg/db/` 中 `UpdateScriptParams` 新增主键目标字段。

## 2. 契约层（`pkg/contract/script.go`）

3. `ScriptMutateBody` 增加 `ID string json:"id"` 字段（放在最前）。
4. 新增 `ValidateScriptID(id string) error`：校验 `^[a-zA-Z0-9_-]+$` 且长度 1–64，非法返回结构化错误。

## 3. Handler（`pkg/server/handle_script.go`）

5. `handleCreateScript`：
   - 语法校验后，解析 ID：`in.Body.ID` 为空则 `xid.New().String()`，非空则 `ValidateScriptID`（失败 400）。
   - `InsertScript` 用该 ID；唯一约束违例（`23505`）→ 409。
6. `handleUpdateScript`：
   - 语法校验后 `ValidateScriptID(in.Body.ID)`（失败 400）。
   - `UpdateScript`（旧 ID `in.ID`，新 ID `in.Body.ID`）；`pgx.ErrNoRows` → 404，唯一约束违例 → 409。
7. 新增内部辅助函数 `isUniqueViolation(err error) bool`（检查 `*pgconn.PgError` 且 `Code=="23505"`）供两处复用。

## 4. OpenAPI / TS 类型

8. `mise run openapi` 重新生成 `openapi.yaml`。
9. `pnpm --dir dashboard generate-openapi` 重新生成 `dashboard/src/openapi-types.d.ts`。

## 5. 仪表盘

10. `ScriptForm.vue`：
    - `form` 增加 `id: props.script?.id ?? ''`。
    - 创建模式显示「ID」输入框（占位「留空自动生成」）；编辑模式 ID 输入框改为可编辑（去掉 readonly，绑定 `form.id`）。
    - 提交 body 增加 `id: form.value.id`。
11. `ScriptsView.vue`：`toggle` 的 `updateScript` body 补 `id: script.id`。

## 6. 验证

12. `go build -o /dev/null ./cmd/picotera`（编译通过）。
13. `pkg/server/` 已有测试：`go test ./pkg/server/...`。
14. `pnpm --dir dashboard type-check` 与 `pnpm --dir dashboard build`。
15. 手动验证：创建（留空生成 / 指定 slug / 非法 slug 报 400 / 重复 slug 报 409）、编辑改 ID（成功 / 撞已有报 409）。
