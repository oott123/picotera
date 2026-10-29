# 执行计划：fetchModels 链路接入 rewriteRequest

## 1. 改造 handleFetchModels

**`pkg/server/handle_provider_endpoint.go`**

1. 会话创建（现 134-138 行的 `s.jsxEngine.NewSession` + `defer sess.Close()`）连同 `providerAnno` / `provJS` / `mergedAnno` 的准备与第一次 `PatchContext`，整体上移到 `provider.ModelsEndpointUrl == ""` 校验之后、构建 GET 请求之前。第一次 patch 只带 `EndpointType` / `Provider` / `Annotations`：

```go
endpointType := "fetchModels"
...
if perr := sess.PatchContext(jsx.ContextPatch{
    EndpointType: &endpointType,
    Provider:     &provJS,
    Annotations:  &mergedAnno,
}); perr != nil {
    return nil, huma.NewError(gatewayHookStatus(perr), perr.Error())
}
```

原注释保留并改写为：models 地址挂在 provider 上而非 endpoint 行，故 `ctx.endpoint` 与 `routedModel`/`request`/`apiKey`/`providerModel`/`attempt` 一并为 null；本管理路由不做 API key 认证，`ctx.annotations` 只含渠道层注解。

2. GET 请求改用 `ctx` 构建（不再是 `fetchCtx`），`applyCredentials` 与 `anthropic-version` 头位置不变。紧接着插入 hook：

```go
pending, herr := sess.RunRewriteRequest(serializePendingRequest(req), nil)
if herr != nil {
    return nil, huma.NewError(gatewayHookStatus(herr), herr.Error())
}

// 10s 只覆盖上游往返；hook 时长由引擎的 hook timeout 约束。
fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
defer cancel()
req, _, err = buildRequestFromPending(fetchCtx, pending, nil)
if err != nil {
    return nil, huma.Error502BadGateway(err.Error())
}
```

原第 81-82 行的 `fetchCtx` 声明随之删除。

3. 上游响应解析、`aggregateProviderModels` 保持原样。第二次 `PatchContext` 只带 `UpstreamResponse`，紧贴 `RunRewriteProviderModels` 之前。

4. 三处 hook 失败的状态码判断（两次 `PatchContext` + `RunRewriteProviderModels`）统一替换为 `huma.NewError(gatewayHookStatus(err), err.Error())`，删掉内联的 `status := http.StatusBadGateway; if errors.Is(..., jsx.ErrHookTimeout) {...}` 分支。检查 `errors` / `http` import 是否仍被文件其它位置使用（`errors.Is(err, pgx.ErrNoRows)` 与 `http.MethodGet` 仍在，两者都保留）。

## 2. 测试

**`pkg/jsx/engine_test.go`**（跟在 `TestSession_RewriteRequest_BodyJSONRoundtrip` 之后）

5. `TestSession_RewriteRequest_NoBody`：脚本就地改 `pending.url` 与 `pending.headers`，以 `nil` body 调用 `RunRewriteRequest`，断言 URL/headers 生效且 `out.Body == nil`。

6. `TestSession_RewriteRequest_NoBody_ScriptAddsBody`：脚本返回 `{...pending, method: 'POST', body: {scope: 'all'}}`，断言 `out.Method == "POST"` 且 `out.Body` 是 `{"scope":"all"}` 的 JSON 字节——这是 fetch-models 链路上脚本携带请求体的唯一途径。

`pkg/server` 侧不加测试：`handleFetchModels` 需要 `db.Querier` 与真实上游，仓库无 DB 测试装置，该 handler 本来也没有测试文件。

## 3. 文档

**`docs/scripting.md`**

7. Hook 一览表 `rewriteRequest` 行的概要补上「获取模型列表拉取前同样执行」。

8. 「单次请求中的执行顺序」末尾那句独立链路说明改为：拉取前执行一次 `rewriteRequest`，拉取后执行一次 `rewriteProviderModels`，两者共用同一会话的 `ctx`（脚本挂在 `ctx` 上的自定义字段可跨这两个 hook 传递）。

9. `### rewriteRequest` 章节末尾追加「获取模型列表」小节：`ctx.endpointType === 'fetchModels'` 判别；可见的 ctx 只有 `provider` / `annotations`；`pending` 是发往 `provider.modelsEndpointUrl` 的 GET，凭证已注入 `pending.headers`；无 body，就地赋值 `pending.body` 不生效，需返回新对象（附 design.md 里的 `models-post` 示例）。

10. `### rewriteProviderModels` 的「可用的 ctx 字段」列表加一条 `ctx.endpointType`：恒为 `"fetchModels"`。

11. ctx 字段表 `endpointType` 行：类型改为 `"gateway" \| "unified" \| "fetchModels"`，说明补「获取模型列表链路」。

**`CLAUDE.md`**

12. Scripts 小节的 `rewriteRequest` 条目补一句：also runs once on the fetch-models path, over the upstream `/models` GET (`ctx.endpointType = "fetchModels"`, no `pending.body`), on the same session as `rewriteProviderModels`。

13. `rewriteProviderModels` 条目补上会话与 `ctx.endpointType` 的共享事实。

## 4. 验证

14. `go build ./...`
15. `go test ./pkg/jsx/... ./pkg/server/...`
16. 手工：启动服务，建一条脚本 `picotera.hooks.rewriteRequest.tap('t', (ctx, pending) => { console.log(ctx.endpointType, pending.url) })`，在渠道表单点「获取模型列表」，确认改写生效（例如改 `pending.url` 指向另一地址后返回的模型列表随之变化）。
