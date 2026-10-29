# 代理数组的原生数组操作（无克隆）

## 背景

最近一个提交 `f8d1320 feat: proxy js` 引入了 body Proxy 机制：`ctx.request.body` 与
rewriteRequest 的 `pending.body` 以 `jsonast.Node` 树形式存在于 Go 侧，JS 侧通过整数 id
的 Proxy 转发读写。

当脚本对数组 Proxy 调用 `splice` / `shift` / `unshift` 等原生数组方法时，这些方法在内部
通过逐个索引赋值来移动元素。每次索引赋值都会经过 set trap → `__picotera_obj_set` →
`resolveMarkers(clone=true)`，把被移动的既有元素**深克隆**一份。即移动 N 个元素就产生 N 次
深克隆与 N 次 JSON 序列化往返。

## 需求

当 Proxy 对象是数组时，改写其原型链，新增一个 `ProxiedArray` 原型类，由该类提供一系列数组
操作方法。这些方法实际调用 Go 侧函数直接对 `[]*Node` 切片做指针重排，从而在元素**重定位**时
不再克隆。

覆盖的方法：`splice`、`push`、`pop`、`shift`、`unshift`、`reverse`。

`sort` 因需要 JS 比较器回调，保持原生实现（仍会克隆），不在本次范围内。所有只读方法
（`map`、`filter`、`slice`、`forEach`、`join`、迭代、展开等）继续沿用 `Array.prototype`。
