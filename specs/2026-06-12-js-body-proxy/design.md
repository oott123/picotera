# Design: JS 大 body 的 Proxy 化处理

## 总览

把 JS hook 可见的两个大 JSON body —— `ctx.request.body` 与 rewriteRequest 的 `pending.body` —— 从「整串 JSON 嵌入 / 回传」改为 **Go 侧对象树 + JS Proxy 按需读写**:

- Go 侧用 `pkg/jsonast` 的 `*Node` 树持有 body,每个被 JS 访问到的 object/array 节点分配一个 session 内唯一的整数 id,登记到 session 的对象注册表。
- JS 侧用 `Proxy` 包装这些 id,get/set/枚举/删除 trap 通过同步 host 函数转发到 Go 侧节点。标量只在被读到时才跨边界,大字符串(如 data-url)脚本不读就永不进入 QuickJS。
- 写入直接落到 Go 侧树上;rewriteRequest 结束时,若 body 仍由 Proxy 跟踪,Go 直接 `jsonast.Encode` 自己的树,**大 body 不再以字符串形式回传**。
- hook 返回全新对象(如 `{...pending, body: someProxy}`)时,胶水层序列化用 `JSON.stringify` 的 replacer 把 Proxy 替换为 marker `{"__picotera_object": <id>}`;Go 解析后用注册表中的内部节点替换 marker,跟踪不丢失。
- JS 边界的 data-url 脱敏(datamask)接入整体移除;`pkg/datamask`、`PICOTERA_JS_DATA_URL_MASK_MIN_BYTES` 之外的 jsonast 基建复用。

## Go 侧:对象注册表(pkg/jsx/objects.go)

每个 `qjsSession` 持有一个 `objectRegistry`:

- `nextID int` 单调递增,session 内永不复用。
- `entries map[int]*objectEntry`,`objectEntry{node *jsonast.Node, tree *treeState}`。
- `treeState{root *Node, dirty bool}`,树级 dirty 标记:任何成功的 set / delete / length 截断都置 `dirty = true`。
- 两个具名树槽位:
  - **request 树**:`SetClientBody(body []byte)` 设置原始字节,首次 JS 访问时才 `jsonast.Parse`(lazy)。再次调用 `SetClientBody`(模型改写后 body 变化、模拟器二次设置)时,旧树的所有 id 从注册表移除(失效),换新槽位。
  - **pending 树**:每次 `RunRewriteRequest` 开始时,上一次 pending 树的所有 id 失效,本次 body 字节存入槽位,同样 lazy parse。
- 失效 id 的任何 host 调用返回明确错误(`stale proxy`),JS 侧抛异常 —— fail fast,不静默读旧数据。

### jsonast 增量

- `Clone(n *Node) *Node`:深拷贝节点结构;string/number 的 `str`/`raw` 为不可变数据,直接共享底层字节,拷贝成本与节点数线性、与字符串大小无关。
- object member 的查找/赋值/删除、array 元素操作等以 registry 内部 helper 实现,不扩 jsonast API。

### Marker 协议

marker 是恰好只有一个成员、键为 `__picotera_object`、值为数字 id 的 JSON object。多余成员或非数字 id → 报错(fail fast)。两条解析路径:

- **写回路径**(`__picotera_obj_set` 的 value):marker 替换为对应节点的 **`Clone` 深拷贝**。由于一切 Proxy 赋值(含 `body.a = body.b`)都经此深拷贝路径,系统中不存在节点别名,树恒为无环,Encode 无需环检测。
- **rewriteRequest 输出路径**:marker 替换为对应节点的**直接引用**(只读消费,随后立刻 Encode,无别名风险)。marker 可指向任意在册节点 —— 包括 request 树的节点(脚本可把 `ctx.request.body` 的子树直接用作上游 body)。

## Host 函数协议(pkg/jsx/helpers.go 注册)

全部为同步函数,沿用 kv 的 `(string, error)` 风格;descriptor 为 JSON 字符串:

| 函数 | 签名 | 说明 |
|---|---|---|
| `__picotera_obj_root` | `(slot string) → (string, error)` | `"request"` / `"pending"`,lazy parse 并登记根节点,返回 descriptor;槽位为空返回 `{"t":"u"}` |
| `__picotera_obj_get` | `(id int, key string) → (string, error)` | 读成员/元素,返回 descriptor |
| `__picotera_obj_set` | `(id int, key string, valueJSON string) → error` | 写成员/元素;valueJSON 内的 marker 深拷贝替换 |
| `__picotera_obj_del` | `(id int, key string) → error` | 删除 object 成员;array 上仅允许删最后一个元素(供 pop/splice 从尾部删除),其余索引报错 |
| `__picotera_obj_keys` | `(id int) → (string, error)` | object 返回 `{"t":"o","keys":[...]}`;array 返回 `{"t":"a","len":n}` |
| `__picotera_obj_has` | `(id int, key string) → (int, error)` | 成员存在性,返回 1/0(QuickJS 绑定不支持 Go bool 跨界) |
| `__picotera_obj_setlen` | `(id int, len int) → error` | array 截断;大于当前长度报错 |

descriptor 取值:

- 标量:`{"t":"j","v":<原始 JSON 标量>}` —— JS 侧 `JSON.parse` 后直接得到 string/number/bool/null;
- object:`{"t":"o","id":<id>}`;array:`{"t":"a","id":<id>,"len":<n>}` —— JS 侧据此创建(或从缓存取出)子 Proxy;
- 不存在:`{"t":"u"}`。

array 的 `key` 必须是十进制整数索引,get 范围 `[0, len)`、set 范围 `[0, len]`(`len` 即追加);越界、非索引键一律报错。

## JS 侧:Proxy 工厂(pkg/jsx/sdk.js)

`makeProxy(id, kind, len)`,session 级 `Map(id → proxy)` 缓存保证 `body.a === body.a`;`WeakSet` 登记所有本系统 Proxy 用于识别。

- **target**:object 用 `{}`,array 用 `[]`(`Array.isArray` 为 true;`map`/`push`/`splice` 等原型方法经普通原型链取得,内部 `[[Get]]/[[Set]]` 全部命中 trap,天然走 Go 侧)。
- **get**:array 的 `'length'` → host len;整数索引 / object 键 → `__picotera_obj_get`;symbol 与原型方法 → `Reflect.get`。不特判 `toJSON`(数据里真有 `toJSON` 键也按数据返回;非函数值会被 `JSON.stringify` 忽略,语义正确)。
- **set**:值为 `undefined` 或不可 JSON 序列化(函数等)→ 抛错;否则 `JSON.stringify(value, markerReplacer)` 后 `__picotera_obj_set`(任何 Proxy —— 无论嵌套还是值本身 —— 经 replacer 变 marker,Go 侧深拷贝替换,既快又无别名)。值本身就是一个 Proxy(`body.a = body.b`)同样走此路径深拷贝,这也是原生 `splice`/`shift` 在对象数组上能工作的前提。
- **deleteProperty / has / ownKeys / getOwnPropertyDescriptor**:转发对应 host 函数;gOPD 返回 `{enumerable:true, configurable:true, writable:true, value:...}`;array 的 `'length'` descriptor 通过先 `Reflect.defineProperty(target,'length',{value:hostLen})` 同步 target 再交给 Reflect,满足 Proxy invariant。
- **`markerReplacer`**:`(k, v) => isProxy(v) ? {__picotera_object: idOf(v)} : v`。**只在胶水层与 set trap 使用**;脚本自己写的普通 `JSON.stringify(proxy)` 不带 replacer,经 trap 递归**全量物化**为真实 JSON 文本 —— 这正是显式深拷贝逃生通道 `JSON.parse(JSON.stringify(x))` 的实现基础,`console.log` 调试输出同理可读。

## Session 接口与类型变化(pkg/jsx)

- `RequestShape` 删除 `Body` 字段;client body 改由新接口方法 `SetClientBody(body []byte) error` 传入(nil 表示无 JS 可见 body)。`PatchContext` 在 `patch.Request != nil` 且 request 槽位已设置时,补装 `ctx.request.body` 的 lazy getter(首次读时 `__picotera_obj_root("request")` 建 Proxy);`SetClientBody` 在 `ctx.request` 已存在时同样立即装 getter —— 两者顺序无关。
- `RunRewriteRequest(initial PendingRequestShape, body []byte)`:第二参数从 `func() string` 改为原始字节(nil = body 不可见)。content-type / `json.Valid` 的可见性判定仍在调用方(行为与现状一致:invalid JSON 时 body 不存在)。
- `PendingRequestShape.Body` 改为 `[]byte` 并标 `json:"-"`:输入侧恒为 nil(胶水层自行 defineProperty);输出侧直接携带**最终上游 body 字节**,不再是 JSON string token。

### rewriteRequest 胶水协议

`pending.body` 经 `defineProperty` 安装 getter/setter:getter 首次访问时 `__picotera_obj_root("pending")` 创建根 Proxy 并缓存;setter 记录脚本设置的任意新值。waterfall 结束后:

| 终态 | meta.bodyState | 回传 | Go 侧处理 |
|---|---|---|---|
| 无 body / 脚本置 null | `none` | — | `Body=nil`,fallback 原始字节 |
| 从未读写 | `unchanged` | — | `Body=nil`,fallback 原始字节 |
| 终值为 string | `raw` | `__picotera_rr_out` = 该字符串 | `Body=[]byte(out)` |
| 其余(Proxy / 对象 / 数组等) | `json` | `__picotera_rr_out` = `JSON.stringify(v, markerReplacer)` | 见下 |

`json` 态 Go 侧:`jsonast.Parse(out)` → 替换 marker(直接引用)→ 若结果恰为 pending 根节点且树 **未 dirty** → `Body=nil`(fallback 原始字节,byte-identical 透传);否则 `jsonast.Encode` → `Body`。未修改的标量按 jsonast 保证原字节写回。

## Server 侧接入(pkg/server)

- `serializeClientRequest` 去掉 masker 与 body 参数(只产出 meta);`gateway_flow.resolveAndRewriteModel` 在两次 `PatchContext` 后各调一次 `SetClientBody(f.body)`(模型改写导致 body 变化时第二次换树,旧 Proxy 失效)。
- `buildRewrittenUpstreamRequest`:`pendingBodyProvider` 删除,直接 `RunRewriteRequest(pending, jsonBodyOrNil(req.Header, reqBody))`;返回的 `newPending.Body != nil` 时直接作为上游字节,Unmask 调用块删除。
- `buildRequestFromPending`:`p.Body != nil` 时 `outBody = p.Body`,不再做 string token 解码。
- `handle_simulate.go`:masker 删除;`SetClientBody(bodyBytes)`,模型改写后再次 `SetClientBody(更新后字节)`。
- **datamask 接入移除**:`gatewayFlow.masker` 字段、`maskJSONBody`、`pendingBodyProvider` 及全部调用删除;`configx.JSDataURLMaskMinBytes`(含校验与 env)删除。`pkg/datamask`、`pkg/jsonast` 包保留。

## 不变的部分

- 持久化(request 行、artifacts、`preRewriteBody`、project extractor、userMessagePreview)仍基于原始 `f.body`。
- 路径网关与统一网关共用同一改造点;llmbridge / web-search 改写发生在 hook 之前,顺序不变。
- hook 未读写 body 时上游字节与现状完全一致(fallback 原始字节),token/TTFT 提取不受影响。
- 其余 hook(sortProviders / rewriteModel / beforeRequest / beforeTransform / rewriteProviderModels)的值传递方式不变。
- 无 Huma operation / contract / 数据库 schema 变更,`openapi.yaml` 与 dashboard 类型无需重生成。

## 行为变化(面向脚本作者,契约见 api.md)

- body 不再出现 `picotera://data-url/` 占位符,读到的是原文。
- `body.a = body.b` 深拷贝并入(不报错、不别名);跨 attempt / 跨槽位保存的 Proxy 失效后访问抛错。
- 写入 `undefined`、函数,数组越界写、删除非最后一个数组元素、收缩以外的 `length` 赋值,均抛错。
