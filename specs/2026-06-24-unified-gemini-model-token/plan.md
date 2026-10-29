# 执行计划

## 1. 测试（本次先写，验证问题）

文件：`pkg/server/handle_unified_gateway_test.go`

新增 `TestUnifiedUpstreamPathVars`，覆盖：

1. `unifiedUpstreamPathVars("gemini-2.5-flash")` 返回 `{"model": "gemini-2.5-flash"}`。
2. `unifiedUpstreamPathVars("")` 返回 `nil`。
3. 端到端串联：`substitutePathVars(geminiURL, unifiedUpstreamPathVars("gemini-2.5-flash"))`
   生成的 URL 中 `{model}` 已被替换为 `gemini-2.5-flash`，且不再含 `{`。

该测试引用尚未实现的 `unifiedUpstreamPathVars`，在修复前 `pkg/server` 包**无法编译**
（即红灯状态），符合「先有失败测试」的预期。修复后转绿。

## 2. 修复（`/execute-file-based-plan` 阶段执行）

### 2.1 新增 helper

`pkg/server/gateway_unified_helpers.go`：新增 `unifiedUpstreamPathVars(upstreamModel string) map[string]string`
（见 design.md）。

### 2.2 接入调用点

`pkg/server/gateway_flow_attempts.go` 的 `buildRewrittenUpstreamRequest`：unified 分支里
计算 `pathVars := unifiedUpstreamPathVars(input.UpstreamModel)`，并把传给
`buildUpstreamRequest` 的最后一个路径变量参数从 `f.config.PathVars` 改为该值。
路径网关分支继续使用 `f.config.PathVars`。

## 3. 验证

```bash
go test ./pkg/server/
```

`TestUnifiedUpstreamPathVars` 通过；其余 `pkg/server` 测试不回归。
