# Overview Sankey · 设计

**日期**: 2026-05-09
**范围**: 在概览页「分布统计」上方新增 Sankey 区块，用 SegmentedControl 切换 5 张图。

## 目标

让运维 / ML 工程师在概览页一眼看清：

- Token 在「输出」与「输入」中的分布，输入又如何分布到 4 类缓存桶。
- Token 与费用如何沿 (渠道 → 上游模型 → 请求模型 → API Key) 链路流动，以及反向 (API Key → 请求模型 → 上游模型 → 渠道)。

5 张 sankey 共享同一组顶层过滤器（时间范围 / API Key / 模型 / 上游 / 渠道）和同一份后端响应。

## 用户控制 & 标签命名

新增 `SegmentedControl`，5 个标签使用「结构语义型」命名：

| variant key | 标签 | 流向 |
|---|---|---|
| `tokenComposition` | Token 构成 | 总 Token → 输出 / 输入 → 4 个输入桶 |
| `tokensIn` | Token 上游 | 总 Token → 渠道 → 上游模型 → 请求模型 → API Key |
| `tokensOut` | Token 下游 | 总 Token → API Key → 请求模型 → 上游模型 → 渠道 |
| `costIn` | 费用上游 | 总费用 → (同 B 顺序) |
| `costOut` | 费用下游 | 总费用 → (同 C 顺序) |

每张图都有显式 root 节点：A/B/C 为 `总 Token`，D/E 为 `总费用`（多币种原始模式下追加币种后缀，例如 `总费用 · USD`）。

## 数据语义

`request_overview_hourly` 的 5 个 token 列互斥：`input_tokens`（未命中缓存）、`cache_read_tokens`、`cache_write_tokens`（5 分钟）、`cache_write_1h_tokens`（1 小时）、`output_tokens`。**总计 = 这 5 项之和**，与现有 `OverviewSummaryView.totalTokens` 公式一致。

图 A 输入分支必须含 4 个叶子（含「未缓存输入」），保证叶子总和与「总 Token - 输出」严格相等。

## 后端

### 扩展 `OverviewSummaryView`

`pkg/contract/overview.go` 新增两个字段：

```go
type OverviewTokenBreakdownView struct {
    Input        int64 `json:"input"`        // 未缓存输入
    CacheRead    int64 `json:"cacheRead"`
    CacheWrite   int64 `json:"cacheWrite"`
    CacheWrite1h int64 `json:"cacheWrite1h"`
    Output       int64 `json:"output"`
}

type OverviewBreakdownRowView struct {
    ApiKeyID      int32              `json:"apiKeyId"`      // 0 = 缺失
    Model         string             `json:"model"`         // "" = 缺失
    UpstreamModel string             `json:"upstreamModel"` // "" = 缺失
    ProviderID    int32              `json:"providerId"`    // 0 = 缺失
    TotalTokens   int64              `json:"totalTokens"`
    Costs         []OverviewCostView `json:"costs"`
}

type OverviewSummaryView struct {
    Window          OverviewWindowView         `json:"window"`
    TotalTokens     int64                      `json:"totalTokens"`
    TotalRequests   int64                      `json:"totalRequests"`
    TotalTraceCount int64                      `json:"totalTraceCount"`
    Costs           []OverviewCostView         `json:"costs"`
    TokenBreakdown  OverviewTokenBreakdownView `json:"tokenBreakdown"`
    Breakdown       []OverviewBreakdownRowView `json:"breakdown"`
}
```

不新增 endpoint。前端只需现有 `useQuery(getOverviewSummary)` 一次拿全 5 张图所需数据。

### 新增 sqlc 查询

`db/queries/overview.sql` 增加 **3** 条查询，避免 token 在 currency 维度上被重复计数：

1. `GetOverviewTokenBreakdown :one` —— 返回 5 个 token 桶的 SUM，过滤条件与 `GetOverviewTotals` 一致。
2. `ListOverviewBreakdownTokens :many` —— 按 `(api_key_id, model, upstream_model, provider_id)` GROUP BY，输出每组的 `total_tokens`（5 桶之和）。NULL 维度回填为 `0` / 空字符串。
3. `ListOverviewBreakdownCosts :many` —— 按 `(api_key_id, model, upstream_model, provider_id, cost_currency)` GROUP BY，输出每组每币种的 `cost`。仅保留 `cost_currency` 非空的记录。

`pkg/server/handle_overview.go` 的 `GetOverviewSummary` 处理器：
- 并行触发上述查询及 `GetOverviewTokenBreakdown` / 现有 `CountTraces`。
- 在 Go 端用 `(api_key_id, model, upstream_model, provider_id)` 作为 map key 把 tokens 行与 costs 行合并成 `[]OverviewBreakdownRowView`。
- 仅出现在 costs 中而 tokens 全为 0 的组合也保留（防止 cost-only 行被丢）。

### 行规模

经验估计：24h 内 (api_key × model × upstream × provider × currency) 组合 ~几百行，30d 上千行。远小于现有 series 接口的逐小时点数；JSON 响应增加几十 KiB 量级，单次 GET 内可接受。

## 前端

### 新组件

`dashboard/src/components/charts/OverviewSankey.vue` —— 薄包装 `@unovis/vue` 的 `VisSingleContainer + VisSankey`。

```ts
interface SankeyNode { id: string; label: string; layer?: number }
interface SankeyLink { source: string; target: string; value: number }
defineProps<{
  nodes: SankeyNode[]
  links: SankeyLink[]
  valueFormat: (v: number) => string
}>()
```

- 颜色：复用 `charts/colors.ts`，同一 `layer` 内按 value 降序映射调色板；`__other__` 节点固定灰色 (`text-ink-faint`)。
- Tooltip：hover link → `源 → 目标 · valueFormat(value)`；hover node → `节点 label · valueFormat(累加值)`。
- 容器高度：固定 `h-72` (288px)。

### `OverviewView.vue` 变更

1. 在「Bento totals」与「Distribution」之间插入 Sankey 区块：
   - `SegmentedControl` 绑定新的 `sankeyVariant ref<'tokenComposition'|'tokensIn'|'tokensOut'|'costIn'|'costOut'>`，初始值 `'tokenComposition'`。
   - 单列布局：`grid-cols-1`（不像 donut 那样在 `lg:` 切两列）。
   - `DataCard` 内根据 variant 渲染对应 nodes/links。
2. 5 个 variant 的 nodes/links 由前端 `computed` 构造，纯派生自 `summary.tokenBreakdown` / `summary.breakdown`。
3. 状态分支与 donut 完全对齐：`isLoading` / `isError` / `nodes.length === 0 || links.length === 0` (空态) → `<StateText>`，否则渲染 `OverviewSankey`。
4. 多币种（D / E）：在原始模式下，按 `breakdown` 中出现的所有 `currency` 渲染 `v-for` 多张 `DataCard`，每张 sankey 根 label 追加 ` · {currency}`；目标币种模式下汇总为单图。

### Sankey 构造逻辑

**`tokenComposition`** —— 直接展开 5 桶：

```
nodes = [
  {id:'root', label:'总 Token', layer:0},
  {id:'output', label:'输出', layer:1},
  {id:'input',  label:'输入', layer:1},
  {id:'in_uncached', label:'未缓存输入', layer:2},
  {id:'in_cache_read',     label:'缓存读取',     layer:2},
  {id:'in_cache_write',    label:'缓存写入',     layer:2},
  {id:'in_cache_write_1h', label:'长期缓存写入', layer:2},
]
links =
  ('root'→'output', tokenBreakdown.output),
  ('root'→'input',  tokenBreakdown.{input+cacheRead+cacheWrite+cacheWrite1h}),
  ('input'→'in_uncached',         tokenBreakdown.input),
  ('input'→'in_cache_read',       tokenBreakdown.cacheRead),
  ('input'→'in_cache_write',      tokenBreakdown.cacheWrite),
  ('input'→'in_cache_write_1h',   tokenBreakdown.cacheWrite1h),
```

**`tokensIn` / `tokensOut`** —— 5 层 (root + 4 维度)，layer 顺序：
- `tokensIn`: provider → upstreamModel → model → apiKey
- `tokensOut`: apiKey → model → upstreamModel → provider

每行 `breakdown` 提供 4 维 + tokens；构造算法：

```
for row in breakdown:
  links[(root, layer1.key(row))] += row.totalTokens
  links[(layer1.key(row), layer2.key(row))] += row.totalTokens
  links[(layer2.key(row), layer3.key(row))] += row.totalTokens
  links[(layer3.key(row), layer4.key(row))] += row.totalTokens
```

**`costIn` / `costOut`** —— 同 tokensIn/tokensOut，但 `value = row.costs[currency].amount`（原始模式按 currency 拆图）或 `Σ ccy.convert(...)`（目标币种模式）。

**Top-N 截断**（每层独立）：

```
for layer L in [1..4]:
  group rows by L.key, sum value
  keep top 8, fold remainder into '__other__@L'
  rewrite all links touching folded keys → '__other__@L'
  re-sum links by (source, target)
```

L=0 (root) 不参与截断。`__other__@L` 节点 label 显示「其他」。

### 节点 label 解析

复用 `dimensionLabel(dim, key)`，扩展：

- `apiKey` / `provider` 维度 ID 为 0 → 显示「未知」。
- `model` / `upstreamModel` 空字符串 → 显示「未知」。
- `__other__@L` → 显示「其他」。

Token 桶 label 写死中文：`输出 / 输入 / 未缓存输入 / 缓存读取 / 缓存写入 / 长期缓存写入`。

### 值格式化

- Token：复用 `compactNumber(v)`。
- 费用：复用 `formatCurrencyCompact(v, code)`，`code` 取当前图的 currency（目标币种或原始币种）。

## 测试

- **后端单测**：`pkg/server/handle_overview_test.go` 新增用例覆盖：
  - `tokenBreakdown` 5 桶累加正确。
  - `breakdown` 4 维分组、NULL → 0/空占位。
  - 过滤器（apiKeyId / model / upstreamModel / providerId）正确传播到新查询。
- **手动验证**：种入多 provider × 多 API key × 多模型样例数据：
  - 5 个 segment 切换无报错，链路总值守恒（总 Token / 总费用 = root 出度之和）。
  - Top 8 截断生效，「其他」节点 label 正确。
  - 原始模式 / 目标币种模式切换时 D / E 的 card 数量正确。
  - 空数据时各 variant 显示「暂无数据」。
- **OpenAPI 同步**：`mise run openapi` → `pnpm --dir dashboard generate-openapi`。
- **type-check & lint**：`pnpm --dir dashboard type-check && pnpm --dir dashboard lint`。

## 范围外

- 不抽通用 sankey 给其他视图复用。
- 不做 click-to-drill-down，不持久化 segment 选中。
- sankey 不叠加时间维度。
- 不引入新颜色 token。
- 不为 sankey 单独建 endpoint —— 全部走扩展后的 `/overview/summary`。

## 风险

- **breakdown 行膨胀**：极端配置下（数百 API key × 数百模型）单次响应可能数 MB。若实际部署中观测到回归，再考虑分页或拆 endpoint。
- **Unovis Sankey 自适应**：节点过多时垂直空间紧张。固定 `h-72` 是初始选择，必要时调到 `h-96`，不在本设计内反复调整。
