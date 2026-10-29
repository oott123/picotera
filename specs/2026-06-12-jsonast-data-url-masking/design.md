# Design: JSON AST 工具库 + 大 data-url 脱敏

## 总览

两层交付物：

1. **`pkg/jsonast`** — 通用 JSON AST 库：解析为可变树、区分 object key 与 value、遍历/替换字符串、序列化还原。
2. **`pkg/datamask`** — 基于 jsonast 的 data-url 脱敏器（Masker），在 JS hook 边界把超长 data-url 字符串替换为 `picotera://data-url/<id>?...` 占位符，rewriteRequest 结束后还原。

加上 `pkg/jsx` 与 `pkg/server` 的接入改造、`pkg/configx` 的阈值配置。

## pkg/jsonast

### 第三方依赖

引入 `github.com/go-json-experiment/json`（即未来的 `encoding/json/v2`），只使用其 `jsontext` 子包做 token 级词法：它正确处理字符串转义、UTF-8、数字语法，且能以原始字节读取标量。AST 层（树结构、遍历、变更、序列化）自研。

### 节点模型

单一具体结构体 `Node`（指针可变树），`Kind` 区分六种 JSON 类型。设计要点：

- **Object 保留成员顺序**：`Members []Member`，`Member{Key string, Value *Node}`。Key 以解码后字符串为准，序列化时重新编码（key 通常很短，转义归一化无影响）。
- **标量原文保留**：string 与 number 节点同时持有原始字节切片（指向输入 buffer，零拷贝）与解码值。未被修改的节点序列化时原样写回原始字节——字符串转义形式、数字精度（如 `1e10`、超 int64 位数的大整数）全部不变。调用 `SetString` 等修改方法后，原始字节作废，按解码值重新编码。
- **key 与 value 的区分由结构本身体现**：key 不是 `Node`，`Walk` / `WalkStrings` 只访问 value 节点；要扫描/替换 key 时直接迭代 `Members`。这正是「只替换 value 不碰 key」需求的承载方式。

### Roundtrip 保证

`Encode(Parse(x))` 保证语义相等 + key 顺序不变 + 未修改标量字节级不变；不保证整体 byte-identical（token 之间的空白会被丢弃，输出为 compact 格式）。需要 byte-identical 的场景（脱敏无命中时）由调用方拿原始字节直接透传解决——`datamask.Masker.Mask` 无命中时原样返回输入切片。

解析严格（fail fast）：输入必须恰好是一个完整 JSON 值，尾部多余内容、非法语法一律报错；不做任何宽容修复。

## pkg/datamask

### Masker

每个网关请求一个 `*datamask.Masker` 实例，挂在 `gatewayFlow` 上，跨 hook、跨 attempt 共享，保证同一 data-url 始终映射到同一占位符。

- **识别规则**（只看 string value，永不碰 key）：解码后字节长度 ≥ 阈值（默认 30720），以 `data:` 开头，且前 256 字节内含 `,`（data URL 头部以逗号结束）。
- **占位符**：`picotera://data-url/<id>?mediaType=<m>&encoding=base64&length=<n>`。`id` 为 crypto/rand 16 hex 字符；`mediaType` 取 `data:` 与首个 `;` 或 `,` 之间的部分（URL 编码，空则省略参数）；`encoding=base64` 仅在 `;base64` 存在时出现；`length` 恒为原始字符串字节长度。
- **去重**：按原始值缓存，同一 data-url 多次 Mask（两次 serializeClientRequest、多个 attempt）得到同一占位符。
- **Mask 失败安全降级**：`Mask` 返回 error 时调用方记 warn 日志并使用未脱敏 body——此时 map 里没有对应条目，Unmask 自然为 no-op，整条链路退化为现状行为，不会出错。
- **Unmask 语义**：解析 JSON，把与已知占位符**整串相等**的 string value 替换回原文。占位符作为子串嵌在更长字符串里的情况不处理（文档注明：脚本必须把占位符作为完整 string value 保留/搬运）。与 `picotera://data-url/` 前缀相似但不在 map 中的字符串原样放行（可能是客户端数据，不属于输入校验范畴）。
- **非 JSON body 的 fail fast**：rewriteRequest 之后 body 不是合法 JSON 且包含 `picotera://data-url/` 前缀 → 该 attempt 以明确错误失败（无法安全确定转义方式，拒绝静默损坏）；不含占位符则原样透传。

### 快速路径

- `Mask`：`len(body) < 阈值` 或不含 `data:` 子串 → 直接返回输入切片，零解析。
- `Unmask`：masker 无条目，或 body 不含 `picotera://data-url/` 子串 → 直接返回输入切片。

## JS 边界接入（pkg/jsx + pkg/server）

### ctx.request.body（急切，覆盖 sortProviders / rewriteModel / beforeRequest）

`serializeClientRequest` 增加 masker 参数，在现有 `jsonBodyOrNil` 判定通过后对 body 做 `Mask`。两处调用（rewriteModel 前后各一次，`gateway_flow.go:272/316`）共享 flow 上的 masker，ID 稳定。该 body 只读，永不还原。脱敏同时缩小了急切嵌入 QuickJS 的字符串，直接降低 JS 内存压力。

### pending.body（lazy，rewriteRequest）

沿用并强化 lazy body 机制：`RunRewriteRequest` 签名改为接收 body provider：

```go
RunRewriteRequest(initial PendingRequestShape, body func() string) (PendingRequestShape, error)
```

- session 不再持有 `rrBody` 字符串，而是持有 provider；`__picotera_rr_body` host function 首次被调时才执行 provider（结果缓存，保证多次读取一致）。provider 闭包内做 `masker.Mask(reqBody)` —— **JS 不读 body 就连脱敏本身都不发生，零开销**。
- JS 侧 `bodyState == "unchanged"` 时返回的 `PendingRequestShape.Body` 改为 nil（原先回填 `bodyToken(rrBody)`）：调用方本就以 `reqBody` 作 fallback，内容等价，且顺带省掉一次无谓的大字符串 marshal；更重要的是 fallback 用的是**未脱敏**的原始字节，无需 Unmask。
- `bodyState == "set"`（JS 读过或改过 body）时，Go 侧在 `buildRequestFromPending` 之后、`PrepareAttempt` 之前执行 `masker.Unmask`。该位置先于 web-search 改写与 llmbridge 跨格式转换，二者看到的都是还原后的真实 data-url，跨格式图片转换不受影响。

### 模拟器（handle_simulate.go）

模拟流程构造 `jsx.RequestShape` 时同样接入 masker（每次模拟一个新实例），保证脚本在模拟器中看到与生产一致的脱敏形态。模拟器不发上游请求，无需 Unmask。

### 不受影响的部分

- 持久化（request 行、artifacts、`preRewriteBody`、project extractor、userMessagePreview）全部基于原始 `f.body`，存储内容不变。
- 路径网关与统一网关共用 `buildRewrittenUpstreamRequest`，一处改造两边生效。
- 1:1 identity 透传：JS 未读 body 时上游收到的字节与现状完全一致（fallback 原始字节），token/TTFT 提取不受影响。

## 配置

`pkg/configx` 新增 `JSDataURLMaskMinBytes`（`mapstructure:"js_data_url_mask_min_bytes"`，env `PICOTERA_JS_DATA_URL_MASK_MIN_BYTES`），默认 30720；0 表示关闭脱敏（Masker 全程直通）；负值在配置解析时报错。

## 无 API/Schema 变更

不新增/修改任何 Huma operation、contract 类型或数据库 schema，无需重新生成 `openapi.yaml` 与 dashboard 类型。占位符 URI 格式是面向脚本作者的对外契约，规范见 `api.md`。
