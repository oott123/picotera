# Proposal

在模型界面,增加一个导出价格的功能。点击之后,弹出面板,用户可以选择导出的目标汇率,复制导出的表达式。

- 当缓存读取价格和隐式缓存读取价格一样时,直接使用缓存读取价格;
- 当缓存写入和 1h 缓存写入价格一样的时候,也不要区分。

我们自动导出符合下述规则的 billing expression 表达式(该表达式语言用于**另一套** AI API 网关,本仓库不实现其求值)。

## 目标表达式语言(billing expression)

表达式基于标准算术 + 三元运算符。

### Token 变量

输入侧:

- `p` — 输入 token 数(计价用)。自动排除已单独计价的子类(如用了 `cr`,缓存 token 从 `p` 扣除)
- `len` — 输入上下文总长度(判断条件用)。不受自动排除影响,始终反映完整输入长度。用于阶梯条件
- `cr` — 缓存命中(读取)token 数
- `cc` — 缓存创建 token 数(5 分钟 TTL)
- `cc1h` — 缓存创建 token 数(1 小时 TTL,Claude 特有)
- `img` — 图片输入 token 数
- `ai` — 音频输入 token 数

输出侧:

- `c` — 输出 token 数。同样自动排除已单独计价的子类
- `img_o` — 图片输出 token 数
- `ao` — 音频输出 token 数

### p/c 自动排除

`p`/`c` 是回退变量,代表未在表达式中单独计价的所有 token。若表达式用了某个子类变量(如 `cr`),这些 token 会从 `p` 扣除以避免重复计费。未使用的子类 token 仍留在 `p`/`c` 中按基础价计。

重要:`len` 不受自动排除影响。阶梯条件应用 `len` 而非 `p`,以防缓存命中降低 `p` 而误判阶梯。

### 内置函数

- `tier(name, value)` — 标注计费阶梯;必须包裹成本表达式
- `max(a, b)`, `min(a, b)` — 最大/最小
- `ceil(x)`, `floor(x)`, `abs(x)` — 向上/向下取整、绝对值
- `header(name)` — 读取请求头
- `param(path)` — 读取请求体 JSON 路径(gjson 语法)
- `has(source, substr)` — 子串检查
- `hour(tz)`, `minute(tz)`, `weekday(tz)`, `month(tz)`, `day(tz)` — 时间函数,`tz` 如 `"Asia/Shanghai"`

### 价格系数

表达式里的数字是 $/1M tokens 单价。例如 `p * 2.5` 表示输入 $2.50/1M tokens。

### 规则

1. 每个叶子分支必须包裹在 `tier("name", cost_expr)` 中
2. 用英文阶梯名,如 `"base"`、`"standard"`、`"long_context"`
3. 阶梯条件用 `len`(不是 `p`),支持 `<`、`<=`、`>`、`>=`
4. 多阶梯用嵌套三元:`cond1 ? tier(...) : (cond2 ? tier(...) : tier(...))`
5. 价格系数是供应商官方 $/1M tokens 单价
6. 若缓存/图片/音频无需单独计价,省略对应变量;其 token 自动包含在 `p`/`c` 中

## 澄清(与用户确认的结论)

- **隐式/显式缓存读取价不一致时**:目标语言只有一个缓存读取变量 `cr`。当 `cacheRead !== implicitCacheRead` 时,取**较贵的一档**作为 `cr` 系数,并在面板上显示警告。相等时按规则合并为单个 `cr` 项。
- **目标币种**:导出系数默认用模型自身的 `Pricing.Currency`(系数即原值,无换算误差);用户可在面板切换目标币种,按汇率 `unitsPerUsd` 换算系数。
- **阶梯命名**:单一 tier 用 `"base"`;多 tier 按顺序命名 `"tier_0"`、`"tier_1"`……
- **实现范围**:纯前端功能。picotera 价格已是结构化存储且完整暴露给 dashboard,汇率经 `useExchangeRates` 可读,导出为字符串生成 + 复制到剪贴板,**不涉及后端 / OpenAPI / DB 改动**。
