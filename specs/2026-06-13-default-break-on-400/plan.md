# 执行计划

## 1. 修正 passthrough 决策回显（`pkg/jsx/session.go`）

- `RunAfterUpstreamError` 中将 `zero` 由 `AfterUpstreamErrorDecision{Break: initial.Break, StatusCode: initial.StatusCode, Message: initial.Message}` 改为 `AfterUpstreamErrorDecision{Break: initial.Break}`。
- **JS wrapper 同步修正**：no-op tap / 无 tap 时 `runWaterfall` 返回种子输入对象本身（identity），不会落到 `zero`，否则种子的 `statusCode` / `message` 仍被回显。将种子存入变量 `input`，passthrough 判定加上 `r === input`，使其返回 `undefined` 落到 `zero`。
- 更新该方法上方的注释：passthrough（含返回未改动的输入对象）沿用初始 `break`，且不携带 `statusCode` / `message` 覆盖（等价 follow-upstream）。

## 2. 网关层种入 400 默认 break（`pkg/server/gateway_flow_attempts.go`）

- 在 `runAfterUpstreamError` 中计算 `defaultBreak := statusCode == http.StatusBadRequest && !streamed`，并把它作为 `jsx.UpstreamErrorView{Break: defaultBreak, ...}` 的种子传入 `f.session.RunAfterUpstreamError`。
- 其余逻辑（`return dec, dec.Break && !streamed`）保持不变。

## 3. 更新测试（`pkg/jsx/engine_test.go`）

- 修改 `TestSession_AfterUpstreamError_Passthrough`：passthrough 现在重置 `StatusCode=0` / `Message=""`，断言改为 `dec.Break==false && dec.StatusCode==0 && dec.Message==""`，并更新注释。
- 新增 `TestSession_AfterUpstreamError_DefaultBreakSeed`：种入 `UpstreamErrorView{Break:true, StatusCode:400, Message:"bad"}` 且 hook 为 passthrough（`function(){}`），断言返回 `{break:true, statusCode:0, message:""}`（忠实透传）。
- 新增 `TestSession_AfterUpstreamError_HookOverridesDefaultBreak`：种入 `Break:true`、hook 返回 `{ break:false }`，断言 `dec.Break==false`（脚本可关闭默认透传）。

## 4. 文档（`CLAUDE.md`）

- 在 Scripts 小节 `afterUpstreamError` 段落补充：当上游状态码恰好为 `400` 且 `streamed=false` 时，`break` 默认种子为 `true`（默认透传该上游 400 响应回客户端）；其余状态码默认 `false`。hook 可读到该默认值并返回 `{ break: false }` 改写为继续尝试。

## 5. 验证

- `go build ./...`
- `go test ./pkg/jsx/ ./pkg/server/`
