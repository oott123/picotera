# 请求自定义 Annotations — 执行计划

前置阅读：`design.md`（架构决策）、`api.md`（接口形态）。

## 1. 数据库

1. 新建 `db/migrations/044_request_annotations.sql`：
   - Up：`ALTER TABLE request ADD COLUMN annotations JSONB;` + `CREATE INDEX request_annotations_idx ON request USING GIN (annotations jsonb_path_ops) WHERE annotations IS NOT NULL;`
   - Down：`DROP INDEX IF EXISTS request_annotations_idx;` + `ALTER TABLE request DROP COLUMN IF EXISTS annotations;`
2. `db/queries/request.sql`：
   - `ListRequests`：SELECT 列表补 `r.annotations`；WHERE 增加 `AND (sqlc.narg('annotations')::jsonb IS NULL OR r.annotations @> sqlc.narg('annotations')::jsonb)`。
   - `ListRequestsBySpan`：SELECT 列表补 `r.annotations`。
   - `UpdateRequest`：增加 `annotations = CASE WHEN sqlc.arg('set_annotations')::bool THEN sqlc.narg('annotations')::jsonb ELSE annotations END`。
   - `GetRequest` 用 `r.*`，无需改动。
3. 运行 `sqlc generate`，确认 `pkg/db/` 生成的 `ListRequestsParams.Annotations`、`UpdateRequestParams.SetAnnotations/Annotations` 与各 Row 结构的 `Annotations []byte` 字段。

## 2. pkg/jsx — 脚本写入通道

4. `session.go`：`qjsSession` 增加 `metaAnnotations`、`upstreamAnnotations map[string]string` 字段（惰性初始化，与 `logs` 共用 `mu`）与 slot 路由的内部方法 `annoSet/annoGet/annoDel/annoKeys/annoHas(slot, ...)`（set 校验 key 非空、返回 error）；新增导出方法：
   - `MetaAnnotations() map[string]string`（加锁返回拷贝，空返回 nil）；
   - `UpstreamAnnotations() map[string]string`（同上）；
   - `ResetUpstreamAnnotations() error`（清空 upstream 累加器；首次调用时 eval 安装 `globalThis.ctx.upstreamRequest = { annotations: __picotera_makeAnnotationsProxy('upstream') }`，之后仅清 map）。
5. `iface.go`：`Session` 接口增加上述三个方法（唯一实现者是 `qjsSession`，无 fake 需要同步）。
6. `helpers.go`：新增 `registerAnnotations(s *qjsSession)`，注册五个 host 函数并在 `registerHelpers` 中接线：`__picotera_anno_set(slot, key, value)`（返回 error）、`__picotera_anno_get(slot, key)`（返回值的 JSON 编码串，缺失返回空串——JSON 编码保证空串值与不存在可区分）、`__picotera_anno_del(slot, key)`、`__picotera_anno_keys(slot)`（返回 JSON 数组串）、`__picotera_anno_has(slot, key)`（返回 int 0/1）。
7. `sdk.js`：实现 `__picotera_makeAnnotationsProxy(slot)` 工厂——以空对象为 target 的 `Proxy`，traps：`get`（string 键转发 host，缺失返回 `undefined`；Symbol 键返回 `undefined`）、`set`（JS 侧 `typeof` 校验 key 非空字符串、value 字符串，违规抛 `TypeError`，通过后转发 host）、`deleteProperty`、`has`、`ownKeys`、`getOwnPropertyDescriptor`（enumerable+configurable，使 `Object.keys`/`JSON.stringify`/spread 可用）。session 初始化尾部（ctx 建立后）安装 `globalThis.ctx.metaRequest = { annotations: __picotera_makeAnnotationsProxy('meta') }`（在 `session.go` 的 `newSession` 中以 eval 完成，与 sdk.js 加载同处）。
8. `pkg/jsx` 新增测试（参考 `engine_test.go` 的现有 session 测试写法）：
   - 脚本在多个 hook 中对 `ctx.metaRequest.annotations` 写/覆盖/删除，`MetaAnnotations()` 返回最终态；未写入时返回 nil；
   - 非字符串 value / 空 key / Symbol 键写入抛 `TypeError`；
   - 空串 value 可写入且可读回（与不存在区分）；
   - `Object.keys` / `JSON.stringify` / `in` / spread 语义正确；
   - `ResetUpstreamAnnotations` 安装 `ctx.upstreamRequest`、清空累加器；安装前脚本读 `ctx.upstreamRequest` 为 `undefined`；reset 后旧写入不残留；
   - `PatchContext`（如 Attempt patch）不清除 `ctx.metaRequest`/`ctx.upstreamRequest`；
   - hook 超时 taint 后 `MetaAnnotations()`/`UpstreamAnnotations()` 仍返回已写入内容。

## 3. pkg/server — 持久化

9. `request_update.go`：`requestUpdate` 增加 `Annotations(v []byte) *requestUpdate` setter（置 `SetAnnotations=true`）。
10. 新增 helper（放 `gateway_flow.go`）：`(f *gatewayFlow) persistRequestAnnotations(id string, createdAt time.Time, anno map[string]string)` —— anno 为空直接返回；否则 `json.Marshal` 后用 `ctxs.Persist()` 上下文执行 `updateRequest(...).Annotations(b)`。
11. meta 行：`gateway_flow.go` `run()` 中现有 `defer session.Close()` 闭包扩展——Close 前调用 `f.persistRequestAnnotations(f.meta.ID, f.meta.CreatedAt, f.session.MetaAnnotations())`。
12. upstream 行：`gateway_flow_attempts.go` `runSingleAttempt`——
    - 函数开头（`runBeforeRequest` 之前）调用 `f.session.ResetUpstreamAnnotations()`，错误按 hook 错误处理（`f.failHook` + `return false, true`）；
    - `insertUpstreamAttempt` 返回后紧跟 `defer f.persistRequestAnnotations(input.UpstreamID, input.UpstreamCreatedAt, f.session.UpstreamAnnotations())`，覆盖本函数全部出口（含各失败路径 `afterUpstreamError` 之后、成功路径 `HandleSuccess` 同步返回之后、hook 失败提前终止）。
13. 实现时验证：unified 路由（`gatewayRouteUnified`）与流式响应下，`HandleSuccess` 及其中的 `runStreamErrorHook` 均在请求 goroutine 上、`runSingleAttempt` 返回前完成；如发现任何 hook 在其后仍可能执行，把该路径的持久化调用移到对应终结点并在代码注释中说明。

## 4. pkg/contract + handler — 查询与展示

14. `pkg/contract/request.go`：
    - `RequestView` 增加 `Annotations map[string]string \`json:"annotations,omitempty"\``；
    - `ListRequestsRequest` 增加 `Annotations string \`query:"annotations,omitempty"\``；
    - `ToRequestView` / `ToListRequestRowView` / `ToListRequestsBySpanRowView` 解码 `annotations []byte`：非空时 `json.Unmarshal` 到 `map[string]string`，解码失败置 nil（保持现有无 error 签名不变；该列仅由本功能以 `map[string]string` 写入，失败在正常运行中不可达）。
15. `pkg/server/handle_requests.go`：
    - 常量 `requestIDLookback` 更名为 `filterLookback`（注释改为覆盖 requestId 与 annotations 两种检索），更新引用处；
    - 新增独立校验函数 `parseAnnotationsFilter(s string) ([]byte, error)`：空串返回 nil；否则严格解析为 JSON 对象、≥1 对、所有值为字符串（用 `map[string]json.RawMessage` + 逐值 `json.Unmarshal` 到 string 实现），违规返回 `huma.Error400BadRequest`；通过后把 `map[string]string` 重新 marshal 为规范 JSON 返回；
    - `handleListRequests`：调用 `parseAnnotationsFilter`，结果传入 `ListRequestsParams.Annotations`；`annotations` 过滤存在且 `startAt` 缺省时应用 30 天默认窗口（与 requestId 分支合并为同一处逻辑）。
16. `pkg/server` 测试：`handle_requests_test.go`（或新文件）覆盖 `parseAnnotationsFilter` —— 合法单对/多对、空串、非对象（数组/字符串/数字）、空对象、值为数字/布尔/null/嵌套对象、非法 JSON。

## 5. 收尾

17. `mise run openapi` 重新生成 `openapi.yaml`；`pnpm --dir dashboard generate-openapi` 更新 TS 类型（无 UI 改动）。
18. 更新根 `CLAUDE.md`：Scripts 小节补充 `ctx.metaRequest.annotations` / `ctx.upstreamRequest.annotations` 的语义（Proxy、类型校验、per-attempt 重置、持久化时点、与配置层 `ctx.annotations` 的区别）与查询参数；Key Patterns/Schema 处提及 `request.annotations` 列及压缩窗口约定。
19. `go build ./... && go test ./pkg/jsx/ ./pkg/server/`；`pnpm --dir dashboard type-check` 确认生成类型无 TS 错误。
