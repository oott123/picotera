# 执行计划

## 1. 注入 alt=sse

文件:`pkg/server/gateway_flow_attempts.go`,函数 `bridgeUnifiedRequest`。

在 `resetRequestBody(req, upBody)` / 设置 `Content-Type` 之后、`return req, upBody, nil` 之前,加入:当 `input.Sidecar.UpstreamFormat == llmbridge.FormatGeminiStreamGenerateContent` 时,对 `req.URL` 覆盖式设置查询参数 `alt=sse`,并写回 `req.URL.RawQuery`。附中文注释说明用途。

该分支位于 identity 提前返回之后,自动只对 bridge 场景生效。

## 2. 测试

文件:`pkg/server/handle_unified_gateway_test.go`(或同包新增测试)。

新增单元测试:构造 `input.Sidecar.UpstreamFormat = FormatGeminiStreamGenerateContent` 且源格式不同(如 Anthropic Messages)的 bridge 场景,调用 `bridgeUnifiedRequest`,断言返回 `req.URL.Query().Get("alt") == "sse"`。

补充 identity 场景断言:当 `UpstreamFormat == SourceFormat == FormatGeminiStreamGenerateContent` 时,`bridgeUnifiedRequest` 提前返回、不注入 `alt`(URL 保持原样)。

> 若 `bridgeUnifiedRequest` 依赖 `f.h.llmBridge.BridgeRequest`(插件)而难以在纯单测中构造,则将 alt=sse 注入抽成可独立测试的小函数(如 `ensureGeminiStreamAltSSE(u *url.URL)`),在 `bridgeUnifiedRequest` 中调用,并对该函数与格式判定分别写单测,避免触及 gRPC 插件。

## 3. 验证

- `go build ./...` 通过。
- `go test ./pkg/server/...` 通过。
