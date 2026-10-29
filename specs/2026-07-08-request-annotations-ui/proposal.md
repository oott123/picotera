# 请求 Annotations 前端界面（proposal）

在前端界面"请求"页，增加 annotations 筛选功能，放在 ID 后面，两个输入框，一个输入 key 一个输入 value。然后请求详情界面，如果请求有 annotations，需要渲染出来，每一堆 KV 都像原来那样增加一个小标题，标题为 k 值为 v。

> 关联但不合并：请求级 annotations 的后端与存储设计见 `specs/2026-07-08-request-custom-annotations/`（列、GIN 索引、脚本写入通道、`GET /api/picotera/requests?annotations=` 过滤参数、`RequestView.annotations`）。该功能后端已完成，本 spec 仅做 dashboard 界面。

## 规划过程中的补充澄清

- **筛选是单个 KV 对**：两个输入框分别是 key 和 value，构造 `{"<key>":"<value>"}` 作为 `annotations` 查询参数（后端多对时是 AND，本界面只暴露一对）。
- **筛选生效条件**：key 非空时生效；此时 value 可以为空串（精确匹配 value 为空串的注解）。key 为空则不带 annotations 参数，与其它筛选"空即不生效"的语义一致。
- **筛选状态不同步到 URL**：与渠道/端点/模型等 ColumnFilter 一致（仅追踪/ID/项目/时间同步 URL）。
- **详情渲染的是当前选中 span 的 annotations**：meta 行与 upstream 行各自独立，切换 span 卡片即切换所展示的注解。
- **每个 KV 用既有 `Field`（label + 值）渲染**：label 为 key、slot 内容为 value，整组归入一个"Annotations"小节标题下。
