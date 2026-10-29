# API: jsonast / datamask / 占位符契约

本特性不涉及 REST API 变更。本文档定义两个 Go 包的公开接口与面向脚本作者的占位符 URI 契约。

## pkg/jsonast

```go
package jsonast

type Kind uint8

const (
	KindNull Kind = iota
	KindBool
	KindNumber
	KindString
	KindObject
	KindArray
)

// Node 是可变 JSON 文档树节点。
type Node struct {
	Kind    Kind
	Bool    bool     // KindBool
	Members []Member // KindObject，按文档顺序
	Elems   []*Node  // KindArray
	// 非导出：str（解码值）、raw（原始字节，未修改标量序列化时原样写回）
}

// Member 是 object 成员。Key 是解码后的键名，可直接改写；
// key 不是 Node，Walk/WalkStrings 不会访问它。
type Member struct {
	Key   string
	Value *Node
}

// Parse 严格解析：输入必须恰好是一个完整 JSON 值，否则报错。
func Parse(data []byte) (*Node, error)

// Encode 序列化为 compact JSON。未修改的 string/number 节点
// 字节级还原原文（转义形式、数字精度不变）。
func Encode(n *Node) ([]byte, error)

// String 返回解码值：KindString 为字符串内容，KindNumber 为数字原文。
// 其余 Kind 返回空串。
func (n *Node) String() string

// SetString 将节点改写为给定字符串值（Kind 变为 KindString，原文作废）。
func (n *Node) SetString(s string)

// Walk 前序遍历所有 value 节点（不含 object key）。fn 返回 error 则中止。
func Walk(root *Node, fn func(n *Node) error) error

// WalkStrings 按文档顺序访问所有 KindString 的 value 节点（不含 key）。
// fn 可就地修改节点。
func WalkStrings(root *Node, fn func(n *Node) error) error
```

需求示例「替换所有 value 中含 foobar 的字符串、不碰 key」：

```go
root, _ := jsonast.Parse(body)
_ = jsonast.WalkStrings(root, func(n *jsonast.Node) error {
	if strings.Contains(n.String(), "foobar") {
		n.SetString("[REDACTED]")
	}
	return nil
})
out, _ := jsonast.Encode(root)
```

## pkg/datamask

```go
package datamask

// Masker 把 JSON body 中超长 data-url string value 替换为占位符并支持还原。
// 实例为单请求生命周期、非并发安全；同一实例内同一原始值得到同一占位符。
type Masker struct{ /* 非导出字段 */ }

// New 创建 Masker。minBytes ≤ 0 时 Mask/Unmask 全程直通。
func New(minBytes int) *Masker

// Mask 扫描 JSON body，把满足条件的 data-url string value 替换为占位符。
// 无命中（或功能关闭）时原样返回输入切片（byte-identical）。
// 解析失败返回 error，调用方应记日志并使用原始 body（安全降级）。
func (m *Masker) Mask(body []byte) ([]byte, error)

// Active 报告是否已产生过任何占位符。
func (m *Masker) Active() bool

// Unmask 把与已知占位符整串相等的 string value 替换回原文。
// body 非合法 JSON 且包含 "picotera://data-url/" 时返回 error（fail fast）；
// 不含占位符时原样返回输入切片。
func (m *Masker) Unmask(body []byte) ([]byte, error)
```

**识别条件**（全部满足才脱敏）：节点是 string value（非 key）；解码后字节长度 ≥ minBytes；以 `data:` 开头；前 256 字节内含 `,`。

## 占位符 URI 契约（面向脚本作者）

```
picotera://data-url/<id>?mediaType=<m>&encoding=base64&length=<n>
```

| 部分 | 说明 |
|---|---|
| `<id>` | 16 个十六进制字符，crypto/rand 生成，仅在当次请求内有效 |
| `mediaType` | data URL 的 mediatype（URL 编码，如 `image%2Fpng`）；原文为空时省略该参数 |
| `encoding` | 原文含 `;base64` 时固定为 `base64`，否则省略 |
| `length` | 恒存在，原始字符串的字节长度 |

脚本侧规则：

- `ctx.request.body` 与 rewriteRequest 的 `pending.body` 中看到的是占位符；同一 data-url 在同一请求的所有 hook 中 ID 一致。
- 脚本可读取占位符的 query 参数做路由决策（如按图片大小选 provider），无需也无法读取原始字节。
- 搬运/保留占位符时必须保持其为**完整的 string value**；拼接进更长字符串后不会被还原。
- 删除占位符即从上游请求中删除对应 data-url；rewriteRequest 返回的 body 中每个完整匹配的占位符 value 都会被还原为原文后发往上游。

## 配置

| Env | 默认 | 说明 |
|---|---|---|
| `PICOTERA_JS_DATA_URL_MASK_MIN_BYTES` | `30720` | 脱敏阈值（字节）；`0` 关闭功能；负值配置报错 |
