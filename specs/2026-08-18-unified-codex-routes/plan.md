# 执行计划

## 1. 端点类型常量（`pkg/contract/endpoint.go`）

- 新增 `EndpointType_CodexCompact int32 = 11`、`EndpointType_CodexSearchV1Alpha int32 = 12`。
- `ToEndpointType` / `FromEndpointType` 各加 `codexCompact`、`codexSearchV1Alpha` 分支。
- `EndpointView.EndpointType` 的 `enum` tag 追加两项。

## 2. 端点标签枚举（`pkg/contract/label.go`）

- `EndpointLabel.EndpointType` 的 `enum` tag 追加同样两项。

## 3. 路由表改造（`pkg/server/unified_routes.go`）

- `unifiedRoute` 增加 `SourceType int32` 字段，字段顺序调整为 `Path, Name, Format, SourceType`。
- 为既有五条路由填 `SourceType`（4 / 3 / 2 / 7 / 8）。
- 追加三条：
  - `/api/unified/codex/responses`，`Unified Codex Responses`，`FormatOpenAIResponses`，`EndpointType_OpenAIResponses`
  - `/api/unified/codex/responses/compact`，`Unified Codex Compact`，`FormatUnknown`，`EndpointType_CodexCompact`
  - `/api/unified/v1/alpha/search`，`Unified Codex Search v1alpha`，`FormatUnknown`，`EndpointType_CodexSearchV1Alpha`
- 新增方法 `func (r unifiedRoute) passthrough() bool { return r.Format == llmbridge.FormatUnknown }`，注释说明「llmbridge 无对应格式 ⇔ 纯透传」。
- 删除 `unifiedRoutePath`（format→path 反查在 `/codex/responses` 出现后不再成立）。

## 4. 调度辅助函数（`pkg/server/gateway_unified_helpers.go`）

- 删除 `sourceEndpointType`，调用点改读 `route.SourceType`。
- `candidateEndpointTypes(route unifiedRoute, streaming bool) []int32`：`route.passthrough()` 时返回 `[]int32{route.SourceType}`；否则保持现有 Gemini/非 Gemini 的类型表逻辑（内部按 `route.Format` 判断）。
- `extractUnifiedModel(route unifiedRoute, r *http.Request, body []byte) (string, error)`：Gemini 两种格式取 `{model}` 路径变量；其余（含透传路由）取 body 的 `model`，空则 400 `errorx.ModelNotFound`。删除原先的「unsupported source format」兜底分支（路由表已穷举）。
- `setUnifiedModel(route unifiedRoute, body []byte, newModel string) ([]byte, error)`：Gemini 原样返回；其余用 `sjson.SetBytes` 写 `model`。
- `upstreamFormatFor` 不动。

## 5. unified handler（`pkg/server/handle_unified_gateway.go`）

- `handleUnifiedGenerate(route unifiedRoute) http.HandlerFunc`，`newUnifiedGatewayFlowConfig(route unifiedRoute, r *http.Request)`。
- 虚拟端点：`Path: route.Path`、`EndpointType: route.SourceType`（注释同步更新，说明 Path 为注册的路由模式）。
- `SourceFormat: route.Format`。
- `ExtractModel` / `SetBodyModel` / `ResolveCandidates` 改传 `route`。
- `PrepareAttempt`：`route.passthrough()` → `identityPrepareAttempt`，否则 `prepareUnifiedAttempt`；写注释说明透传路由跳过 `beforeTransform` 与格式转换。
- `HandleSuccess` 维持 `unifiedStreamSuccess`（透传时自动退化为字节转发）。

## 6. 路由注册（`pkg/server/server.go`）

- `registerEndpoints` 的循环改为 `h := s.handleUnifiedGenerate(route)`。

## 7. 端点标签合成条目（`pkg/server/handle_label.go`）

- unified 合成标签的 `EndpointType` 由 `r.Format.String()` 改为 `contract.FromEndpointType(r.SourceType)`。

## 8. 脚本可见的上游格式（`pkg/server/gateway_flow_candidates.go`）

- `buildPathCandidateSet` 与 `buildUnifiedCandidateSet` 里 `buildProviderModel(..., upstreamFormat.String())` 改为 `contract.FromEndpointType(<endpoint type>)`；`buildPathCandidateSet` 仍保留 `upstreamFormat` 变量供 sidecar 使用。

## 9. 用户消息预览（`pkg/server/user_message_preview.go`）

- `extractUserMessage` 新增 `case contract.EndpointType_CodexCompact:` → `extractOpenAIResponsesUserMessage`；`case contract.EndpointType_CodexSearchV1Alpha:` → `extractQueryUserMessage`。

## 10. 迁移（`db/migrations/047_codex_endpoints.sql`）

- Up：`CREATE OR REPLACE VIEW completion_endpoint_path`，端点类型白名单 `ARRAY[2,3,4,7,8,11]`，unified 常量路径在原五条基础上追加 `/api/unified/codex/responses`、`/api/unified/codex/responses/compact`。
- Down：还原 045 的定义（`ARRAY[2,3,4,7,8]` + 原五条路径）。
- 普通视图替换，不需要 `-- +goose NO TRANSACTION`。

## 11. Go 测试（`pkg/server/handle_unified_gateway_test.go`）

- 按新签名更新既有用例（原 `sourceEndpointType` 用例改为断言路由表里每条路由的 `SourceType`）。
- 新增：
  - 路由表自检：路径唯一；`/api/unified/codex/responses` 的 Format 为 `FormatOpenAIResponses`、SourceType 为 3；两条透传路由 `passthrough()` 为 true 且 SourceType 为 11 / 12。
  - `candidateEndpointTypes` 对两条透传路由返回单元素集合，且不受 `streaming` 影响。
  - `extractUnifiedModel` 对透传路由从 body 取 `model`；body 无 `model` 时返回 400。
  - `setUnifiedModel` 对透传路由写回 `model`。
- `go test ./...` 全绿。

## 12. openapi 与前端类型

- `mise run openapi` 重新生成 `openapi.yaml`。
- `pnpm --dir dashboard generate-openapi` 重新生成 `dashboard/src/openapi-types.d.ts`。

## 13. 前端常量

- `dashboard/src/api/index.ts`：`ENDPOINT_TYPE_LABELS` 增 `codexCompact: 'Codex 压缩'`、`codexSearchV1Alpha: 'Codex 搜索 v1alpha'`；`ENDPOINT_TYPES_MODEL_ROUTED` 追加这两个类型。
- `dashboard/src/utils/requestLabels.ts`：`UNIFIED_ENDPOINT_NAMES` 增三条 —— `/api/unified/codex/responses`: `Codex Responses`、`/api/unified/codex/responses/compact`: `Codex 压缩`、`/api/unified/v1/alpha/search`: `Codex 搜索 v1alpha`。
- 其余前端文件不改（测试台、渲染分派、端点表单均无需按新类型分支）。

## 14. 文档

- `CLAUDE.md` 的「Unified generation routes」段：五条改八条，说明透传路由的候选集合、跳过 `beforeTransform`、`endpoint_type` 11/12；「Database Schema」段补 `completion_endpoint_path` 的新范围。
- `docs/scripting.md`：`beforeTransform` 时机一句补「codex 透传路由不执行」；`upstreamFormat` 字段说明改为「该条目上游端点类型的字符串」。
- `README.md`：在 unified 端点说明处补一句 Codex base_url 配 `…/api/unified/codex`。

## 15. 验证

```bash
go build ./... && go test ./...
mise run openapi && pnpm --dir dashboard generate-openapi
pnpm --dir dashboard type-check && pnpm --dir dashboard lint
docker compose up -d && mise run server   # 迁移 047 在启动时自动执行
```

手工验收（需要一个已配置 `codexCompact` / `codexSearchV1Alpha` 端点并绑定渠道、且在 `provider_models` 里配了对应模型的环境）：

1. `POST /api/unified/codex/responses` 与 `/api/unified/v1/responses` 同 body，结果一致；两者 meta 行的 `endpoint_path` 分别记为各自路由。
2. `POST /api/unified/codex/responses/compact` 透传成功，请求详情页可见上游行的用量；该请求出现在成功率图表里。
3. `POST /api/unified/v1/alpha/search` 透传成功，对话 Tab 走 search alpha 渲染；该请求**不**出现在空回复/成功率统计中。
4. 三条路由缺 `model` 时返回 400；无匹配渠道时返回 404。
