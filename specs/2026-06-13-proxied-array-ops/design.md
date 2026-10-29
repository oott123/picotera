# 设计

## 目标

消除数组 Proxy 上变异操作（`splice` / `shift` / `unshift` / `push` / `pop` / `reverse`）对
被重定位元素的深克隆。元素在同一棵树内重排只需移动 `*Node` 指针，无需克隆。

## 核心思路

1. **改写数组 Proxy 的原型链。** `makeProxy(id, 'a')` 创建的 target 仍是 `[]`（保证
   `Array.isArray(proxy)` 为真），但把 target 的原型设为自定义的 `ProxiedArrayProto`，其原型
   再指向 `Array.prototype`：

   ```
   target ([])  →  ProxiedArrayProto  →  Array.prototype  →  Object.prototype
   ```

   `ProxiedArrayProto` 只覆盖变异方法（`splice`/`push`/`pop`/`shift`/`unshift`/`reverse`）；
   其余所有方法落到 `Array.prototype`。get trap 对非索引、非 `length` 属性已经走
   `Reflect.get(target, prop, recv)`，会沿新原型链解析到这些覆盖方法，无需改动 get trap 逻辑。

2. **变异方法转发到 Go 侧切片操作。** 方法内通过闭包中的 `idByProxy` WeakMap 由 `this`
   取得 id，调用宿主函数。Go 侧直接在 `node.Elems` 切片上做重排，被移动的既有元素只是切片中
   指针位置变化，**不克隆**。

3. **仅一个新宿主入口 `__picotera_arr_splice` 承载全部位移语义**，`push`/`pop`/`shift`/
   `unshift` 在 SDK 内用 `splice` 表达；`reverse` 使用独立宿主函数
   `__picotera_arr_reverse`（纯指针交换）。

## 克隆语义

- **被重定位的既有元素**：在 Go 侧切片内移动，零克隆。这是本次优化的核心。
- **新插入的字面量值**（如 `push({role:"b"})`）：作为全新解析出的节点插入，本就不是克隆。
- **插入一个引用既有节点的 Proxy**（如 `arr.unshift(body.x)`，参数序列化为 marker）：经
  `resolveMarkers(clone=true)` 深克隆后插入。这与既有 set trap 的语义一致（同一节点不能在树中
  出现两次），保持不变。

## `__picotera_arr_splice` 协议

JS 端做 JS 忠实的参数归一化（`start` 负数回绕与上限钳制、`deleteCount` 省略即到末尾、钳制到
`[0, len-start]`），再以已归一化的 `(id, start, deleteCount, itemsJSON)` 调用宿主。`itemsJSON`
是插入项数组经 `markerReplacer` 序列化的结果。

宿主返回 `[resultJSON, errOrNull]`，`resultJSON` 形如：

```json
{ "removed": [<descriptor>, ...], "len": <新长度> }
```

- `removed` 为被删除元素的描述符数组（对象/数组元素经 `register` 取得 id 后以 Proxy 描述符
  返回，标量内联）。SDK 用 `descToValue` 还原为 JS 值，作为 `splice` 的返回值。被删元素从树上
  分离，但其 id 在该 slot 重置前仍有效，故返回的 Proxy 可继续读取。
- `len` 为操作后数组的新长度，供 `push`/`unshift` 返回新长度使用。

宿主侧仍做防御性边界校验（`start`、`deleteCount` 必须在合法范围内），与 `arrayIndex`、`setlen`
等既有错误风格一致。

## `__picotera_arr_reverse` 协议

`__picotera_arr_reverse(id)` 原地反转 `node.Elems`（纯指针交换），置 `dirty`，返回
`errOrNull`。SDK 的 `reverse` 返回 `this`（与原生 `Array.prototype.reverse` 一致）。

## 不变量与兼容性

- `Array.isArray(proxy)`、`proxy instanceof Array`、展开、`for...of`、`map`/`filter` 等只读
  方法均依赖 target 是真数组与 `length`/索引 get，原型链改写不影响这些路径。
- 索引赋值（`arr[i] = x`）、`arr.length = n`、`delete arr[last]` 继续走既有 set / setlen /
  del trap，语义与克隆行为不变。
- `sort`（带比较器）保持原生：仍经 set trap 克隆。后续如需可再扩展。

## 涉及文件

- `pkg/jsx/sdk.js`：定义 `ProxiedArrayProto` 及六个变异方法；`makeProxy` 数组分支设置原型。
- `pkg/jsx/objects.go`：新增 `objectRegistry.arrSplice` 与 `arrReverse` 方法。
- `pkg/jsx/helpers.go`：在 `registerObjects` 注册 `__picotera_arr_splice` /
  `__picotera_arr_reverse`。
- `CLAUDE.md`：更新 Body Proxies 段落，说明数组变异方法走宿主切片操作、零克隆重定位。
