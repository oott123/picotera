# API：价格表在线更新

## 新增

### `POST /api/picotera/pricing/refresh`

- OperationID：`refreshPricing`，注册在 `admin` 组（非管理员 403），位于 `OperationMatchPricing` 旁。
- Summary：`Fetch the online pricing catalog and replace the in-memory one`
- 请求体：无。

行为：按顺序拉取配置的地址（未配置 `PICOTERA_PRICING_URL` 时为 GitHub → CNB），每个地址超时 30 秒，第一个成功的结果替换内存中的价格表。

成功响应 `200`（`RefreshPricingResponse`）：

```go
type RefreshPricingResponse struct {
	Body struct {
		// The url the installed catalog was fetched from.
		SourceURL string `json:"sourceUrl" example:"https://raw.githubusercontent.com/oott123/picotera/refs/heads/master/pkg/pricing/pricing.json"`
		// The catalog's own generated_at field, verbatim.
		GeneratedAt string `json:"generatedAt" example:"2026-10-08T07:54:49.447687Z"`
	}
}
```

失败响应 `502`：所有地址都失败。`detail` 为 `failed to refresh pricing catalog`，`errors` 中包含每个地址的失败原因。内存中的价格表不变。

## 变更

### `POST /api/picotera/pricing/matches`

请求与响应结构不变。匹配数据源从「每次解析内嵌价格表」改为「内存中当前生效的价格表」。Summary 改为 `Match pricing candidates for a model against the current pricing catalog`。
