# 设计

## 目标

让脚本 ID 成为操作者可控的 slug：创建时可选指定（不指定则随机生成），编辑时可修改。

## Slug 校验

- 规则：`^[a-zA-Z0-9_-]+$`，长度 1–64。
- 在 `pkg/contract/script.go` 新增 `ValidateScriptID(id string) error`，返回结构化错误供 handler 转成 400。
- 严格校验，不 trim、不 case-fold、不对空串做默认填充（生成随机 ID 的逻辑只在「未提供 ID」分支触发，不属于归一化）。

## 数据层

`script` 表结构无需变更（`id` 已是 `TEXT PRIMARY KEY`）。

`db/queries/script.sql` 的 `UpdateScript` 改为允许修改主键：

```sql
-- name: UpdateScript :one
UPDATE script
SET id = $2, name = $3, source = $4, enabled = $5, updated_at = now()
WHERE id = $1
RETURNING *;
```

参数语义：`$1` = 旧 ID（定位行），`$2` = 新 ID（目标 slug，可与旧 ID 相同）。改主键时若 `$2` 与已有行冲突，Postgres 抛唯一约束违例，handler 映射为 409。

`InsertScript` 维持不变（已接受 `id` 入参）。

改 schema 后运行 `sqlc generate` 重新生成 `pkg/db/`。

## 契约层

`ScriptMutateBody` 增加 `ID` 字段：

```go
type ScriptMutateBody struct {
    ID      string `json:"id"`
    Name    string `json:"name"`
    Source  string `json:"source"`
    Enabled bool   `json:"enabled"`
}
```

- 创建：`Body.ID` 可空。为空 → 服务端 `xid.New().String()` 生成；非空 → 校验格式后使用。
- 编辑：`Body.ID` 为目标 ID，必填且必须合法（表单会预填当前 ID）。

## Handler 行为（`pkg/server/handle_script.go`）

### handleCreateScript

1. 校验语法（保持现状）。
2. 解析 ID：`Body.ID` 为空 → 生成 xid；非空 → `ValidateScriptID`，失败返回 400。
3. `InsertScript`。若主键冲突（唯一约束违例）返回 409。

### handleUpdateScript

1. 校验语法（保持现状）。
2. `ValidateScriptID(Body.ID)`，空或非法返回 400。
3. `UpdateScript`（旧 ID = path 的 `in.ID`，新 ID = `in.Body.ID`）。
   - `pgx.ErrNoRows` → 404（旧 ID 不存在）。
   - 唯一约束违例（新 ID 已被其他脚本占用）→ 409。

唯一约束违例通过检查 `*pgconn.PgError` 且 `Code == "23505"` 识别。

## 仪表盘（`dashboard/src/components/ScriptForm.vue`）

- 表单 `form` 增加 `id` 字段。
- 创建模式：显示「ID（可选）」输入框，占位提示「留空自动生成」，可填 slug。
- 编辑模式：ID 输入框从 readonly 改为可编辑，预填当前 ID。
- 提交时把 `id` 一并放入 mutation body。`createScript` / `updateScript` 的 body 类型由 `ScriptMutateBody` 生成，重新生成 TS 类型后自动带上 `id`。
- 错误（400/409）由现有 `error` 展示逻辑显示。

`ScriptsView.vue` 的 `toggle` mutation 也通过 `ScriptMutateBody` 传 body：需补上 `id: script.id`（保持 ID 不变），否则改 schema 后会把 ID 置空。

## 流程顺序

后端契约改动后须依次执行 `mise run openapi` 与 `pnpm --dir dashboard generate-openapi`，再改前端。
