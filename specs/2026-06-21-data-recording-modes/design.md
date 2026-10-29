# 设计：数据记录模式

## 概念模型

引入一个 per-request 的 OTR（off the record）模式，三档，用「把什么移出记录」表达：

```go
type otrMode int8

const (
    otrNone           otrMode = iota // 不 OTR，完整记录（默认）
    otrBody                          // body / 聚合 / 时序 移出记录
    otrBodyAndMessage               // 在 otrBody 基础上再移出 user_message_preview
)

func (m otrMode) recordBody() bool    { return m == otrNone }
func (m otrMode) recordPreview() bool { return m != otrBodyAndMessage }
```

两个布尔派生量驱动所有记录点：

- `recordBody()` —— 是否记录请求体、响应体、聚合 JSON、逐行时序、live body 缓冲。
- `recordPreview()` —— 是否记录 `user_message_preview`。

## 解析与优先级

header 与用户设置**共用同一套字符串值** `none` / `body` / `body-and-message`，命名对齐。模式由两处输入解析，header 优先：

1. **header**：`X-PicoTera-OTR`
   - `none` → `otrNone`，`body` → `otrBody`，`body-and-message` → `otrBodyAndMessage`
   - 空 / 缺失 → 不覆盖，落到用户设置
   - 其它任意非空值 → **400 拒绝**
2. **用户设置**：`user_setting` 表，key = `request.otr`，value 为上述三挡值之一的 JSON 字符串
   - 同上映射；缺失 / 其它值 → `otrNone`

二者共用一个 `parseOTRValue(s string) (otrMode, bool)` 解析函数：header 路径对 `ok == false` 返回 400，设置路径对 `ok == false` 退回 `otrNone`。

用户设置走既有的通用 `user_setting` CRUD（`/api/picotera/settings`），不新增管理 API。读取沿用 `project.autoCreate` 同一套模式（`GetUserSetting` + `json.Unmarshal`）。

## 时序问题：为什么解析点在认证之后

关键约束：**记录模式默认值取自用户设置，而用户身份只有在认证后才知道**。

当前 `insertMetaRequest`（认证前）做了两件「记录」动作：提取并写入 `user_message_preview`、上传请求体 artifact。这两者都依赖记录模式，因此必须**推迟到认证之后**：

- `insertMetaRequest`：meta 行插入时 `user_message_preview = NULL`，**不再**上传请求 artifact。
- `authenticateAndBackfill`：用户已知后解析 `f.otr`，随后：
  - 若 `recordPreview()`：计算 preview 并回填（新增专用 query `UpdateRequestUserMessagePreview`，避免与后续 `UpdateRequestOnHeader` 调用相互覆盖）。
  - 上传请求 artifact，body 是否清空取决于 `recordBody()`（保留 method/url/headers）。

**header 合法性校验**在 `run()` 入口（`readBody` 之后、`insertMetaRequest` 之前）进行：非法值直接 `writeGatewayError(400)` 返回，不创建 meta 行。合法的 header 模式暂存在 flow 上，认证后参与最终模式计算。

### 行为变更

认证失败的请求**不再记录请求体 artifact 和 user_message_preview**（认证前无法得知用户设置，无法判断该不该记）。失败请求的 meta **响应** artifact（401/403 错误体）仍由 `failMeta` 路径记录，故仍有失败记录，只是缺请求体与 preview。这是按用户设置门控的必然结果，属可接受的轻微行为变更。

## 记录点门控

### 请求体（path 与 unified 共用）

`gateway_flow.go` 的 `insertMetaRequest` / `authenticateAndBackfill`（见上）。

### 响应体 + 时序：经 `liveProgress` 统一门控

`liveProgress` 是 live 视图与持久化 artifact body/timings 的**唯一来源**（artifact body 取自 `progress.artifactRecord()`）。因此在 `liveProgress` 层门控可一次覆盖 live 与持久化两侧：

- 给 `liveProgress` 增加 `recordBody bool` 字段。
- `recordChunk`：`recordBody == false` 时，**跳过 body 缓冲与 timings 追加**，仅累加 `bytes` 计数。
- 于是 `artifactRecord()` 返回空 body + 空 timings；`Snapshot()`（live 视图）返回空 body、空 timings，但保留字节数与状态。
- 构造入口（`newLiveProgress` / `newLiveProgressWithOrigin` / `RegisterUpstream`）增加 `recordBody` 形参，由 flow 的记录模式传入。

响应流的 token/TTFT 提取器（`NewResponseExtractor`）独立于 `liveProgress` 读取字节流，指标不受影响。

### 聚合 JSON

`aggregatePathResponse`（path）与 `unifiedStreamSuccess`（unified）中，`recordBody() == false` 时跳过 `buildAggregatedArtifact`，传 `nil` 聚合。

### artifact body 清空辅助

为 path/unified 各调用点统一清空 body，增加 flow 辅助：

```go
func (f *gatewayFlow) artifactBody(b []byte) []byte {
    if f.otr.recordBody() {
        return b
    }
    return nil
}
```

非 200 上游响应 artifact（`handleUpstreamNonOK`）、afterUpstreamError break 的 meta 响应 artifact（`respondUpstreamErrorBreak`）、各类 gateway 错误响应 artifact（`gateway_flow_errors.go`）等调用点，body 实参统一包一层 `f.artifactBody(...)`。`unifiedStreamSuccess` 在 `gatewayHandler` 方法上，经 `input.Flow` / `unifiedStreamArgs` 取得记录模式。

> 统一规则：`recordBody == false` 时清空**所有** artifact 的 body + 聚合 + 时序（请求/响应、成功/失败、上游/网关自生成），仅保留 headers/状态/日志。上游错误文本仍在 `error_message` 列，故诊断信息不丢。

## 上游 header 清理

`buildUpstreamRequest`（`gateway_helpers.go`，path 与 unified 共用）的 header 复制循环增加跳过条件：`strings.HasPrefix(lower, "x-picotera")`，移除所有 `X-PicoTera*` 头。`buildForwardedHeaders`（web-search 自调用）是白名单转发，本就不含 X-PicoTera，无需改动。

## Dashboard

`dashboard/src/views/SettingsView.vue` 增加「数据记录」选择控件（三选一：完整记录 `none` / 不记录 body `body` / 仅元数据 `body-and-message`），key = `request.otr`，value 为字符串。沿用既有 `getUserSetting` / `upsertUserSetting` 与 vue-query 模式（参照 `project.autoCreate` 复选框）。UI 控件复用 `src/ui/` 本地 Tailwind 原语（`SegmentedControl`），不引第三方。无 openapi / contract 改动。
