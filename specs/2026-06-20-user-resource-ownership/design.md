# 设计：多用户资源归属

## 目标

把三类数据归属到用户并在读取路径上按用户隔离：

- **API Key**：归创建者所有。
- **request**（hypertable）：归该请求 API Key 所属用户所有。
- **traces**：归该追踪首个上游请求的用户所有。

其它资源保持全局共享。所有列举/单查/过滤/概览/实时查看/中断都按当前用户隔离，不设管理员旁路。

## 归属来源

- **管理 API**（`/api/picotera`）：用户来自 `auth.UserFromContext(ctx)`（已有的 auth 中间件解析）。
- **网关与 unified**（`/`、`/api/unified`）：用户来自 API Key 的 `user_id` 列。`GetApiKeyByKey` 返回的行新增 `user_id`，网关据此把用户写入请求 context 与各行的 `user_id`。

API Key 与用户是多对一：一把 Key 恰好属于一个用户。

## 数据库改动

### 列新增（迁移 035，事务内）

- `api_key.user_id BIGINT NOT NULL`：`ADD COLUMN ... DEFAULT 1` 回填现有行，随后 `DROP DEFAULT`，使新插入必须显式提供。
- `request.user_id BIGINT`（可空）：`ADD COLUMN ... DEFAULT 1`（元数据级回填现有行为 1），随后 `DROP DEFAULT`。可空是因为元请求（meta）在鉴权前插入，此时用户未知，鉴权后由 `UpdateRequestOnHeader` 回填。
- `traces.user_id BIGINT NOT NULL`：`ADD COLUMN ... DEFAULT 1` 回填现有行，随后 `DROP DEFAULT`。追踪只在鉴权后（用户已知）才创建，故非空。
- 追踪唯一键由 `parent_span_id` 改为 `(parent_span_id, user_id)`：删除现有 `traces_parent_span_id_key` 唯一约束，新增 `UNIQUE (parent_span_id, user_id)`。现有行 `user_id` 均为 1，原有 `parent_span_id` 在复合键下仍唯一，迁移无冲突。

不加外键约束，沿用现有 `user_identity.user_id` 无外键的惯例。回填值固定为 `1`，与需求一致；运营方需保证存在 id 为 1 的用户（全新部署下单用户模式 root 即为 1 号）。

为支持按用户列举/过滤的性能，新增索引：
- `request (user_id, created_at DESC, id DESC)`
- `api_key (user_id)`
- `traces (user_id, last_request_at DESC, id DESC)`

### 连续聚合重建（迁移 036，`NO TRANSACTION`）

概览依赖两个 TimescaleDB 连续聚合 `request_overview_hourly`、`request_speed_hourly`，二者按固定列 `GROUP BY`。连续聚合无法 ALTER 增加分组列，须 drop + recreate（沿用 `025`/`028` 加 `project_id` 的同款模式）：

- 在两个 cagg 的 SELECT 与 `GROUP BY` 中加入 `user_id`。
- cagg 数据源是 `request WHERE type = 1`（上游行），上游行的 `user_id` 在鉴权后插入时即写入，统计可靠。
- Down 迁移重建为不含 `user_id` 的旧定义。

必须先 035 增列、后 036 重建 cagg。

## traces 用户归属机制

`parent_span_id` 来自客户端请求头，是客户端可控值。若仅以 `parent_span_id` 作为追踪唯一键，两个用户使用相同 `parent_span_id`（巧合或恶意构造）会落到同一行追踪，导致请求被并入他人追踪、`ListRequestTraces` 的指标 LATERAL（按 `parent_span_id` + 时间窗聚合）混入他人请求。因此追踪归属以 **`(parent_span_id, user_id)` 复合键**确认：每个用户对同一 `parent_span_id` 拥有各自独立的追踪行，请求只并入与自身 `user_id` 匹配的追踪。

由于复合键要求 `user_id` 非空，而 meta 行在鉴权前插入（用户未知），追踪的创建时机调整为**鉴权后**：

1. 移除 `insertRequest` 内对 meta（鉴权前、`user_id` 为 NULL）的追踪触发——`upsertTrace` 仅在 `user_id` 有效时执行。
2. 在 `authenticateAndBackfill`（鉴权后、网关与 unified 共用）显式调用一次 `upsertTrace`，以 **meta 行的 `created_at` 作为 `first_request_at` 锚点** + 真实 `user_id` 建立追踪。这样时间窗下界仍是 meta 时间，`ListRequestTraces` 中按 `type=0` 取用户消息预览/项目的 LATERAL 仍能命中 meta 行。
3. 后续每个上游行 `insertRequest`（`user_id` 有效）继续触发 `upsertTrace`，经 `ON CONFLICT (parent_span_id, user_id)` 的 `LEAST/GREATEST` 扩展 `last_request_at`。

`UpsertTrace` 增加 `user_id` 参数，`ON CONFLICT` 目标改为 `(parent_span_id, user_id)`。鉴权失败的请求只有 meta 行、不创建追踪，对所有人不可见（符合越权防护）。

`ListRequestTraces` 的全部指标/预览/项目 LATERAL 子查询，除按 `parent_span_id` + 时间窗外，再加 `AND request.user_id = traces.user_id`，确保共用 `parent_span_id` 的两个用户各自只聚合自己的请求。

启动期回填（`ListTraceBackfillCandidates` / `BackfillTrace`）改为按 `(parent_span_id, user_id)` 分组，每组生成一条追踪，`ON CONFLICT (parent_span_id, user_id)` 回填窗口（现有数据 `user_id` 均为 1）。

## 越权防护策略

读取路径一律把当前用户作为**必填**过滤条件注入 SQL，而非可选过滤：

- 列举：`WHERE user_id = $userID`。
- 单查 / 跨 span / 过滤：在原条件基础上 `AND user_id = $userID`；查不到即视为 404 / 不存在，避免泄露他人资源是否存在。
- 实时进度 / 中断：`liveRequests` 是按请求 ID 的内存结构，本身无归属信息。先用按用户限定的 `GetRequest` 校验该行归属，命中后再读取/中断；未命中则返回 `inFlight=false` / `interrupted=false`，不泄露存在性。
- 概览：每条 overview 查询加 `user_id` 必填过滤；cagg 类查询过滤 cagg 的 `user_id` 列，`request r` 类查询过滤 `r.user_id`。

NULL `user_id` 的行（鉴权失败的孤立 meta、未补齐的追踪）不匹配任何用户，自然隐藏。

## 网关用户解析与禁用校验

`authenticateClient` 解析 API Key 后：

1. 读取 `apiKey.UserID`。
2. 以 `GetUserByID` 查所属用户；若 `disabled` 为真，返回 403（与管理 API 对禁用用户的处理一致）。
3. 把 `UserID` 存入 `gatewayAuthState`，写入 context 与后续各行 `user_id`。

写入点：
- `UpdateRequestOnHeader`（meta 行回填用户）。
- 上游行 `insertRequest`（`gateway_flow_attempts.go`）。
- unified 路径的两处 `UpdateRequestOnHeader`（`gateway_unified_helpers.go`，`unifiedStreamArgs` 增加 `userID` 字段）。
- 鉴权后在 `authenticateAndBackfill` 显式调用 `upsertTrace`（meta `created_at` 锚点 + 用户）创建追踪；上游行 `insertRequest` 经 `InsertRequestParams.UserID` 触发的 `upsertTrace` 扩展窗口（仅在 `UserID` 有效时执行）。

## 管理 API 改动

每个受隔离的 handler 读取 `auth.UserFromContext(ctx)`（中间件保证非空，缺失视为接线 bug → 500，与 `handleGetMe` 一致），并把 `u.ID` 传入对应查询：

- API Key：list / get / create（owner = 当前用户）/ update / delete。
- 请求：list / get / list-by-span / list-traces。
- 实时：get-live / interrupt（先校验归属）。
- 概览：summary / distribution / series / speed-boxplot 涉及的全部 overview 查询。

`CreateApiKey` 的 owner 取自 context，不在请求体暴露；`UpdateApiKey` 不提供改派。

## 契约与前端

- `ApiKeyView` 增加 `userId`。`RequestView` 增加 `userId`（行已有该列，便于展示）。
- 重新生成 `openapi.yaml` 与前端 TS 类型。
- 隔离全部在服务端完成，对前端透明，控制台无需功能改动；密钥/请求/概览列表自动只显示本人数据。

## 不做项

- 不做管理员旁路（admin 看全部）。
- 不做密钥改派。
- 不做项目等其它资源的用户归属。
- 不引入第三方库或算法。
