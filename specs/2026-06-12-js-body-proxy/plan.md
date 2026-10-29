# Plan: JS 大 body 的 Proxy 化处理

## 步骤 1:jsonast 增加 Clone

- `pkg/jsonast/node.go`(或新文件 `clone.go`):`Clone(n *Node) *Node` 深拷贝节点结构,`str`/`raw` 共享底层数据;`Members`/`Elems` 递归拷贝。
- `pkg/jsonast/jsonast_test.go`:Clone 后修改副本不影响原树;Clone 保留未修改标量的 raw 字节(Encode 输出一致)。

## 步骤 2:jsx 对象注册表与 host 函数

- 新文件 `pkg/jsx/objects.go`:
  - `objectRegistry`:`nextID`、`entries map[int]*objectEntry`、`treeState`(root + dirty)、request/pending 两个槽位(原始字节 + lazy parse 的树 + 已登记 id 列表)。
  - 槽位重设 / RunRewriteRequest 开始时,旧树 id 批量失效。
  - descriptor 编码、key→object member / array index 的读写删、`setlen` 截断、marker 解析与替换(set 路径 Clone,输出路径直接引用)。
- `pkg/jsx/helpers.go`:注册 `__picotera_obj_root/get/set/del/keys/has/setlen` 七个 host 函数(挂在 `qjsSession` 的 registry 上);删除 `registerRewriteBody` 与 `__picotera_rr_body`。
- 新文件 `pkg/jsx/objects_test.go`:registry 单测(不经 VM):marker 非法形态报错、深拷贝语义、dirty 标记、失效语义、array 边界。

## 步骤 3:sdk.js Proxy 工厂

- `pkg/jsx/sdk.js`:
  - `makeProxy(id, kind, len)` + id→proxy `Map` 缓存 + `WeakSet` 识别 + `markerReplacer`,以内部命名(如 `globalThis.__picotera_makeProxy`、`__picotera_markerReplacer`)暴露给胶水代码,不挂到 `picotera.*` 公共面。
  - 按 design.md 实现 get/set/deleteProperty/has/ownKeys/getOwnPropertyDescriptor 六个 trap;set trap 对 Proxy 值 / undefined / 不可序列化值抛错;array 的 `length` invariant 用 `Reflect.defineProperty(target, 'length', ...)` 同步。

## 步骤 4:Session 接口与 rewriteRequest 胶水

- `pkg/jsx/types.go`:`RequestShape` 删 `Body`;`PendingRequestShape.Body` 改 `[]byte` + `json:"-"`,更新注释。
- `pkg/jsx/iface.go`:`Session` 增加 `SetClientBody(body []byte) error`;`RunRewriteRequest(initial PendingRequestShape, body []byte)`。
- `pkg/jsx/session.go`:
  - 删除 `rrBodyFn/rrBodyCached/rrBodyDone/rrBodyValue`。
  - `SetClientBody`:写 request 槽位(旧树失效);`ctx.request` 已为对象时 eval 安装 `body` lazy getter。
  - `PatchContext`:`patch.Request != nil` 且 request 槽位非空时,Object.assign 后补装同一 getter。
  - `RunRewriteRequest`:写 pending 槽位;胶水 expr 按 design.md 的 bodyState 协议(`none|unchanged|raw|json`)重写;`json` 态走 `jsonast.Parse + marker 替换 + 根节点/dirty 判定 + Encode`。

## 步骤 5:server 接入与 datamask 移除

- `pkg/server/gateway_helpers.go`:`serializeClientRequest` 去掉 masker/body;删除 `maskJSONBody`、`pendingBodyProvider`;`buildRequestFromPending` 直接使用 `p.Body` 字节;`jsonBodyOrNil` 保留(可见性判定)。
- `pkg/server/gateway_flow.go`:删 `masker` 字段与构造;两次 `PatchContext` 后各调 `SetClientBody(f.body)`。
- `pkg/server/gateway_flow_attempts.go`:`RunRewriteRequest(pending, jsonBodyOrNil(req.Header, reqBody))`;删除 Unmask 块。
- `pkg/server/handle_simulate.go`:删 masker;初始与模型改写后各调 `SetClientBody(bodyBytes)`。
- `pkg/configx/configx.go`:删 `JSDataURLMaskMinBytes` 字段、默认值与负值校验。
- 确认 `pkg/datamask`、`pkg/jsonast` 包无其他引用残留后保留不动。

## 步骤 6:测试

- `pkg/jsx/engine_test.go` + 新增 `pkg/jsx/proxy_test.go`(经 VM 的端到端 hook 测试):
  - 读:标量/嵌套/枚举/展开/`Array.isArray`/数组方法/Proxy 身份缓存。
  - 写:对象赋值、`push`/`splice`、嵌套 Proxy 深拷贝并入、`delete` 成员。
  - 报错:`body.a = body.b`、写 undefined、数组越界、`delete arr[i]`、length 增大、失效 Proxy(跨 attempt / SetClientBody 换树)。
  - rewriteRequest 终态:未读写 → `Body=nil`;读未写返回 initial → `Body=nil`(clean root marker);原地改 → Encode 输出且未触字段字节不变;返回新对象嵌 marker;`body = '<string>'` → raw;`body = null` → none;`JSON.parse(JSON.stringify(body))` 全量物化深拷贝。
  - `ctx.request.body`:rewriteModel/sortProviders 中可读写;读不到 body(非 JSON)时属性不存在。
- `pkg/jsx/large_body_test.go`:改写为 Proxy 语义(未读 body 不 parse、读单字段不全量传输、内存上限不被大 body 击穿)。
- 删除 jsx/server 中 datamask 相关测试断言(`pkg/datamask/masker_test.go` 保留)。
- `pkg/server` 现有 helper 测试随签名更新。

## 步骤 7:文档与收尾

- 更新 `CLAUDE.md` 的 Scripts 段落:body Proxy 行为、datamask 接入移除。
- `go build ./... && go test ./pkg/jsx/... ./pkg/jsonast/... ./pkg/server/...` 全绿。
- 无 openapi / dashboard 变更。
