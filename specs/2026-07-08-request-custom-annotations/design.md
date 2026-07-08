# 请求自定义 Annotations — 系统设计

## 概述

脚本通过 `ctx.metaRequest.annotations` 和 `ctx.upstreamRequest.annotations` 两个 Proxy 对象为当前网关请求写入字符串 KV 对（请求级 annotations，区别于既有的 model/provider/apiKey 配置层 annotations）。meta 行（type=0）与 upstream 行（type=1）分别记录：meta 注解在请求终结时持久化一次，upstream 注解在每次 attempt 终结时持久化到该 attempt 的行。`request` 表新增 JSONB `annotations` 列承载；管理 API `GET /api/picotera/requests` 增加 `annotations` 过滤参数，用 JSONB 包含查询（`@>`）+ partial GIN 索引检索。不做任何 dashboard UI 修改（仅重新生成 TS 类型）。

## 存储

### 迁移 `db/migrations/044_request_annotations.sql`

```sql
-- +goose Up
ALTER TABLE request ADD COLUMN annotations JSONB;
CREATE INDEX request_annotations_idx ON request USING GIN (annotations jsonb_path_ops)
  WHERE annotations IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS request_annotations_idx;
ALTER TABLE request DROP COLUMN IF EXISTS annotations;
```

要点：

- 列 nullable、无默认值。未打标的行为 NULL，磁盘开销仅为 null bitmap 一位；INSERT 路径完全不写该列（meta 行与 upstream 行插入时恒为 NULL，注解只在各自终结点 UPDATE 一次），所以 GIN 索引对 INSERT 零成本。
- partial GIN（`jsonb_path_ops`）只收录非 NULL 行：索引体积与"打了标的行数"成正比，与总行数无关。`jsonb_path_ops` 比默认 `jsonb_ops` 更小更快，且完全覆盖本功能唯一的查询算子 `@>`。
- hypertable 上 `CREATE INDEX` 自动按 chunk 建立。压缩兼容性：TimescaleDB columnstore 不支持 GIN（hypercore TAM 的稠密索引方案自 2.21 起已废弃），因此约定——将来启用压缩时 `compress_after` 必须大于 annotations 查询窗口（如 90 天 vs 30 天），使 annotations 过滤查询永远只命中带索引的未压缩 chunk。JSONB 列本身可正常参与列压缩，不阻碍启用压缩。
- meta 行与 upstream 行都可携带注解；同一 span 的两类行各自独立记录，互不继承。

### 值域

`map[string]string`，键值均为字符串。不设条数/长度硬性上限（proposal 澄清）。写入校验（fail fast，违规即抛 JS `TypeError`）：

- key：非空字符串（Symbol 键、空串直接抛）；
- value：字符串（非字符串类型直接抛，不做 `String()` 强转）。

## 脚本侧写入通道（pkg/jsx）

现状没有 JS→Go 的通用回写通道（ctx 是 Go 单向写入，waterfall 返回值只读固定字段）。采用与 body Proxy（`objects.go` + sdk.js）同族的模式，但存储侧是平面 `map[string]string` 而非 jsonast 树：

- `qjsSession` 新增两个 Go 侧累加器 `metaAnnotations`、`upstreamAnnotations map[string]string`（与 `logs` 共用 `mu`）。
- host functions（`helpers.go`）按 slot（`"meta"` / `"upstream"`）路由：`__picotera_anno_get(slot, key)`（返回值的 JSON 编码串，缺失返回空串——JSON 编码保证空串值与不存在可区分）、`__picotera_anno_set(slot, key, value)`、`__picotera_anno_del(slot, key)`、`__picotera_anno_keys(slot)`（JSON 数组）、`__picotera_anno_has(slot, key)`。Go 侧对 set 再做一次防御性校验（key 非空）。
- `sdk.js` 提供 `__picotera_makeAnnotationsProxy(slot)` 工厂：返回一个以空对象为 target 的 `Proxy`，traps——`get`/`set`/`deleteProperty`/`has`/`ownKeys`/`getOwnPropertyDescriptor`（可枚举、可配置）全部转发到上述 host 函数。`set` trap 先在 JS 侧做 `typeof key === 'string' && key !== ''` 与 `typeof value === 'string'` 检查，违规抛 `TypeError`（避免 QuickJS host 边界的隐式强转）。`Object.keys`、`JSON.stringify`、`in`、spread 均可用。
- 安装时机：
  - `ctx.metaRequest = { annotations: __picotera_makeAnnotationsProxy('meta') }` 在 session 初始化（sdk.js 加载与 ctx 建立之后）时安装，整个 session 生命周期有效。
  - `ctx.upstreamRequest = { annotations: __picotera_makeAnnotationsProxy('upstream') }` 在**第一次 attempt 开始时**安装；此前访问 `ctx.upstreamRequest` 为 `undefined`（sortProviders/rewriteModel 阶段没有 upstream 请求可标注）。Proxy 按 slot 转发、无内部状态，跨 attempt 复用同一个 Proxy，Go 侧仅重置 map。
- `jsx.Session` 接口（`iface.go`）新增：`MetaAnnotations() map[string]string`（拷贝）、`ResetUpstreamAnnotations() error`（清空 upstream 累加器；首次调用时执行安装 `ctx.upstreamRequest` 的 JS eval）、`UpstreamAnnotations() map[string]string`（拷贝）。代码库中只有 `qjsSession` 实现该接口，无 fake 需要同步。
- 既有 `PatchContext` 用 `Object.assign` 浅合并 ctx 顶层键，不触碰 `metaRequest`/`upstreamRequest` 键，两者不会被 per-attempt patch 覆盖。

读取累加器不触碰 VM，因此即使 session 因 hook 超时被 taint，已写入的注解仍可安全取回并持久化。

## 持久化时点（pkg/server）

共用一个 helper：`persistRequestAnnotations(pctx, id, createdAt, anno)` —— anno 非空时 marshal 为 JSON，对该行执行一次部分 UPDATE（`request_update.go` 新增 `Annotations([]byte)` setter；`UpdateRequest` 查询新增 `set_annotations` CASE 分支）。失败仅记日志（遵循现有 recording 约定）。

**meta 行**：`gatewayFlow.run()` 中现有的 `defer session.Close()` 闭包内，Close 之前读 `session.MetaAnnotations()` 并持久化到 meta 行。该 defer 在 session 创建后的所有路径（成功、各类失败、流式中断、gateway 与 unified 两种路由）都会执行，且所有 hook 运行在请求 goroutine 上、`run()` 返回前必然结束。

**upstream 行**（per-attempt）：在 `runSingleAttempt`（`gateway_flow_attempts.go`）内——

1. 函数开头调用 `session.ResetUpstreamAnnotations()`：清空累加器（首次同时安装 `ctx.upstreamRequest`），使 `beforeRequest` 起的本 attempt hook 写入全部归属本 attempt；
2. `insertUpstreamAttempt` 返回后立即 `defer` 持久化 `session.UpstreamAnnotations()` 到 `(input.UpstreamID, input.UpstreamCreatedAt)`。该 defer 覆盖本函数所有出口：各失败路径（`afterUpstreamError` hook 之后才退出，hook 内的写入被包含）、成功路径（`HandleSuccess` 同步返回之后，流式期间 `runStreamErrorHook` 的写入被包含）、hook 失败提前终止路径。
3. `beforeRequest` 返回 `next=true` 跳过的 attempt 没有 upstream 行，期间写入的 upstream 注解随下一次 attempt 开头的 reset 丢弃——upstream 注解只属于真实发出的 attempt。

**不受 OTR 门控**：OTR 管的是"客户端内容不落盘"（body/preview）；注解是脚本作者主动、显式写入的运维标签，不属于客户端内容。

## 查询（管理 API）

`GET /api/picotera/requests` 增加查询参数 `annotations`：URL 编码的 JSON 对象字符串，如 `annotations=%7B%22agent%22%3A%22claude-code%22%7D`（即 `{"agent":"claude-code"}`）。

- 严格校验（fail fast，违规即 400）：必须是合法 JSON 对象、至少一对、所有值必须是 JSON 字符串。不接受数组、嵌套对象、数字、布尔、null 值。校验通过后由 Go 侧 `map[string]string` 重新 marshal 出规范 JSON 传给 SQL（不透传原始输入）。
- SQL：`ListRequests` 增加 `AND (sqlc.narg('annotations')::jsonb IS NULL OR r.annotations @> sqlc.narg('annotations')::jsonb)`。多对时 `@>` 天然是 AND 语义；meta 行与 upstream 行均可命中（可叠加现有 `type` 参数区分）。仅支持精确相等匹配（无前缀/存在性查询）。
- 时间窗：与 requestId 检索同一模式——`annotations` 过滤存在且未传 `startAt` 时，默认 `startAt = now - 30d`；显式传入的 `startAt` 原样尊重、不夹取。现有常量 `requestIDLookback` 更名为 `filterLookback`，两处共用（干净替换，不留旧名）。
- 查询计划：时间窗内 chunk 排除 + partial GIN bitmap scan，按 `(created_at DESC, id DESC)` 排序分页，与现有游标分页完全兼容。

## 读展示

`contract.RequestView` 增加 `Annotations map[string]string`（`json:"annotations,omitempty"`）。`ListRequests`、`ListRequestsBySpan` 的 SELECT 列表补 `r.annotations`（`GetRequest` 用 `r.*` 自动带出）。JSONB → map 用 `json.Unmarshal`，解码失败置 nil、保持现有无 error 的转换函数签名（列仅由本功能以 `map[string]string` 写入，失败在正常运行中不可达）。

Overview 聚合（caggs）、admin overview、label endpoints 均不涉及。

## 不引入的东西

- 不引入第三方库；不新增 hypertable 边表；不做 UI；不做 retention（列跟随 request 行生命周期）。
- 不提供管理 API 写注解的入口（仅脚本可写，符合 proposal"由用户自己挂脚本写进去"）。
- upstream 注解不继承 meta 注解，二者相互独立。
