# 移除请求"状态"字段

## 背景

请求详情页的"状态"一栏（`status`：0=Pending / 1=HeaderReceived / 2=Completed / 3=Failed）与"完成原因"（`finishReason`）+ "状态码"（`statusCode`）高度冗余。经分析，`status` 相对后两者仅有的独立信息——区分 `finishReason=2/5` 时"响应是否已交付"——可由同页已展示的 `statusCode` 提供，因此 `status` 字段是边际冗余的。

## 需求

1. 移除请求详情页"状态"一栏的展示。
2. 将"完成原因"移动到原"状态"在概览网格中的位置。
3. 移除 `status` 相关的后台代码，包括从 API 契约（`RequestView`）和数据库中彻底删除 `status` 列。
4. 删除 `status` 列后，受影响的查询与前端派生逻辑改由 `finish_reason` + `status_code` 推导，且语义与原 `status` 完全等价。

## 语义等价

经代码路径穷举，`status` 的全部取值可由 `(status_code, finish_reason)` 精确还原：

| status | status_code | finish_reason |
|--------|-------------|---------------|
| 2（Completed） | 200 | 2、3、5 |
| 3（Failed） | 200 | 6 |
| 3（Failed） | 非 200 | 1、2、4、7 |
| 0 / 1（进行中） | — | NULL |

由此：

- `status = 2` ⟺ `status_code = 200 AND finish_reason IN (2, 3, 5)`
- 进行中（原 status 0/1）⟺ `finish_reason IS NULL`

该等价是精确的，速度指标（`WHERE status = 2`）口径不变。

## 非目标

- 不改变 `finish_reason` 或 `status_code` 的取值含义与写入逻辑。
- 不改变连续聚合（`request_overview_bucketed` / `request_speed_bucketed`）——它们不依赖 `status` 列，仅按 `type = 1` 与 token/耗时阈值聚合。
- 不改变实时状态查询（`getRequestLive`）的内存态 `phase` 字段——它独立于 DB `status`，继续驱动"处理中/已收到响应头/流式接收中"展示与打断按钮。
