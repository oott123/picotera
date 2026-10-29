# API: 脚本可见的 body Proxy 契约

不涉及任何 HTTP API / OpenAPI 变更。本文档是面向脚本作者的行为契约,以及 jsx 内部 host 函数协议。

## 脚本可见行为

`ctx.request.body` 与 rewriteRequest 的 `pending.body` 是由网关托管的 Proxy 对象,按需从 Go 侧读写,不再是普通 JS 对象:

### 读

- 属性读取、`in`、`Object.keys`、`for...in`、`for...of`、解构、展开(`{...body}`)均正常工作;嵌套 object/array 返回子 Proxy,同一节点多次读取返回同一 Proxy(`body.a === body.a`)。
- `Array.isArray` 对数组节点返回 true;`map`/`filter`/`slice`/`find` 等数组方法可用,返回**普通数组**(元素为子 Proxy 或标量)。
- 大字符串(如 data-url)只在读取该字段时才传输;**不再有 `picotera://data-url/` 脱敏占位符**,读到的始终是原文。
- `JSON.stringify(body)` 全量物化为真实 JSON 文本(逐字段从 Go 侧拉取,大 body 代价高,按需使用);`console.log(body)` 同理。

### 写

写入直接落到 Go 侧,后续 hook 与最终上游请求自动可见,无需返回任何东西:

- `body.model = 'x'`、`body.messages.push({...})`、`body.messages.splice(i, 1)`、`delete body.metadata` 均正常工作。
- 写入普通对象/数组时其内容被拷贝进托管树;其中嵌套的 Proxy 子树按**深拷贝**并入。
- **把一个 Proxy 赋给另一位置**(`body.a = body.b`):**深拷贝**并入,不报错也不产生别名。原生 `splice`/`shift`/`unshift` 在对象/数组元素的数组上正常工作正是依赖这一点。
- `JSON.parse(JSON.stringify(x))` 是显式深拷贝手段:得到可自由修改的普通 JS 对象,写回时整体拷贝。

### 报错(均抛 JS 异常,fail fast)

- 写入 `undefined`、函数等不可 JSON 序列化的值。
- 数组:索引越界写(合法写入范围 `[0, length]`,`length` 位置即追加)、`delete arr[i]`(仅允许删除**最后一个**元素,供 `pop`/`splice` 等原生方法从尾部删除使用;删除其他索引报错)、把 `length` 改大。收缩 `length` 合法(截断)。
- **失效 Proxy**:`pending.body` 的 Proxy 只在当次 attempt 的 rewriteRequest 内有效;`ctx.request.body` 的 Proxy 在 rewriteModel 改写了 body 后整体更换。把旧 Proxy 存到全局变量后再访问 → 报错。

### rewriteRequest 返回值

- 原地修改 `pending.body` 后直接返回 `pending`(或不返回)即可。
- 返回全新对象也被跟踪:`return {...pending, body: pending.body}`、`return {...pending, body: ctx.request.body.foo}` 等,嵌在返回值里的 Proxy 由网关自动还原为其托管内容。
- `pending.body = '<原始字符串>'` 仍受支持,该字符串原样作为上游 body 字节。
- 脚本不读不写 body 时,上游收到的字节与进站字节完全一致。

## 内部协议(host 函数,非脚本 API)

脚本不应直接调用 `__picotera_obj_*`;以下仅为实现契约。

### descriptor(JSON 字符串)

| 形态 | 含义 |
|---|---|
| `{"t":"j","v":<JSON 标量>}` | string / number / bool / null,`v` 为原始标量 |
| `{"t":"o","id":<n>}` | object 节点,`id` 为 session 内唯一整数 |
| `{"t":"a","id":<n>,"len":<n>}` | array 节点 |
| `{"t":"u"}` | 不存在 |

### 函数表

| 函数 | 签名 | 错误条件 |
|---|---|---|
| `__picotera_obj_root(slot)` | `(string) → (descriptor, error)` | slot 非 `"request"`/`"pending"`;字节 parse 失败 |
| `__picotera_obj_get(id, key)` | `(int, string) → (descriptor, error)` | id 失效;array 键非索引或越界 |
| `__picotera_obj_set(id, key, valueJSON)` | `(int, string, string) → error` | id 失效;valueJSON 非法;marker 非法或指向失效 id;array 索引越界(> len) |
| `__picotera_obj_del(id, key)` | `(int, string) → error` | id 失效;array 上删除非最后一个元素(仅允许删尾,供 pop/splice 使用) |
| `__picotera_obj_keys(id)` | `(int) → (string, error)` | id 失效;返回 `{"t":"o","keys":[...]}` 或 `{"t":"a","len":n}` |
| `__picotera_obj_has(id, key)` | `(int, string) → (int, error)` | id 失效(返回 1/0,因 QuickJS 绑定不支持 Go bool 跨界) |
| `__picotera_obj_setlen(id, len)` | `(int, int) → error` | id 失效;节点非 array;len 大于当前长度 |

### marker

`{"__picotera_object": <id>}`,恰好一个成员、值为数字。出现在 `__picotera_obj_set` 的 valueJSON 中 → 深拷贝替换;出现在 rewriteRequest 输出(`bodyState:"json"` 的 `__picotera_rr_out`)中 → 直接引用替换。其他成员并存、id 非数字、id 不在册,一律报错。

## 移除项

- 环境变量 `PICOTERA_JS_DATA_URL_MASK_MIN_BYTES` 删除。
- 脚本可见的 `picotera://data-url/<id>?...` 占位符不再出现(2026-06-12-jsonast-data-url-masking 中定义的 JS 边界脱敏契约废止)。
