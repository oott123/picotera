# 设计：模型历史费用重算

## 目标

在模型列表页给每个模型一个「重算历史费用」入口：按该模型**当前**的 `model.pricing` 重新计算它在所选时间区间内所有已结束请求的 `model_cost` / `model_cost_currency`，并让概览里的费用统计立即反映这次改写。

## 「实际费用」的定义

请求费用 = 该行自己的五个 token 计数 × 该模型**当前**价格档位，币种取 `pricing.currency`。

计算函数就是网关写库时用的同一个 `computeCost`（`pkg/server/pricing.go`）：

- 档位：`pickTier` 选 `minInputTokens <= input_tokens` 的最高档；
- 金额：`(输入单价×input_tokens + 输出单价×output_tokens + 缓存读单价×cache_read_tokens + 缓存写单价×cache_write_tokens + 1h 缓存写单价×cache_write_1h_tokens) / 1e6`，用 `big.Rat` 精确算术后经 `ratToNumeric6` 落到 `NUMERIC(20, 6)`；
- `implicitCacheRead` 与写库时一致地**不参与**（该字段是预留的，没有 token 列）。

所以重算的结果与「这次请求若在现在被写库」逐位一致，包括舍入。**不在 SQL 里重写计价公式**，见下。

无法计价的行（`input_tokens IS NULL`，或价格 JSON 里没有匹配档位）写 NULL / NULL —— 与 `costsFor` 的行为一致，不是特例。

### 待重算的行

同时满足三条：

1. `model = <name>`：精确匹配路由后命中的模型名（与 `costsFor` 的入参同一个字段，也唯一对应 `model` 表主键）。`upstream_model` 不参与 —— 计费口径跟踪的是我们的模型目录，不是上游的别名。
2. `created_at ∈ [start, end)`：见「时间区间」。
3. `finish_reason IS NOT NULL`：**终态行**判据。

type = 0（meta 行）与 type = 1（上游行）**都写**：两行的 token 与费用在网关里由同一条 UPDATE 写入同一份值（`completeGatewaySuccess` / `unifiedStreamSuccess`），只重算其中一行会让请求列表与 trace 汇总互相矛盾。

「终态 ⟺ token 不再变化」成立的原因：网关所有终态写入（成功、失败、脚本短路 `respondScriptResponse`、客户端断连）都在同一条 UPDATE 里写入 token 与 `finish_reason`，此后再没有任何代码路径改动这两组字段。于是：

- 正在流式进行中的请求（`finish_reason IS NULL`）不被重算 —— 它还没有 token 可算，费用由网关在它结束时自己写；
- 扫描到的行，其 token 在整个重算期间不会被并发改写，不存在「读到半截 token 再写回去」的交错。

## 时间区间

请求里的 `range` 是**任意 Go duration 字符串**（`time.ParseDuration`，单位 ns/us/ms/s/m/h），不设 enum；服务端在收到请求时取一次 `now`：

| `range` | 窗口下界 | 上界 |
| --- | --- | --- |
| 任意合法 duration，如 `24h` / `168h` / `720h` | `now - duration` | `now` |
| 空字符串或字段省略 | 无（SQL 侧 NULL） | `now` |

- 解析失败、非正数（`0` / `0s` / 负数）→ 400，不做兜底解读（fail fast）。顺带一提，Go 的 duration 没有 `d` 单位，所以 `7d` 会解析失败 —— 前端下拉框自己把天数换算成小时。
- 上界在处理开始时固定：本次重算的范围在那一刻就确定，不随后续写入扩张；`end` 之后新产生的请求本来就会在网关侧写入正确费用。
- 不接收绝对时间戳：调用方只表达「最近多久」或「全部」。

仪表盘下拉框固定四档，发送值：`24 小时` → `24h`、`7 天` → `168h`、`30 天` → `720h`、`全部` → 空字符串。

## 执行方式：Go 侧分批计算

同步阻塞（用户已确认）。循环三步：

1. `ListRequestCostRecalcBatch`：按 `(created_at, id)` 升序 **keyset** 扫描，取一批 ≤ 2000 行的 `(id, created_at, 五个 token 列)`。
2. Go 侧用 `computeCost` 逐行算出 `(pgtype.Numeric, pgtype.Text)`。
3. `UpdateRequestCosts`：一条 `UPDATE request ... FROM ROWS FROM (unnest($ids), unnest($created_ats), unnest($costs))`（Postgres 只有单数组与双数组的 `unnest`，四列并列数组必须走 `ROWS FROM`；币种是标量参数，因为一次重算只对应一个 pricing 的币种，且 sqlc 把 `text[]` 参数落成 `[]string`、表达不了 SQL NULL），按 `(id, created_at)` 主键精确命中。

每批一条 UPDATE、一个隐式事务，**不套外层事务**：操作天然幂等（同样的价格 + 同样的 token 得到同样的值），中断后已完成的批次保留，重跑一遍即可。

**为什么不在 SQL 里算。** 档位选择、按 1e6 缩放、6 位小数舍入这套算术已经在 `computeCost` 里实现并被 `pricing_test.go` 覆盖；换成 SQL 表达式就是第二份计价实现 —— JSON 浮点数转 numeric 的舍入、档位边界、NULL 语义都要重新对齐，两处一旦漂移就是账单口径不一致。分批扫 + Go 算 + 批量 update 只多几次往返，对管理员动作完全可接受。

**为什么用 keyset 而不是 OFFSET / 重复取同一批。** 被改写的行仍然满足 `model = X AND created_at ∈ 区间`（费用列不在谓词里），同一条件重复执行会永远命中同一批；`(created_at, id)` 游标保证单调前进。实测（dev 库 `EXPLAIN`，带参数的形态）：游标比较被下推成索引条件 `Index Cond: created_at >= <cursor>`，每批从游标处继续扫，整个重算的扫描量与区间行数成正比（配合 `ORDER BY created_at, id LIMIT n` 的 ChunkAppend + 反向索引扫描）。

**不加新索引。** `request` 已有 10 个索引，网关每个请求写两行；重算是低频管理员动作，扫描代价与所选区间行数成正比，改写代价本来就是同一量级（每行还要维护这 10 个索引）。为它加 `(model, created_at)` 索引是给热路径添纯写入开销。

**客户端断开 / 反代超时。** 处理使用请求自带的 context：断开后剩余批次不再执行，已写的部分保留、结果不可知，重跑一遍即完成。这是同步阻塞的既定代价，不引入 `context.WithoutCancel`（用户选择的就是这一档）。

## 概览连续聚合刷新

`request_overview_bucketed` 含 `SUM(COALESCE(model_cost, 0))`，概览的每一张费用卡片与曲线都读它；`request_speed_bucketed` / `request_outcome_bucketed` 不含费用列，不受影响。

它是 `materialized_only = false` 的连续聚合，刷新策略只覆盖 `[now - 35d, now - 5m]`、每 5 分钟一次。已物化的分桶不会因为底层行被改写而自动变化（实测：物化后改写原始行，视图仍返回旧值，直到显式刷新），因此：

- 重算 35 天内的数据：策略最迟 5 分钟后会追上，但我们不等；
- 重算 35 天以前的数据：那些分桶早已物化、策略不再覆盖，不显式刷新就一直是旧值。

所以重算结束后（且确实有行被改写时）执行一次：

```sql
CALL refresh_continuous_aggregate('request_overview_bucketed', <start>, NULL)
```

- `range` 为空（「全部」）时传 NULL → 从最开始重物化（代价与历史数据量成正比，是「全部」这个选项的固有成本）。
- 上界 NULL = 刷新到现在。
- **不能跑在事务块里**：实测 `ERROR: refresh_continuous_aggregate() cannot run inside a transaction block`。pgx 对单条 `CALL` 的 Exec 是自动提交，满足约束。
- 窗口参数必须是 **`timestamp without time zone`**（与 `request.created_at` 同类型）：实测传 `timestamptz` 会报 `invalid time argument type "timestamp with time zone"`，SQL 里显式 `::timestamp`。

刷新失败只记 warn、不让整个操作失败：行改写已经落库，聚合刷新是派生数据的重建，重跑重算即可补上。

## 并发与竞态

- 进行中的请求不在范围内（见「待重算的行」），它们的费用由网关自己写。
- 两个管理员同时重算同一模型：各自算出同一组值，后提交的覆盖前一个，最终一致；不加分布式锁。
- 重算与网关写入同一行：终态行不会被网关再改，非终态行不在范围内，无交错。

## 权限与错误

- 模型是管理员资源，重算端点注册在 **admin 组**：非管理员 403。
- 端点跨越所有用户的请求行（`model` 是全局配置）。这与其它管理端点一致：`is_admin` 是能力闸门，重算是全局维护动作，不按 `user_id` 过滤。
- 模型不存在 → 404；模型没有价格（`pricing` 为空/无档位）→ 400 `model has no pricing`（没有价格就没有可写的值，与其把历史费用清空不如直接拒绝）；`range` 解析失败或非正 → 400；`name` 缺失 → 422（Huma schema 校验）。

## 响应

`{ model, range, startAt, endAt, updated, tookMs }`。`updated` 是被改写的请求行数（= 扫描并写入的行数，两者按构造相等）；`range` 回显请求里发来的字符串（「全部」时为空串）；`startAt` 在「全部」时为 null；`endAt` 是本次处理固定的上界；`tookMs` 是服务端耗时。同步执行，所以响应本身就是最终结果。

## 不做的事

- 不引入后台任务、进度轮询、内存任务表、任务状态接口。
- 不做 dry-run 预览计数。
- 不在 SQL 里重写计价公式，不引入价格档位的第二份实现。
- 不改表结构、不加索引、不加迁移（连续聚合刷新与新增查询都跑在既有 schema 上）。
- 不动 `tool_cost` / `tool_cost_currency`（工具费用由脚本 hook 产出，没有可复算的公式）、`usage_raw` 等记录列，也不动五个 token 列。
- 不重算 `finish_reason IS NULL` 的行，不为它们补写费用。
- 不主动刷新 `request_speed_bucketed` / `request_outcome_bucketed`（不含费用列）。
- 不改写 `traces`（trace 的费用视图是按 `request` 行实时汇总的）。