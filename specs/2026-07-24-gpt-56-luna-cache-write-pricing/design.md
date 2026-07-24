# 设计

## 根因

内置价格目录中的 `gpt-5.6-luna` 记录来自 OpenAI 价格数据，包含按输入上下文分档的 `input`、`output`、`cache_read`、`cache_write`，没有 `cache_write_long`。`pkg/pricing/match.go` 的 `fillTierDefaults` 当前独立处理缺失字段：`cacheWrite` 缺失时回退到 `input`，`cacheWrite1H` 缺失时也直接回退到 `input`。因此对于该模型，匹配结果为普通缓存写入 `$1.25`、1h 缓存写入 `$1`，错误地把输入价当成了长缓存写入价。

## 方案

调整 `fillTierDefaults` 的默认链：

1. 缺少普通缓存写入价时，继续使用该档输入价作为 `cacheWrite`。
2. 缺少 1h 缓存写入价时，使用已经确定的 `cacheWrite`，而不是再次使用 `input`。
3. 明确提供 `cache_write_long` 的记录保持原值，不受影响。

这样没有独立 1h 价格的模型会复用普通缓存写入价格；`gpt-5.6-luna` 的每个输入上下文档将得到 `cacheWrite1H == cacheWrite`，分别为 `$1.25` 和 `$2.50`。这符合当前价格匹配数据模型中“缺少缓存字段使用已有同类价格”的默认策略，也避免依赖输入价覆盖已知缓存写入价。

## 范围与接口

- 修改 `pkg/pricing/match.go` 的内部默认值转换逻辑。
- 修改 `pkg/pricing/match_test.go`，覆盖默认链和 GPT-5.6 Luna 回归。
- 不修改前端组件、API contract、OpenAPI、数据库或价格目录。
- 不引入第三方库、兼容层或宽松输入处理。
