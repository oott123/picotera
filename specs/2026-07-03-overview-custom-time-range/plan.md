# Plan — 概览页面自定义时间范围

## 1. 后端契约

- [ ] `pkg/contract/overview.go`：`OverviewCommonRequest.Range` 枚举改为 `1d,7d,1m,custom`；新增 `StartAt`、`EndAt`（`query:"startAt,omitempty"` / `query:"endAt,omitempty"`）。
- [ ] `pkg/contract/admin_overview.go`：`AdminOverviewCommonRequest` 同上。

## 2. 后端窗口与桶宽逻辑

修改 `pkg/server/handle_overview.go`：

- [ ] 删除旧 `overviewSeriesBucketInterval`（rangeKey 版）与旧 `overviewWindowAligned`，全部调用点改为新函数。
- [ ] 新增 `overviewWindow(rangeKey, startAt, endAt, now, align)`：`custom` 走 `parseOverviewCustomWindow`，其余走 `overviewPresetWindow`。
- [ ] 新增 `presetLookback(rangeKey)` 与 `overviewAutoBucket(span)`。
- [ ] 重写 `overviewSeriesBucketIntervalFor(bucketKey, span)`：`auto` 用 `overviewAutoBucket`；`10m` 在 `span > 7d` 时拒绝；其余桶不变。
- [ ] 删除旧 `overviewSeriesBucketInterval`（rangeKey 版）与旧 `overviewWindowAligned`（或保留为薄封装兼容，但优先直接替换所有调用点）。
- [ ] 新增 `resolveOverviewSeriesWindow(rangeKey, startAt, endAt, bucketKey, now)`：一次性返回 `(start, end, bucketInterval)`，处理预设/自定义分支与 align。

## 3. 后端处理器

- [ ] `handle_overview.go`：summary / distribution / speed-boxplot 改用 `overviewWindow(in.Range, in.StartAt, in.EndAt, time.Now(), time.Hour)`。
- [ ] `handle_overview.go`：series 改用 `resolveOverviewSeriesWindow(in.Range, in.StartAt, in.EndAt, in.Bucket, time.Now())`。
- [ ] `handle_admin_overview.go`：summary / distribution / speed-boxplot / series 同上改造。

## 4. 后端测试

- [ ] `pkg/server/handle_overview_test.go`：更新 `TestOverviewWindow`、`TestOverviewSeriesBucketInterval`、`TestOverviewBucketCount` 以适配新签名（span-based）。
- [ ] 新增 `TestOverviewCustomWindow`：合法/缺端点/逆序/非法格式用例。
- [ ] 新增 `TestOverviewSeriesBucketIntervalFor` 的 `10m` 跨度边界用例（≤7d 允许、>7d 拒绝）。
- [ ] 运行 `go test ./pkg/server/...`。

## 5. 前端类型与数据层

- [ ] `dashboard/src/api/index.ts`：`OverviewRange` 加 `'custom'`。
- [ ] `dashboard/src/api/queryKeys.ts`：`OverviewFilters` / `AdminOverviewFilters` 的 `range` 类型加 `'custom'`，新增可选 `startAt` / `endAt`。
- [ ] `dashboard/src/api/client.ts`：`overviewQuery` / `adminOverviewQuery` 在 `range === 'custom'` 时附带 `startAt` / `endAt`。

## 6. 前端页面

`OverviewView.vue`：

- [ ] `filters` 加 `startAt: ''`、`endAt: ''`。
- [ ] `rangeOptions` 加 `{ value: 'custom', label: '自定义' }`。
- [ ] 模板：时间范围分段控件后，`filters.range === 'custom'` 时渲染 `TimeRangeFilter`（`:model-value="{ startAt: filters.startAt, endAt: filters.endAt }"`，`@update:model-value` 回写）。
- [ ] `overviewFilters` 计算属性：`range === 'custom'` 时附带 `startAt` / `endAt`。
- [ ] `granularityOptions`：`10m` 可用条件改为「非 `1m` 预设 且（非 `custom` 或跨度 ≤ 7d）」。
- [ ] watch：`10m` 不可用时回落 `auto`（覆盖切 `1m` 与自定义超跨度）。

`AdminOverviewView.vue`：对称改动（`userId` 代替 `apiKeyId`/`projectId`，无项目/密钥维度）。

## 7. 生成与验证

- [ ] `mise run openapi` 重新生成 `openapi.yaml`。
- [ ] `pnpm --dir dashboard generate-openapi` 重新生成 `dashboard/src/openapi-types.d.ts`。
- [ ] `go build ./...` 通过。
- [ ] `go test ./pkg/server/...` 通过。
- [ ] `pnpm --dir dashboard type-check` 通过。
- [ ] 手动验证：概览页切「自定义」出现时间选择框；选时间后图表按窗口更新；切回预设范围行为不变。
