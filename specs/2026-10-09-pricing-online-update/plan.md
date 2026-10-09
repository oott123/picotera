# 执行计划

设计见 `design.md`，接口见 `api.md`。

## 1. `pkg/pricing`

1. `match.go`：
   - `catalog` 新增 `SchemaVersion`、`GeneratedAt` 字段。
   - 删除 `loadCatalog`；`//go:embed` 与 `pricingFS` 移到 `source.go`。
   - 包级 `Match` 改为 `func (c *catalog) match(target string, limit int) []contract.PricingMatchCandidate`，循环与排序逻辑原样保留，去掉 error 返回值。
2. 新建 `source.go`：`DefaultURLs`、`fetchTimeout`、`maxCatalogSize`、`schemaVersion`、`Source`、`RefreshResult`、`NewSource`、`parseCatalog`、`(*Source).Refresh`、`(*Source).Match`、单地址拉取辅助函数 `(*Source).fetch(ctx, url) (*catalog, error)`，行为见 `design.md`「`pkg/pricing`」。
3. `go mod tidy`，确认 `golang.org/x/sync` 移到直接依赖。
4. 测试：
   - `match_test.go`：现有 `Match(...)` 调用改为对内嵌价格表的 `catalog.match(...)`（在测试中通过 `parseCatalog` 解析 `pricingFS` 得到），去掉 `err` 检查。
   - 新建 `source_test.go`，用 `httptest.Server`：
     - 第一个地址返回 500、第二个返回合法价格表 → `Refresh` 成功，`RefreshResult.URL` 为第二个地址，`Match` 结果来自新价格表（构造一个内嵌表中不存在的模型 ID，验证 score 0 命中）。
     - 全部地址失败（非 200、非法 JSON、`schema_version` 为 `"2.0"`、`providers` 为空各一个）→ 返回错误且错误信息包含每个地址；`Match` 结果仍来自原价格表。
     - 响应体超过 `maxCatalogSize` → 该地址失败（测试中临时调小包级变量 `maxCatalogSize`，结束时恢复）。
     - `NewSource(nil)` 返回错误。
     - 内嵌价格表通过 `parseCatalog` 校验。

## 2. 配置

1. `pkg/configx/configx.go`：`Config` 新增 `PricingURL`（`mapstructure:"pricing_url"`），`Parse` 中非空时校验 scheme ∈ {`http`, `https`} 且 host 非空，错误文案 `pricing_url must be an absolute http(s) url`。

## 3. Contract

1. `pkg/contract/pricing_match.go`：
   - `OperationMatchPricing` 的 Summary 改为 `api.md` 中的新文案。
   - 新增 `RefreshPricingResponse` 与 `OperationRefreshPricing`（`POST /pricing/refresh`，OperationID `refreshPricing`）。

## 4. 服务端

1. `pkg/server/server.go`：
   - `Server` 新增 `pricing *pricing.Source`。
   - `NewServer` 中按 `design.md`「服务端」选择地址并调用 `pricing.NewSource`，赋值到 `server.pricing`；`registerEndpoints()` 之后启动 `go server.refreshPricingAtStartup(ctx)`。
   - `register()` 在 `OperationMatchPricing` 旁以 admin 组注册 `OperationRefreshPricing`。
2. `pkg/server/handle_pricing_match.go`：改用 `s.pricing.Match`，删除 500 分支。
3. 新建 `pkg/server/handle_pricing_refresh.go`：`handleRefreshPricing` 和 `refreshPricingAtStartup`，日志字段见 `design.md`。

## 5. OpenAPI 与 TS 类型

1. `mise run openapi`。
2. `pnpm --dir dashboard generate-openapi`。

## 6. 前端

1. `dashboard/src/api/client.ts`：在 `matchPricing` 后新增 `refreshPricing()`。
2. `dashboard/src/components/ModelPricingMatchPanel.vue`：按 `design.md`「前端」新增 `refreshMutation` / `refreshing`，`load()` 改为 `updateOnline()`，按钮文案改为「在线更新」，调整两个按钮的禁用条件与图标状态。

## 7. 文档

1. 按 `design.md`「文档」更新根目录 `CLAUDE.md`。不改 `README.md`。

## 8. 验证

1. `go build ./...`、`go test ./pkg/pricing/ ./pkg/server/`。
2. `pnpm --dir dashboard type-check`、`pnpm --dir dashboard lint`、`pnpm --dir dashboard format`。
3. `mise run server` 启动，日志中出现启动拉取的成功或失败记录且服务正常监听；在模型列表打开价格匹配面板点击「在线更新」，候选重新加载。将 `PICOTERA_PRICING_URL` 设为不可达地址重启，点击「在线更新」，面板显示错误且候选列表不变。
