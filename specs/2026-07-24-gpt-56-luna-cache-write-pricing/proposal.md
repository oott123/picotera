# GPT-5.6 Luna 1h 缓存写入价格匹配

前端模型页面通过价格匹配接口选择内置价格。匹配 `gpt-5.6-luna` 时，官方价格包含输入 `$1 / 1M tokens`、输出 `$6 / 1M tokens`、缓存读取 `$0.10 / 1M tokens` 和缓存写入 `$1.25 / 1M tokens`，但没有独立的 1h 长缓存写入价格。

当前价格转换在缺少 `cache_write_long` 时把 `cacheWrite1h` 默认成输入价格，因此该模型被匹配后得到 `cacheWrite1h = 1`，而不是与已有的缓存写入价格 `cacheWrite = 1.25` 相同。

将缺失的 1h 缓存写入价格默认为同一档的 `cacheWrite`，使没有独立长缓存价的模型复用普通缓存写入价。更新相关单元测试，并增加 GPT-5.6 Luna 的回归断言；不改变已明确提供 `cache_write_long` 的模型价格。
