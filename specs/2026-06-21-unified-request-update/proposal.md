# 统一 request 行更新为一条通用查询

## 背景

网关流程对一条 `request` 行会分多个时机做局部更新，目前由 5 条窄查询承担：`UpdateRequestOnHeader`、`UpdateRequestModel`、`UpdateRequestUserMessagePreview`、`UpdateRequestMetrics`、`UpdateRequestOnComplete`。其中 `UpdateRequestOnHeader` 是"全字段覆盖"式 UPDATE——SQL 里每个字段都无条件赋值，调用方被迫每次把所有字段传一遍，漏传任一字段就会把它覆盖成 NULL。

这已直接导致一个线上 bug：上游响应头到达时的 `UpdateRequestOnHeader` 调用漏传了 `project_id`，把认证阶段刚回填好的项目清成了 NULL——表现为"请求 pending 时能看到项目，分配渠道、上游响应后项目消失"。

## 原始需求

把 request 行的所有更新合并为**一条通用 `UpdateRequest` 查询**，靠传入的布尔标志位决定更新哪些字段：例如 `SetProjectID=true, ProjectID=123` 时才写 `project_id`，否则保持该列原值不变。调用方只设置它真正要改的字段及对应标志，绝不触碰其余字段，从结构上根除"漏传字段被误清空"这一类 bug。

## 澄清与补充决定

- **覆盖范围**：通用查询统一替换上述全部 5 条窄查询；它们及对应的 Go wrapper 全部删除，无兼容层。
- **标志位语义**：每个可变列对应一个 `set_<col> bool` 标志；`true` 时写入传入值（可为 NULL），`false` 时 `CASE WHEN ... ELSE <col> END` 保持原值。这一机制也使"显式写入 NULL"成为可能（COALESCE 做不到）。
- **调用方 Go 侧的可读性**：提供一个链式 builder 构造参数，使各调用点只声明它要改的字段，避免手写约 48 个字段的大结构体。
