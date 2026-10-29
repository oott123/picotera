# Plan: JSON AST 工具库 + 大 data-url 脱敏

设计见 `design.md`，包接口与占位符契约见 `api.md`。

## Step 1 — 引入 jsontext 依赖

- `go get github.com/go-json-experiment/json@latest`，确认 `go.mod` / `go.sum` 更新。

## Step 2 — `pkg/jsonast`：AST 库

新建 `pkg/jsonast/`：

- `node.go`：`Kind`、`Node`、`Member` 定义；`String()` / `SetString()`；非导出字段 `str`（解码值）、`raw`（原始字节切片）。
- `parse.go`：`Parse(data []byte) (*Node, error)`。用 `jsontext.NewDecoder` 驱动：
  - 容器用 `ReadToken` 处理 `{` `}` `[` `]` 与 object key；
  - 标量（string/number/bool/null）用 `ReadValue` 取原始字节存入 `raw`，string 同时解码出 `str`（`json.Unmarshal(raw, &s)`）；
  - object key 直接解码为 `Member.Key`（不留 raw）；
  - 结束后校验输入已耗尽（尾部多余内容报错）。
- `encode.go`：`Encode(n *Node) ([]byte, error)`。用 `jsontext.NewEncoder`（compact）：未修改标量 `WriteValue(raw)` 原样写回；被修改的节点与所有 key 按解码值 `WriteToken` 重新编码。
- `walk.go`：`Walk` / `WalkStrings`（前序、只访问 value 节点、fn 返回 error 中止）。
- `jsonast_test.go`：
  - roundtrip：key 顺序保留；数字原文保留（`1e10`、`0.10`、超 int64 大整数、负零）；字符串转义保留（`A`、`😀` 代理对、`\/`）；空 object/array、深嵌套、顶层标量；
  - 严格性：尾部垃圾、截断、非法转义、裸值拼接均报错；
  - `WalkStrings` 不访问 key；`SetString` 后 Encode 输出新值；改 `Member.Key` 后 Encode 生效；
  - Walk 顺序为文档顺序。

## Step 3 — `pkg/datamask`：Masker

新建 `pkg/datamask/`：

- `masker.go`：`New(minBytes int)`、`Mask`、`Unmask`、`Active`，按 `api.md` 签名实现：
  - Mask 快速路径（长度不足 / 无 `data:` 子串 → 返回输入切片）；
  - 识别规则：string value、字节长度 ≥ minBytes、`data:` 前缀、前 256 字节内有 `,`；
  - 占位符生成（crypto/rand 16 hex；mediaType / encoding / length 参数按 `api.md`）；按原始值去重（`map[string]string` 双向）；
  - Unmask：快速路径（无条目 / 无 `picotera://data-url/` 子串）；合法 JSON → `WalkStrings` 整串相等替换；非法 JSON 且含占位符前缀 → 明确 error。
- `masker_test.go`：
  - 命中/不命中阈值边界；key 上的 data-url 不脱敏；非 data-url 长字符串不脱敏；
  - 无命中时返回原切片（`&in[0] == &out[0]` 级断言或 bytes.Equal + 文档化）；
  - 同一 data-url 两次 Mask 同一占位符；不同 data-url 不同 ID；
  - mediaType 含特殊字符的 URL 编码、无 mediatype 省略参数、非 base64 省略 encoding；
  - Unmask：整串命中还原；子串包含不还原；未知 `picotera://data-url/...` 原样放行；JS 删除占位符后其余正常还原；非 JSON 含占位符报错、不含占位符直通；
  - `New(0)` 全直通。

## Step 4 — `pkg/configx`：阈值配置

- 在 config 结构体（`JSMaxDelay` 旁）新增 `JSDataURLMaskMinBytes int "mapstructure:\"js_data_url_mask_min_bytes\""`。
- 按现有默认值模式注册默认 `30720`；负值在解析/校验处报错。
- env：`PICOTERA_JS_DATA_URL_MASK_MIN_BYTES`（现有前缀机制自动覆盖，确认即可）。

## Step 5 — `pkg/jsx`：lazy body provider

- `iface.go`：`RunRewriteRequest(initial PendingRequestShape, body func() string) (PendingRequestShape, error)`。
- `session.go`：
  - `qjsSession.rrBody string` → `rrBodyFn func() string` + `rrBodyOnce` 缓存（首次调用执行并缓存，保证 JS 多次读取一致）；
  - `RunRewriteRequest`：`hasBody := body != nil`；`initial.Body` 不再作为输入载体，调用方必须传 nil，非 nil 时直接返回错误（fail fast）；
  - `bodyState == "unchanged"` 分支改为 `out.Body = nil`（调用方 fallback 到原始 reqBody）；删除该分支的 `bodyToken` 调用；
  - `bodyState == "set"` 不变（读 `__picotera_rr_out`）。
- `helpers.go`：`registerRewriteBody` 改为调用 `s.rrBodyFn`（含缓存）；更新注释。
- 测试更新：
  - `engine_test.go`（`TestSession_RewriteRequest_Passthrough`、`TestSession_RewriteRequest_BodyJSONRoundtrip`）改用 provider 入参；passthrough 断言 Body 为 nil；
  - `large_body_test.go`（`TestSession_RewriteRequest_LargeBody`、`TestSession_RewriteRequest_BodyAccess`）：未读 body 时 provider 不被调用（计数器断言）、读 body 时只调用一次。

## Step 6 — `pkg/server`：网关接入

- `gateway_flow.go`：
  - `gatewayFlow` 新增 `masker *datamask.Masker`；在 `newGatewayFlow` 或 `resolveAndRewriteModel` 入口处以 `f.h.…cfg.JSDataURLMaskMinBytes` 创建（确认 server 持有 config 的现有路径）；
  - 两处 `serializeClientRequest(f.r, f.body, …)`（行 272、316）改为传入 masker。
- `gateway_helpers.go`：
  - `serializeClientRequest` 增加 `masker *datamask.Masker` 参数：`jsonBodyOrNil` 通过后调用 `masker.Mask`；Mask error → warn 日志 + 用原 body；
  - `serializePendingRequest` 不再填 `Body`（只留 URL/Method/Headers）；新增小 helper `pendingBodyProvider(masker, header, reqBody) func() string`：`jsonBodyOrNil` 判定通过时返回闭包（内部 Mask + 失败降级日志），否则返回 nil。
- `gateway_flow_attempts.go` `buildRewrittenUpstreamRequest`：
  - `f.session.RunRewriteRequest(pending, pendingBodyProvider(f.masker, req.Header, reqBody))`；
  - `buildRequestFromPending` 之后：`if newPending.Body != nil && f.masker.Active()` → `f.masker.Unmask(reqBody)`；error → 以 `gatewayHookError` 失败该 attempt；成功且内容变化 → 替换 `reqBody` 并 `resetRequestBody(req, reqBody)` + 更新 `ContentLength`（确认 `buildRequestFromPending` 内已设，必要时重建）。
- `handle_simulate.go`：构造 `jsx.RequestShape`（行 142 附近）处接入一个新建 masker（同阈值配置），并检查模拟流程是否还有其他 body 进 JS 的路径（若模拟器执行 rewriteRequest 同样传 provider）。
- 全局 `grep RunRewriteRequest / serializeClientRequest / serializePendingRequest`，更新所有调用点（含测试 mock，若 `pkg/server` 测试实现了 `jsx.Session` 接口需同步签名）。

## Step 7 — 验证

- `go build ./...`；`go test ./pkg/jsonast/ ./pkg/datamask/ ./pkg/jsx/ ./pkg/server/ ./pkg/llmbridge/`。
- 手工冒烟（可选，需本地 infra）：`docker compose up -d` + `mise run server`，发一个含 >30 KiB data-url 图片的 `/api/picotera/v1/messages` 请求，配一个 `console.log(ctx.request.body)` 脚本，确认：日志中是占位符；上游 attempt 的 artifact body 是原文；无脚本读 body 时行为与现状一致。
- 无 contract / openapi / dashboard 变更，无需 `mise run openapi`。

## 文件清单

| 操作 | 路径 |
|---|---|
| 新增 | `pkg/jsonast/{node,parse,encode,walk}.go` + `jsonast_test.go` |
| 新增 | `pkg/datamask/masker.go` + `masker_test.go` |
| 修改 | `go.mod` / `go.sum`（新增 go-json-experiment/json） |
| 修改 | `pkg/configx/`（新增 `JSDataURLMaskMinBytes`） |
| 修改 | `pkg/jsx/iface.go`、`session.go`、`helpers.go`、`engine_test.go`、`large_body_test.go` |
| 修改 | `pkg/server/gateway_flow.go`、`gateway_helpers.go`、`gateway_flow_attempts.go`、`handle_simulate.go`（及受签名影响的测试） |
