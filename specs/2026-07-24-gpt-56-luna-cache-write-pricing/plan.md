# 执行计划

## 1. 调整缓存写入默认链

文件：`pkg/pricing/match.go`

修改 `fillTierDefaults`：先处理 `cacheWrite` 的缺省值，再让缺少 `cacheWriteLong` 的档位将 `CacheWrite1H` 设置为已解析的 `CacheWrite`。保留输入、缓存读取和隐式缓存读取的现有默认规则。

## 2. 更新通用转换测试

文件：`pkg/pricing/match_test.go`

- 修改缺少缓存字段的测试，明确断言 `CacheWrite` 和 `CacheWrite1H` 都复用输入价格。
- 增加一个普通缓存写入价格已提供、1h 缓存写入价格缺失的测试，断言 `CacheWrite1H` 复用 `CacheWrite` 而不是 `Input`。
- 保留已有显式缓存字段和分档转换断言，确保提供 `cache_write_long` 时不被覆盖。

## 3. 增加 gpt-5.6-luna 回归测试

在 `pkg/pricing/match_test.go` 中调用 `Match("gpt-5.6-luna", ...)`，找到精确模型候选并断言：

- 模型价格为 USD。
- 两个输入上下文档存在，阈值为 `0` 和 `272000`。
- 第一档 `input=1`、`output=6`、`cacheRead=0.1`、`cacheWrite=1.25`、`cacheWrite1h=1.25`。
- 第二档 `input=2`、`output=9`、`cacheRead=0.2`、`cacheWrite=2.5`、`cacheWrite1h=2.5`。

测试通过实际嵌入价格目录验证匹配结果，而不是复制转换函数的输入数据。

## 4. 验证

运行 `go test ./pkg/pricing/...`，确认价格转换和匹配回归通过。检查计划文件只描述上述确定方案，不包含替代方案、兼容分支或未完成措辞。
