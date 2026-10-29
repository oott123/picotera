# 执行计划

## 1. Go 侧：`pkg/jsx/objects.go`

新增两个 `*objectRegistry` 方法。

### `arrSplice(id, start, deleteCount int, itemsJSON string) (string, error)`

1. `entries[id]` 查表，缺失返回 `errStaleProxy`；`node.Kind` 非 `KindArray` 返回错误。
2. 防御性校验：`start ∈ [0, len]`，`deleteCount ∈ [0, len-start]`，否则返回越界错误（沿用
   `setlen` 风格的错误文案）。
3. 解析 `itemsJSON`（必为 JSON 数组）；对每个元素 `resolveMarkers(clone=true)`。
4. 取 `removed := elems[start:start+deleteCount]`，逐个 `describe(node, tree)` 编码为描述符，
   拼成 `removed` JSON 数组。
5. 用全新切片重排：`newElems = append(append(elems[:start:start], items...), elems[start+deleteCount:]...)`
   —— 注意三索引切片避免覆盖尾部；赋回 `node.Elems`。
6. 置 `tree.dirty = true`。
7. 返回 `{"removed":[...],"len":<len(newElems)>}`。

### `arrReverse(id int) error`

1. 查表、类型校验同上。
2. 原地双指针交换 `node.Elems`。
3. 置 `tree.dirty = true`，返回 `nil`。

## 2. Go 侧：`pkg/jsx/helpers.go`

在 `registerObjects` 内注册：

```go
_ = vm.RegisterFunc("__picotera_arr_splice", func(id, start, deleteCount int, itemsJSON string) (string, error) {
    return reg.arrSplice(id, start, deleteCount, itemsJSON)
}, false)
_ = vm.RegisterFunc("__picotera_arr_reverse", func(id int) error {
    return reg.arrReverse(id)
}, false)
```

## 3. JS 侧：`pkg/jsx/sdk.js`

在 body Proxy 机制段落内、`makeProxy` 之前定义 `ProxiedArrayProto`：

```js
var ProxiedArrayProto = Object.create(Array.prototype)

function arrId(self) {
  var id = idByProxy.get(self)
  if (id === undefined) throw new Error('picotera: array op on a non-managed value')
  return id
}

function normalizeStart(start, len) {
  var s = Math.trunc(Number(start)) || 0
  if (s < 0) { s = len + s; if (s < 0) s = 0 }
  else if (s > len) s = len
  return s
}

ProxiedArrayProto.splice = function (start, deleteCount) {
  var id = arrId(this)
  var len = hostLen(id)
  var s = normalizeStart(start, len)
  var dc
  if (arguments.length < 2) dc = len - s
  else {
    dc = Math.trunc(Number(deleteCount)) || 0
    if (dc < 0) dc = 0
    if (dc > len - s) dc = len - s
  }
  var items = Array.prototype.slice.call(arguments, 2)
  var r = globalThis.__picotera_arr_splice(id, s, dc, JSON.stringify(items, markerReplacer))
  if (r[1]) throw new Error(r[1])
  var out = JSON.parse(r[0])
  return out.removed.map(descToValue)
}

ProxiedArrayProto.push = function () {
  var id = arrId(this), len = hostLen(id)
  var items = Array.prototype.slice.call(arguments)
  var r = globalThis.__picotera_arr_splice(id, len, 0, JSON.stringify(items, markerReplacer))
  if (r[1]) throw new Error(r[1])
  return JSON.parse(r[0]).len
}

ProxiedArrayProto.unshift = function () {
  var id = arrId(this)
  var items = Array.prototype.slice.call(arguments)
  var r = globalThis.__picotera_arr_splice(id, 0, 0, JSON.stringify(items, markerReplacer))
  if (r[1]) throw new Error(r[1])
  return JSON.parse(r[0]).len
}

ProxiedArrayProto.pop = function () {
  var id = arrId(this), len = hostLen(id)
  if (len === 0) return undefined
  var r = globalThis.__picotera_arr_splice(id, len - 1, 1, '[]')
  if (r[1]) throw new Error(r[1])
  return JSON.parse(r[0]).removed.map(descToValue)[0]
}

ProxiedArrayProto.shift = function () {
  var id = arrId(this), len = hostLen(id)
  if (len === 0) return undefined
  var r = globalThis.__picotera_arr_splice(id, 0, 1, '[]')
  if (r[1]) throw new Error(r[1])
  return JSON.parse(r[0]).removed.map(descToValue)[0]
}

ProxiedArrayProto.reverse = function () {
  var e = globalThis.__picotera_arr_reverse(arrId(this))
  if (e) throw new Error(e)
  return this
}
```

在 `makeProxy` 中，数组分支创建 target 后设置原型：

```js
var target = kind === 'a' ? [] : {}
if (kind === 'a') Object.setPrototypeOf(target, ProxiedArrayProto)
```

## 4. 测试：`pkg/jsx/proxy_test.go`

- 现有 `TestProxy_ArraySplice`、`TestProxy_WriteObjectAndArray`（含 `push`）应继续通过，确认
  改写后行为不变。
- 新增用例：
  - `shift` / `unshift`：验证元素顺序正确、返回值（新长度 / 被移除元素）正确。
  - `splice` 同时删除并插入（`splice(1, 1, {x:9})`），验证返回的被删元素与插入结果。
  - `pop`：验证返回末元素且数组缩短；空数组 `pop` 返回 `undefined`。
  - `reverse`：验证顺序反转且返回数组自身。
  - 重定位不克隆既有元素：`unshift` 一个新对象后修改原有元素，确认未发生别名问题（与既有
    deep-copy-on-set 测试同风格）。

## 5. Go 侧单测：`pkg/jsx/objects_test.go`

新增 `arrSplice` / `arrReverse` 的直接单测：边界钳制、删除+插入、`removed` 描述符与 `len`
返回、`reverse` 顺序、非数组与 stale id 的错误路径。

## 6. 文档：`CLAUDE.md`

更新 "Body Proxies" 段落：说明数组 Proxy 原型链指向 `ProxiedArray`，其
`splice`/`push`/`pop`/`shift`/`unshift`/`reverse` 经 `__picotera_arr_splice` /
`__picotera_arr_reverse` 在 Go 侧切片上重排，重定位既有元素零克隆；`sort`（带比较器）仍走原生
逐项赋值路径。

## 7. 验证

```bash
go test ./pkg/jsx/...
```
