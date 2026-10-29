# 执行计划：数据记录模式

## 1. OTR 模式类型与解析（新文件 `pkg/server/otr_mode.go`）

- 定义 `otrMode int8` 与三常量 `otrNone` / `otrBody` / `otrBodyAndMessage`，及方法 `recordBody()`、`recordPreview()`。
- `const otrSettingKey = "request.otr"`。
- `const otrHeaderName = "X-PicoTera-OTR"`。
- `parseOTRValue(s string) (otrMode, bool)`（header 与设置共用）：
  - `"none"` → `(otrNone, true)`，`"body"` → `(otrBody, true)`，`"body-and-message"` → `(otrBodyAndMessage, true)`
  - 其它 → `(otrNone, false)`
- `(h *gatewayHandler) otrSetting(ctx, userID int64) otrMode`：`GetUserSetting(userID, otrSettingKey)`，`json.Unmarshal` 成字符串后经 `parseOTRValue` 映射；缺失/解析失败/未知值 → `otrNone`。

## 2. flow 字段与解析挂载（`pkg/server/gateway_flow.go`）

- `gatewayFlow` 增加字段：
  - `headerOTR otrMode` + `headerOTRSet bool`（认证前解析的 header 覆盖）
  - `otr otrMode`（认证后计算的有效模式）
- `run()` 开头、`readBody()` 之后、`insertMetaRequest()` 之前：读 `v := f.r.Header.Get(otrHeaderName)`。
  - `v != ""` 时 `parseOTRValue(v)`：`ok == false` → `writeGatewayError(f.w, http.StatusBadRequest, "invalid X-PicoTera-OTR header", errorx.InvalidRequest.Error())`，`return`（不建 meta 行）；`ok` → 存 `f.headerOTR`、`f.headerOTRSet = true`。
- `insertMetaRequest()`：
  - `UserMessagePreview` 改为 `pgtype.Text{Valid: false}`（不再在插入时计算）。
  - **删除** 第 219 行的 `f.h.uploadRequestArtifact(...)` 调用（移至认证后）。
- `authenticateAndBackfill()`：在拿到 `apiKey.UserID` 后：
  - 计算有效模式：`if f.headerOTRSet { f.otr = f.headerOTR } else { f.otr = f.h.otrSetting(f.ctxs.Request, apiKey.UserID) }`。
  - 若 `f.otr.recordPreview()`：计算 `extractUserMessagePreview(f.body, f.config.Endpoint.EndpointType)`，经新 query 回填（见 §3）。
  - 上传请求 artifact：`f.h.uploadRequestArtifact(pctx, f.meta.ID, f.meta.CreatedAt, f.meta.RequestMethod, f.meta.RequestURL, f.meta.RequestHeader, f.artifactBody(f.body))`。
- 增加辅助方法 `func (f *gatewayFlow) artifactBody(b []byte) []byte`（`f.otr.recordBody()` 为假返回 `nil`）。

## 3. user_message_preview 回填 query（`db/queries/request.sql` + sqlc）

- 新增：
  ```sql
  -- name: UpdateRequestUserMessagePreview :exec
  UPDATE request SET user_message_preview = $3
  WHERE id = $1 AND created_at = $2;
  ```
- `sqlc generate`，在 §2 的认证后路径调用 `f.h.updateRequestUserMessagePreview(...)`（在 `gateway_handler` 上加薄封装方法，参照既有 `updateRequestModel` 的封装风格）。

## 4. liveProgress body/timings 门控（`pkg/server/live_requests.go`）

- `liveProgress` 增加字段 `recordBody bool`。
- `recordChunk(b)`：`recordBody` 为假时跳过 `p.body.Write(b)` 与 timings 追加循环，仅 `p.bytes += len(b)` 与更新 `lastChunkAt`。
- 构造函数加形参：
  - `newLiveProgress(recordBody bool)`
  - `newLiveProgressWithOrigin(origin time.Time, recordBody bool)`
  - `RegisterUpstream(id string, cancel context.CancelFunc, recordBody bool)`
- 更新全部调用点传入对应 flow 的 `f.otr.recordBody()`：
  - `gateway_flow_attempts.go:245` `RegisterUpstream`
  - `gateway_flow_success.go` `pipePathResponse` 的 fallback `newLiveProgressWithOrigin`
  - `gateway_unified_helpers.go` 的 `upstreamProgress` fallback 与 `metaProgress`（transforming 路由）

## 5. 响应 artifact / 聚合门控

- **path 成功**（`gateway_flow_success.go` `aggregatePathResponse`）：`recordBody()` 为假时跳过 `buildAggregatedArtifact`（传 `nil`）。body/timings 已由 §4 的 `artifactRecord()` 返回空，无需额外处理。
- **path 非 200 上游**（`gateway_flow_attempts.go:326` `handleUpstreamNonOK`）：body 实参包 `f.artifactBody(respBody)`。`error_message` 列保留原文（不动）。
- **afterUpstreamError break**（`gateway_flow_attempts.go:433` `respondUpstreamErrorBreak`）：meta 响应 artifact body 实参包 `f.artifactBody(body)`。
- **gateway 错误响应**（`gateway_flow_errors.go` 各 `uploadMetaResponseArtifact` 调用，约 5 处）：body 实参包 `f.artifactBody(...)`。
- **unified 成功**（`gateway_unified_helpers.go` `unifiedStreamSuccess`）：
  - 在 `unifiedStreamArgs` 增加 `recordBody bool` 字段（由 `unifiedStreamArgsFromSuccess` 从 `input.Flow.otr.recordBody()` 填充）。
  - `recordBody` 为假时跳过 `upstreamAggregated` 与 `metaAggregated` 的构建（传 `nil`）。
  - upstream/meta body 由 §4 的 progress `artifactRecord()` 返回空，无需改动。
  - `failUnifiedSuccess`（约 644 行 `uploadMetaResponseArtifact`）：body 实参包 `a.recordBody` 门控（或经 `input.Flow.artifactBody`）。

## 6. 上游 header 清理（`pkg/server/gateway_helpers.go`）

- `buildUpstreamRequest` 的 header 复制循环跳过条件追加：`|| strings.HasPrefix(lower, "x-picotera")`。
- `buildForwardedHeaders` 为白名单转发，无 X-PicoTera，不改。

## 7. Dashboard 设置 UI（`dashboard/src/views/SettingsView.vue`）

- 新增 `otr = ref<'none' | 'body' | 'body-and-message'>('none')`。
- 新增 `useQuery`（key `queryKeys.userSettings.detail('request.otr')`，`getUserSetting('request.otr')`，`throwOnError: false`），`watch` 回填：`data?.value` 命中三值之一则赋值，否则 `'none'`。
- 用 `SegmentedControl`（`@/ui`）渲染三选项：完整记录（`none`）/ 不记录 body（`body`）/ 仅元数据（`body-and-message`）；下方 `<p>` 说明各档含义。
- 保存：`saveMutation` 内 `upsertUserSetting({ key: 'request.otr', value: otr.value })`，成功后 `invalidateUserSettings`。
- 两个设置（项目自动创建、数据记录）可共用一个保存按钮，或各自保存——沿用现有单按钮，保存时一并 upsert 两个 key。

## 8. 测试（`pkg/server/*_test.go`）

- `otr_mode_test.go`：`parseOTRValue` 各分支（含非法值返回 `ok == false`）；`otrSetting` 的 JSON 解析与缺省。
- `otrMode` 的 `recordBody()`/`recordPreview()` 真值表。
- `liveProgress.recordChunk` 在 `recordBody=false` 时 body 空、timings 空、bytes 仍累加（纯结构体单测）。
- `buildUpstreamRequest` 移除 `X-PicoTera-Foo` / `X-PicoTera-OTR` 头的断言。

## 9. 收尾

- `go build ./...` + `go test ./pkg/server/...`。
- `pnpm --dir dashboard type-check` + `lint`。
- 无 openapi / contract 改动（用户设置走通用 CRUD），无需 `mise run openapi` / `generate-openapi`。

## 验证点

- header 非法值 → 400，不创建 meta 行。
- header `none` 覆盖用户 `body` 设置 → 完整记录。
- `body`：artifact 有 headers/状态、无 body/聚合/时序；live 无 body/时序、有字节数；token/费用正常；preview 仍记录。
- `body-and-message`：在 `body` 基础上 preview 为空。
- 上游请求不含任何 `X-PicoTera*` 头。
