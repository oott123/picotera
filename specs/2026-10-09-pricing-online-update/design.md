# 设计：价格表在线更新

## 现状

- `pkg/pricing/match.go` 用 `//go:embed pricing.json` 内嵌价格表。包级函数 `Match(target, limit)` 每次调用都通过 `loadCatalog()` 重新读取并解析整份 JSON（约 1.9 MB）。
- `POST /api/picotera/pricing/matches`（admin，`handleMatchPricing`）调用 `pricing.Match(..., 8)`。
- 前端 `ModelPricingMatchPanel.vue` 打开时调用这个接口；「重新匹配」按钮只是对同一接口 `refetch`，用的仍是内嵌价格表。

## 方案概览

1. `pkg/pricing` 新增 `Source` 类型：持有当前生效的已解析价格表（`atomic.Pointer`），初始值是内嵌价格表，`Refresh` 按顺序尝试远程地址，第一个成功的结果原子替换当前价格表。匹配逻辑改为 `Source` 的方法，不再每次解析 JSON。
2. 配置新增 `PICOTERA_PRICING_URL`（单个 URL）。未配置时使用内置的两个地址，顺序为 GitHub → CNB。
3. `NewServer` 创建 `Source` 后，启动一个后台 goroutine 执行一次 `Refresh`，不阻塞启动。
4. 新增 admin 接口 `POST /api/picotera/pricing/refresh` 触发一次 `Refresh`。
5. 前端按钮文案改为「在线更新」：先调用刷新接口，成功后让所有价格匹配查询失效并重新获取当前模型的候选。

远程拉取的价格表只存在进程内存中，进程重启后回到内嵌价格表，再由启动时的后台拉取重新替换。

## `pkg/pricing`

### 文件划分

- `match.go`：保留类型定义、`convert*` 系列函数和打分逻辑。删除包级 `Match` 与 `loadCatalog`，打分循环移到 `func (c *catalog) match(target string, limit int) []contract.PricingMatchCandidate`（逻辑不变，去掉 error 返回值）。
- 新文件 `source.go`：`Source`、默认地址、远程拉取与校验、内嵌价格表解析。

### 类型与常量

```go
// DefaultURLs is tried in order when no pricing url is configured.
var DefaultURLs = []string{
	"https://raw.githubusercontent.com/oott123/picotera/refs/heads/master/pkg/pricing/pricing.json",
	"https://cnb.cool/brynhild-inc/picotera/-/git/raw/master/pkg/pricing/pricing.json",
}

const (
	fetchTimeout  = 30 * time.Second
	schemaVersion = "1.0"
)

// maxCatalogSize is a var so tests can lower it.
var maxCatalogSize int64 = 64 << 20 // 64 MiB

type Source struct {
	urls    []string
	client  *http.Client
	current atomic.Pointer[catalog]
	group   singleflight.Group
}

// RefreshResult describes the catalog a successful Refresh installed.
type RefreshResult struct {
	URL         string
	GeneratedAt string
}
```

`catalog` 结构体新增 `SchemaVersion string \`json:"schema_version"\`` 和 `GeneratedAt string \`json:"generated_at"\``。

### `NewSource(urls []string) (*Source, error)`

- `urls` 为空时返回错误（调用方总会传入默认地址或配置的地址）。
- 解析内嵌 `pricing.json` 并经过与远程相同的 `parseCatalog` 校验，失败直接返回错误——内嵌文件随二进制发布，损坏属于构建错误，`NewServer` 随之启动失败。
- `client` 为 `&http.Client{}`（使用 `http.DefaultTransport`，遵循 `HTTP_PROXY` / `HTTPS_PROXY` 环境变量）。超时由每次请求的 context 控制，不设置 `Client.Timeout`。

### `parseCatalog(raw []byte) (*catalog, error)`

内嵌和远程共用的严格校验：

- `json.Unmarshal` 成功；不启用 `DisallowUnknownFields`，因为文件中有大量匹配用不到的字段（`sources`、`source_url` 等）。
- `schema_version` 必须恰好等于 `"1.0"`，否则返回 `unsupported pricing schema_version %q`。
- `providers` 至少一项，否则返回 `pricing catalog has no providers`。

### `(*Source).Refresh(ctx context.Context) (RefreshResult, error)`

- 通过 `singleflight.Group.Do("refresh", ...)` 合并并发调用：启动时的后台拉取与用户点击、或多个用户同时点击，共享同一次拉取的结果。
- 共享的拉取函数使用 `context.WithoutCancel(ctx)`，避免发起者断开连接导致其他等待者一起失败。
- 按 `urls` 顺序逐个尝试，每个地址单独 `context.WithTimeout(..., fetchTimeout)`：
  - `GET` 请求；响应状态码必须为 `200`，否则该地址失败（错误中带状态码）。
  - 用 `io.LimitReader(body, maxCatalogSize+1)` 读取，超过 `maxCatalogSize` 视为失败。
  - 不检查 `Content-Type`（GitHub raw 返回 `text/plain`）。
  - `parseCatalog` 失败视为该地址失败。
  - 第一个成功的地址：`current.Store(cat)`，返回 `RefreshResult{URL, GeneratedAt}`，不再尝试后续地址。
- 全部失败：不修改 `current`，返回 `errors.Join` 后的错误，每条形如 `<url>: <原因>`。

### `(*Source).Match(target string, limit int) []contract.PricingMatchCandidate`

`return s.current.Load().match(target, limit)`。

### 依赖

`golang.org/x/sync/singleflight`。`golang.org/x/sync` 已在 `go.mod` 中作为间接依赖存在，`go mod tidy` 后变为直接依赖，不引入新模块。

## 配置

`configx.Config` 新增：

```go
// PricingURL replaces pricing.DefaultURLs as the only source of the online
// pricing catalog. Empty means the defaults.
PricingURL string `mapstructure:"pricing_url"`
```

对应环境变量 `PICOTERA_PRICING_URL`，无默认值。非空时在 `Parse` 中校验：`url.Parse` 成功、scheme 为 `http` 或 `https`、host 非空，否则启动失败（`pricing_url must be an absolute http(s) url`）。不做 trim 等任何规范化。

## 服务端

- `Server` 新增字段 `pricing *pricing.Source`。
- `NewServer`：
  ```go
  pricingURLs := pricing.DefaultURLs
  if config.PricingURL != "" {
  	pricingURLs = []string{config.PricingURL}
  }
  pricingSource, err := pricing.NewSource(pricingURLs)
  ```
  失败返回 `failed to load pricing catalog: %w`。构造 `server` 之后启动 `go server.refreshPricingAtStartup(ctx)`：使用 `context.WithoutCancel(ctx)` 调用 `Refresh`，成功时 Info 日志带 `url`、`generatedAt`，失败时 Warn 日志带错误并说明继续使用内嵌价格表。
- `NewHuma()` 构造的 `Server` 不设置 `pricing`，它只用于生成 OpenAPI，不会调用处理函数。
- `handleMatchPricing`：改为 `s.pricing.Match(input.Body.TargetModel, 8)`，删除不再可能出现的 500 分支。
- 新文件 `handle_pricing_refresh.go` 的 `handleRefreshPricing`：调用 `s.pricing.Refresh(ctx)`；失败返回 `huma.Error502BadGateway("failed to refresh pricing catalog", err)`；成功记 Info 日志并返回 `RefreshPricingResponse`。

## 前端

`ModelPricingMatchPanel.vue`：

- 新增 `refreshMutation = useMutation({ mutationFn: refreshPricing })`。
- `load()` 改名为 `updateOnline()`：
  1. 清空 `error`；
  2. `await refreshMutation.mutateAsync()`；
  3. `await queryClient.invalidateQueries({ queryKey: queryKeys.pricingMatches.all })`——其他模型已缓存的候选也基于旧价格表，一并失效；当前面板的查询处于活跃状态，会随之重新获取；
  4. `selectedIndex.value = candidates.value.length ? 0 : -1`。
  任一步抛错时 `error.value` 显示错误消息（默认文案「在线更新价格表失败」），候选列表保持不变。
- 新增 `refreshing = computed(() => refreshMutation.isPending.value)`。按钮在 `loading || refreshing || saving` 时禁用；图标在 `loading || refreshing` 时显示旋转的 `loader`；文案为「在线更新」。「保存价格」按钮的禁用条件同样加入 `refreshing`。

`api/client.ts` 新增 `refreshPricing()`，调用 `POST /api/picotera/pricing/refresh`，失败时 `fail(error, '在线更新价格表失败')`。

## 文档

`CLAUDE.md`：

- Package Layout 中 `pkg/pricing/` 一行补充：内嵌价格表为初始值，启动时后台拉取远程价格表并在内存中替换，`POST /pricing/refresh` 手动刷新，`PICOTERA_PRICING_URL` 覆盖默认的 GitHub → CNB 地址。
- 「User isolation & authorization」admin 列表中 `match-pricing` 处加上 `refresh-pricing`。
