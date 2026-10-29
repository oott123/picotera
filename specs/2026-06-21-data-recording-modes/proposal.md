# 数据记录模式（Data Recording Modes）

给用户设置增加一个「数据记录（OTR，off the record）」选项，提供三档，统一用「把什么移出记录」的语义表达：

1. **`none`（完整记录）** —— 不做任何 OTR，行为与当前一致。
2. **`body`（不记录 body）** —— 上传 artifacts、检查 live 时，不记录请求体、响应体、聚合 JSON。
3. **`body-and-message`（不记录 body 与消息）** —— 在 `body` 的基础上，额外不记录「用户消息」栏（`user_message_preview`）。

header 与用户设置共用同一套三挡值（`none` / `body` / `body-and-message`），命名保持对齐。

## 上下文与 header 覆盖

- 在网关（path 路由）和 unified 请求时，把这个开关解析后挂到 per-request 上下文里，统一驱动所有记录点。
- 用户设置：`user_setting` 表，key = `request.otr`，value 为上述三挡值之一的 JSON 字符串；缺省（缺失）= `none`。
- 允许通过请求头覆盖用户设置：`X-PicoTera-OTR`，取值同为 `none` / `body` / `body-and-message`。
  - header 存在且合法时**完全覆盖**用户设置；不存在时按用户设置（缺省为 `none`）。
  - header 值非法（非上述三者之一）时**拒绝请求并返回 400**。

## 上游清理

向上游发送请求时，移除所有以 `X-PicoTera` 开头的请求头（不仅是 OTR header），避免泄漏到上游 provider。

## 记录细节（已确认）

- `body` / `body-and-message` 模式下，artifacts **保留 headers 与状态码**，但**清空 body、聚合 JSON 以及逐行时序（per-line timings）**——没有 body 时时序无意义。
- live 检查同样不缓存 body、不记录时序；仅保留字节计数与状态用于进度显示。
- TTFT、总耗时、token、费用等指标来自响应流的独立提取器，始终记录，不受本开关影响。
- 上游错误文本仍写入 `request.error_message` 列（属诊断元数据，不属本开关范围）。
