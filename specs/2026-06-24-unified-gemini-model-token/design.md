# 设计

## 根因

Unified 网关在 `buildRewrittenUpstreamRequest`（`pkg/server/gateway_flow_attempts.go`）
中构造上游请求时，调用：

```go
buildUpstreamRequest(..., input.Sidecar.UpstreamURL, upstreamModel, ..., f.config.PathVars, ...)
```

`buildUpstreamRequest` 通过 `substitutePathVars(upstreamURL, pathVars)` 把上游 URL 里的
`{name}` 标记按 `pathVars` 替换。Gemini 上游 endpoint 的 `upstreamUrl` 含 `{model}` 标记。

而 unified 路由传入的 `f.config.PathVars` 来自 `chiURLParams(r)`，即入站 unified 路由匹配到的
chi 路径变量：

- Anthropic / OpenAI 源路由（`/api/unified/v1/messages` 等）**路径中没有 `{model}`**，
  所以 `chiURLParams(r)` 为空。`substitutePathVars` 在 `len(vars)==0` 时原样返回 URL，
  `{model}` 标记被保留 → 上游收到字面量 `{model}`（编码为 `%7Bmodel%7D`）。这是被报告的 bug。
- 即便是 Gemini 源路由，入站 `{model}` 路径变量是客户端**请求的模型名**，而非解析后的
  **上游模型名**；当配置了模型映射时，URL 里会带上错误的模型名。

`substitutePathVars` 本身工作正常——问题在于 unified 分支喂给它的路径变量集合不对。

## 修复

上游 URL 里唯一可能出现的标记就是 Gemini endpoint 的 `{model}`，它应当被替换为
**解析后的上游模型名**（`input.UpstreamModel`），而不是入站请求里的模型名。

在 `pkg/server/gateway_unified_helpers.go` 新增纯函数：

```go
// unifiedUpstreamPathVars 返回用于替换 unified 上游 URL 中标记的路径变量。
// unified 上游 URL 唯一可能携带的标记是 Gemini generate/streamGenerate
// endpoint 的 {model}，它必须解析为上游模型名——而非入站请求的模型名——
// 这样即使入站路由不带 {model}（Anthropic / OpenAI 源）的跨格式转换请求，
// 也能生成合法的 Gemini URL。
func unifiedUpstreamPathVars(upstreamModel string) map[string]string {
	if upstreamModel == "" {
		return nil
	}
	return map[string]string{"model": upstreamModel}
}
```

在 `buildRewrittenUpstreamRequest` 的 unified 分支里，用
`unifiedUpstreamPathVars(input.UpstreamModel)` 取代 `f.config.PathVars` 传给
`buildUpstreamRequest`。

### 边界说明

- 非 Gemini 上游（Anthropic / OpenAI）的 `upstreamUrl` 不含 `{model}` 标记，
  多出来的 `model` 变量不会被引用——`substitutePathVars` 忽略未使用的变量，不会报错。
- 暴露给 JS hook 的 `RequestShape.PathVars` 仍然是入站 `f.config.PathVars`（不变），
  与上游 URL 替换是两件独立的事。
- 非 unified 的路径网关（`gatewayRoutePath`）不在本次改动范围内，行为不变。

## 不做的事

- 不引入兼容层 / 回退分支。直接更新 unified 分支这一处调用点。
