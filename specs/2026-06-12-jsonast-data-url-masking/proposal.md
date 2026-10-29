# Proposal: JSON AST 工具库 + 大 data-url 脱敏

## 原始需求

是这样的，因为我会处理各种不定形状的请求 object，然后我又可能要在里面扫描、替换字符串之类的需求，所以我想要一个可以将 JSON 解析为一颗 AST 树，然后还能还原回去。比如我想找到所有包含 "foobar" 的字符串并脱敏，但是这个字符串可能出现在 object key 上，而我只想替换所有的 value，就需要这样的一套东西。不知道有没有现成的，没有的话想自己造一个。

然后我想做一个，将输入里面所有超过 30k 的 data-url 字符串，替换为比如 picotera://id 的格式，再 rewriteRequest，过完 js hook 之后，再替换回来，从而使得 js 不要处理那么长的字符串，这样的功能。

## 澄清与决策（与用户确认）

- **AST 实现**：现有 Go 生态没有完全契合的库（gjson/sjson 是路径式查询、不适合全树遍历替换；fastjson 基本停止维护且序列化行为不可控）。决定引入 `github.com/go-json-experiment/json` 的 `jsontext` 包做 token 流词法层，AST 层自研（新包 `pkg/jsonast`）。
- **占位符格式**：携带元信息，形如 `picotera://data-url/<id>?mediaType=image%2Fpng&encoding=base64&length=2400000`，让脚本不接触原始字节也能按图片类型/大小做路由决策。
- **脱敏范围**：所有 JS 可见 body——`ctx.request.body`（sortProviders / rewriteModel / beforeRequest 等 hook 看到的）与 rewriteRequest 的 `pending.body` 都脱敏；同一 data-url 跨 hook 使用同一个 ID。只有 rewriteRequest 返回的 body 需要还原（其余只读）。
- **阈值**：默认 30 KiB（30720 字节），通过 `PICOTERA_JS_DATA_URL_MASK_MIN_BYTES` 配置，设为 0 关闭该功能。
