# 执行计划

## 1. 解析链（`pkg/server/handle_provider_endpoint.go`）

1. `extractFieldFromData(raw any, field string)` 改为 `extractFieldFromKey(raw any, containerKey, field string)`，函数体里 `obj["data"]` 换成 `obj[containerKey]`，其余不动。
2. `parseModelsResponse` 的判定链改为五条，依次：
   - `extractFieldFromKey(raw, "data", "id")`
   - `extractFieldFromKey(raw, "data", "name")`
   - `extractFieldFromKey(raw, "models", "slug")`
   - `extractFieldFromTopLevel(raw, "id")`
   - `extractFieldFromTopLevel(raw, "name")`
3. 兜底的 `logrus.Warn` 与 422 错误文案不变。

`extractFieldFromData` 无其它调用点（仅 `parseModelsResponse`），改完不留旧名。

## 2. 响应体读取上限（同文件 `handleFetchModels`）

`io.LimitReader(decoded.Body, 1024*1024)` 改为 `8*1024*1024`，并加一行注释说明 Codex 的 `/models` 每个模型内嵌 instructions 模板，单个响应就是几百 KiB 量级。

## 3. 测试（新建 `pkg/server/handle_provider_endpoint_test.go`）

`TestParseModelsResponse`，表驱动，覆盖：

- `fixtures/codex-models.json`（`os.ReadFile("../../fixtures/codex-models.json")`，与 `response_extractor_test.go` 的 fixture 读法一致）→ 期望 6 个 slug 的排序结果 `["codex-auto-review","gpt-5.4-mini","gpt-5.5","gpt-5.6-luna","gpt-5.6-terra","gpt-reserve"]`。
- `{"models":[{"slug":"b"},{"slug":""},{"slug":123},{"slug":"a"},{"slug":"a"}]}` → `["a","b"]`（空串 / 非字符串跳过、去重、排序）。
- `{"object":"list","data":[{"id":"gpt-4"}]}` → `["gpt-4"]`（OpenAI 形态未回归）。
- `[{"name":"m1"}]` → `["m1"]`（裸数组形态未回归）。
- `{"models":[]}` 与 `{"foo":1}` → 报错。
- `not json` → 报错。

## 4. 验证

```bash
go test ./pkg/server/ -run TestParseModelsResponse
go build ./...
```

无契约改动，不需要跑 `mise run openapi` 与前端类型生成。
