# 执行计划：unified 网关 Anthropic count_tokens 端点

后端改动只有路由表一行；其余是注释、测试、前端标签、文档与端到端验证。

## 1. 路由表（`pkg/server/unified_routes.go`）

`unifiedRoutes` 追加第七条（放在 embeddings 之后，成为最后一条固定路由）：

```go
	// Anthropic token counting: the request carries the Messages shape but
	// produces no completion — the response is {"input_tokens": N}. llmbridge
	// has no converter for it, so this is a passthrough route served only by
	// endpoints of type anthropicCountTokens (5); an anthropicMessages endpoint
	// does not serve it (its upstream URL is the messages URL, not the
	// count_tokens one).
	{Path: "/api/unified/v1/messages/count_tokens", Name: "Unified Anthropic Count Tokens", Format: llmbridge.FormatUnknown, SourceType: contract.EndpointType_AnthropicCountTokens},
```

`passthrough()` 由 `Format == FormatUnknown` 自动为 true ⇒ `candidateEndpointTypes` 返回 `{5}`、`newUnifiedGatewayFlowConfig` 选 `identityPrepareAttempt`、`registerEndpoints` 的循环自动注册 POST + OPTIONS。**其余后端文件（`gateway_unified_helpers.go`、`handle_unified_gateway.go`、`server.go`、`handle_label.go`）一行都不改。**

## 2. 注释同步（`pkg/server/endpoint_router.go`）

文件头注释枚举了绕开 `endpointRouter.Match` 的统一路由；把第三行的

```
// Gemini variants), /api/unified/v1/embeddings, and the /api/unified/codex/*
```

替换为

```
// Gemini variants), /api/unified/v1/embeddings,
// /api/unified/v1/messages/count_tokens, and the /api/unified/codex/*
```

## 3. 后端测试（`pkg/server/handle_unified_gateway_test.go`）

1. `TestUnifiedRoutesTable` 的 cases 追加一行（该测试断言 `len(cases) == len(unifiedRoutes)`，漏改会直接失败）：

```go
		{"/api/unified/v1/messages/count_tokens", llmbridge.FormatUnknown, contract.EndpointType_AnthropicCountTokens, true},
```

2. `TestCandidateEndpointTypesPassthrough` 的 map 追加（钉住候选集合就是 `{5}`，与流式标志无关）：

```go
		"/api/unified/v1/messages/count_tokens": contract.EndpointType_AnthropicCountTokens,
```

3. `TestExtractUnifiedModel_Passthrough` 的 cases 追加（钉住按 body 的 `model` 路由，且缺失 `model` 时 400 而非无模型降级 —— 该表的固定路由分支已覆盖 400 断言）：

```go
		{"count tokens", unifiedRouteByPath(t, "/api/unified/v1/messages/count_tokens"), `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`, "claude-sonnet-4-5"},
```

跑 `go test ./pkg/...`。

## 4. 前端（`dashboard/src/utils/requestLabels.ts`）

`UNIFIED_ENDPOINT_NAMES` 追加一条，中文名与 `ENDPOINT_TYPE_LABELS.anthropicCountTokens` 保持一致：

```ts
  '/api/unified/v1/messages/count_tokens': 'Anthropic Tokens 计数',
```

其余前端文件不动：`testBody.ts` 的注释已把 `anthropicCountTokens` 列进「测试台无法构造请求体的类型」，代码走 `default` 分支；`TestView.vue` 的路径映射按格式而非端点类型分派，无需新格式；`conversation.ts` 对计数请求体按 `system` / `messages` 判为 anthropic 会话、对 `{"input_tokens": N}` 响应判为无可渲染格式并落回原始 JSON，符合预期。

`openapi.yaml` 与 `dashboard/src/openapi-types.d.ts` 无需重新生成（没有 contract 改动）。

## 5. 端到端验证

本机 Postgres(34052) / KeyDB(34051) / MinIO(34050) 已在运行；用一个**独立数据库** `picotera_smoke` 跑，避免污染开发库。① 建库、② 假上游、③ 起服务、④ 建配置、⑤ 正例、⑥ 查行、⑦ 反例、⑧ 清理。

1. **建库**：

   ```bash
   psql "postgres://picotera:picotera@localhost:34052/picotera" -c 'CREATE DATABASE picotera_smoke'
   ```

2. **假上游**（throwaway 脚本，落在 `/tmp/fake_count_tokens.py`）：监听 `127.0.0.1:39999`，把每次请求的 `path + query`、`model`、`x-api-key`（按大小写不敏感读头）追加写入 `/tmp/count-tokens-upstream.log`，固定返回 `{"input_tokens":2095}`。用 `hub start` 起（`pty: false`），ready 条件用端口 39999。

3. **起服务**：`hub start name=picotera-smoke application=bash args=["-c","PICOTERA_DATABASE_URL=postgres://picotera:picotera@localhost:34052/picotera_smoke PICOTERA_PORT=9899 exec go run ./cmd/picotera/main.go"]`，`ready={"log":"serving API","port":9899}`。当前 shell 已由 direnv/mise 导出 `PICOTERA_*`，所以数据库与端口用命令内联赋值覆盖，不依赖 `env` 合并顺序；其余变量（S3、`PICOTERA_AUTH_SINGLE_USER_MODE=true`、llmbridge 插件路径）按现有环境继承，`dist/picotera-llmbridge-plugin` 已构建，透传路由也用不到它。单用户模式下管理 API 无需鉴权头，root 自动是 admin。

   起好后先确认连的是空库：`GET /api/picotera/providers` 必须返回 `[]`（若返回开发库的数据，说明覆盖没生效，停下修命令再继续）。

4. **建配置**（全部走管理 API，`http://localhost:9899`）：

   - `PUT /api/picotera/endpoints`：`{"name":"smoke ct","path":"/v1/messages/count_tokens","modelPath":"model","credentialsResolver":"xApiKey","endpointType":"anthropicCountTokens"}`
   - `PUT /api/picotera/endpoints`：`{"name":"smoke msg","path":"/v1/messages","modelPath":"model","credentialsResolver":"xApiKey","endpointType":"anthropicMessages"}`（仅用于反向验证）
   - `PUT /api/picotera/providers`：`{"name":"smoke","credentials":"sk-smoke","priority":0,"providerModels":[{"model":"smoke-ct","upstreamModelName":"smoke-ct-upstream"},{"model":"smoke-msg"}],"annotations":{},"disabled":false,"supportsNativeWebSearch":false,"insecureTls":false}`（`upstreamModelName` 用来验证模型回写真的落到计数请求体上）
   - `PUT /api/picotera/provider-endpoints`（两条）：计数端点 → `upstreamUrl=http://127.0.0.1:39999/v1/messages/count_tokens`；messages 端点 → `upstreamUrl=http://127.0.0.1:39999/v1/messages`
   - `PUT /api/picotera/models` 两次：`{"name":"smoke-ct"}`、`{"name":"smoke-msg"}`
   - `POST /api/picotera/api-keys` `{"name":"smoke"}` → 取返回的 `key`

5. **正例**：

   ```bash
   curl -sS -X POST 'http://localhost:9899/api/unified/v1/messages/count_tokens?beta=true' \
     -H 'authorization: Bearer <key>' -H 'content-type: application/json' \
     -d '{"model":"smoke-ct","messages":[{"role":"user","content":"count me"}]}'
   ```

   断言：HTTP 200，响应体逐字节等于假上游返回的 `{"input_tokens":2095}`；`/tmp/count-tokens-upstream.log` 里那条记录的 path 是 `/v1/messages/count_tokens?beta=true`、model 是 `smoke-ct-upstream`（证明查询串透传 + `upstreamModelName` 回写）、`x-api-key: sk-smoke`。

6. **记录行**：`GET /api/picotera/requests?limit=5` 断言：`type = 0` 的 meta 行 `endpointPath = /api/unified/v1/messages/count_tokens`、`finishReason = 3`、`statusCode = 200`，`type = 1` 的 upstream 行 `endpointPath = /v1/messages/count_tokens`；两行的 `inputTokens` / `outputTokens` / `modelCost` 字段缺席（`omitempty`，即列为 NULL），`userMessagePreview = "count me"`。另跑 `GET /api/picotera/labels/endpoints` 断言新标签 `{path:"/api/unified/v1/messages/count_tokens", name:"Unified Anthropic Count Tokens", endpointType:"anthropicCountTokens"}` 已出现。

7. **反例**：用只绑定 messages 端点的模型打同一路由 —— `{"model":"smoke-msg", …}` 必须 404 `no_provider_available`（证明候选集合就是 `{anthropicCountTokens}`，不会退到 messages 端点）。再确认假上游日志没有新条目。

8. **清理**：`hub stop` 两个进程；`psql … -c 'DROP DATABASE picotera_smoke'`；删掉 `/tmp/fake_count_tokens.py` 与 `/tmp/count-tokens-upstream.log`。若因环境原因无法跑（例如端口/凭据冲突），必须在交付说明里写明未做端到端验证及原因，不得默认通过。

## 6. 文档（`CLAUDE.md`，`AGENTS.md` 是它的软链）

1. 「Unified generation routes」首句：`Six fixed chi routes plus one wildcard mount` → `Seven fixed chi routes plus one wildcard mount`。
2. 同段路由清单追加一条（接在 embeddings 之后）：

   ```
   - `POST /api/unified/v1/messages/count_tokens` — Anthropic count_tokens, **passthrough**, non-streaming only.
   ```

3. 「These are NOT rows in the `endpoint` table」那句的运营者配置类型清单，在 `plus `openaiEmbedding` and `codex`` 里补上 `anthropicCountTokens`。
4. 「**Passthrough routes**」段落的首句类型清单：`openaiEmbedding` = 13 之外补 `anthropicCountTokens` = 5，并说明新路由同样只在 body 的 `model` 上路由、缺失即 400。
5. `README.md` 与 `docs/scripting.md` 不动（前置文档未列 unified 路由清单；`docs/scripting.md` 的格式枚举表已把 countTokens 归入 `unknown`）。

## 不做的事

- 不新增端点类型、不改 `pkg/contract/`，因此不重新生成 `openapi.yaml` / `openapi-types.d.ts`。
- 不新增数据库迁移（`completion_endpoint_path` 是显式白名单）。
- 不改 `ResponseExtractor` / `pricing.go`（计数结果不入库是决策）。
- 不做 llmbridge 跨格式转换与「按端点类型追加 `/count_tokens`」的 URL 推断。
- 不改 `user_message_preview.go`（默认启发式链已正确覆盖计数请求体）。
- 不改 `testBody.ts` / `TestView.vue` / `conversation.ts` / `EndpointForm.vue`。