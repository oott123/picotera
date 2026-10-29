# 让 live 与 artifact 共享同一份记录

## Context

每个网关请求的「响应体 + 每行到达时间(timings)」目前被**记录两遍**：

- 一遍进 artifact 缓冲（`captureBuf` / `clientCapture` / `upstreamCapture` + `LineTimingRecorder`），请求结束后上传到 MinIO；
- 一遍进 `liveProgress`（内存，供实时状态面板读取）。

用户希望按行(row)统一：**upstream 行的 live 与 upstream 行的 artifact 共享一份记录；meta 行的 live 与 meta 行的 artifact 共享一份记录**。不是把 meta 和 upstream 合并，只是消除「同一行内 live 与 artifact 各记一遍」的重复。

统一后 `liveProgress` 成为每条流的**唯一累积器**（body + timings + 状态），artifact 上传时从它取快照。

### 关键现状（来自调研）

- `liveProgress`（`live_requests.go:61`）每个 **upstream** entry 自带一个（`RegisterUpstream`，`live_requests.go:110`）；**meta** entry 没有自己的 progress，通过 `active atomic.Pointer[liveProgress]` 镜像到「当前正在流式的 upstream」（`live_requests.go:54`、Snapshot `:157`）。
- 两条流式成功路径：
  - 路径网关 `pipePathResponse`（`gateway_flow_success.go:180`）—— **永远是 identity**（无格式转换）：上游字节 = 客户端字节 = meta/upstream artifact 字节，单条流。
  - unified `unifiedStreamSuccess`（`gateway_unified_helpers.go:311`）—— `transforming = bridging || wsActive`（`:354`）。转换时存在**两条不同字节流**：`upstreamCapture`(上游格式，喂上游 artifact + `timingRecorder` 在其上计时) 与 `clientCapture`(源格式，喂 meta artifact + 客户端)。
- 现状 bug：转换时 `recordChunk` 喂的是**客户端(源格式)字节**到 **upstream** 的 progress（`:495`，`progress=input.Entry.progress`），导致 upstream 的 live 显示源格式、但其 artifact 是上游格式 —— **对不上**。本方案会修正。
- `timingRecorder.Timings` 起点是 `UpstreamStartTime`；`liveProgress` 现在起点是注册时间 `startedAt`。统一时需对齐到 `UpstreamStartTime`。
- `NewUpstreamTee(src io.ReadCloser, tee *bytes.Buffer)`（`pkg/llmbridge/tee.go:18`）只能 tee 到 `*bytes.Buffer`。

## 目标设计

**每条流的 `liveProgress` = 该行 live 视图与 artifact 的唯一来源（body + timings）。**

- **upstream entry**：始终自带 progress，喂**上游格式**字节流（即今天 `timingRecorder`/`upstreamCapture` 看到的字节）。upstream artifact 从它取快照。
- **meta entry** 通过 `active` 指针指向「代表 meta 行 live 的 progress」：
  - **identity**（路径网关 + unified 非转换）：`active` → upstream progress（沿用今天）；meta artifact 也读 upstream progress（字节相同）。→ **单 buffer**（比今天的 2 个更省）。
  - **transforming**（bridging / web-search）：success 路径新建一个 **meta 专属 progress**（源格式），`active` → 该 meta progress；meta artifact 从它取快照。upstream progress 仍由 tee 喂上游格式。→ 2 buffer（与今天 `upstreamCapture`+`clientCapture` 相同，不可消除）。

删除：`captureBuf`、`clientCapture`、`upstreamCapture`(bytes.Buffer)、`LineTimingRecorder`/`timingRecorder`、identity 的 `clientBytes=upstreamBytes` reconciliation。

## 改动清单

### 1. `pkg/server/live_requests.go`
- `liveProgress`：保留已加的 `timings []float64`；新增 `timingStart time.Time`（计时起点，区别于用于显示的 `startedAt`）。
- `markHeaders(statusCode int, start time.Time)`：增设 `p.timingStart = start`。
- `recordChunk(b []byte)`：ms 用 `time.Since(p.timingStart)`（`timingStart` 为零时回退 `startedAt`）；其余（body/bytes/逐换行 append timings/lastChunkAt）不变。
- 新增 `func (p *liveProgress) artifactRecord() (body []byte, timings []float64)`：加锁返回 **拷贝**（`bytes.Buffer.Bytes()` 复制一份，timings `append(nil,…)`），供 success 路径在循环结束后取最终记录。
- 新增带起点的构造（如 `newLiveProgress(origin time.Time)` 或保留无参 + 由 `markHeaders` 设起点），用于 transforming 时新建 meta progress。
- `Snapshot` 已返回 `Timings` 拷贝，无需再改。

### 2. `pkg/llmbridge/tee.go`
- 将 `NewUpstreamTee` 第二参从 `*bytes.Buffer` 改为 `io.Writer`（仅一个调用点）。这样可传入一个把字节转发给 `upstream progress.recordChunk` 的小适配器。

### 3. `pkg/server/gateway_flow_success.go`（路径网关，identity）
- `markPathHeadersReceived`：`markHeaders(status, input.UpstreamStartTime)`。
- `pipePathResponse`：reader 链去掉 `timingRecorder`（`extractor → idleReader`）；删 `captureBuf`；循环只 `progress.recordChunk(buf[:n])`（upstream progress）；返回值改为不再返回 `captureBuf`/`timingRecorder`。
- `streamSuccess` / `aggregatePathResponse`：upstream artifact 与 meta artifact 均从 `progress.artifactRecord()` 取 body+timings（identity，meta 读 upstream progress）。`extractor.Metrics()`(TTFT/token) 不受影响。

### 4. `pkg/server/gateway_unified_helpers.go`（unified）
- `markHeaders(resp.StatusCode, a.upstreamStartTime)`（`:340`）。
- transforming 时新建 `metaProgress`（起点 `a.upstreamStartTime`），`metaEntry.active.Store(metaProgress)`；identity 时 `active.Store(input.Entry.progress)`（沿用）。
- 上游字节记录：`teedUpstream` 的 tee 目标从 `&upstreamCapture` 改为转发到 **upstream progress**（适配器）；删除 `timingRecorder`（计时由 upstream progress.recordChunk 承担，tee 读时机 ≈ 今天 timingRecorder）。
- 客户端循环（`:487`）：删 `clientCapture`；`if transforming { metaProgress.recordChunk(buf[:n]) }`（identity 时 upstream progress 已由 tee 喂，循环只 `w.Write`+flush）。
- 删除 reconciliation（`:516-521`）。
- 上传 artifact：upstream artifact ← `upstreamProgress.artifactRecord()`；meta artifact ← `(transforming ? metaProgress : upstreamProgress).artifactRecord()`（body 与 timings 都来自同一快照，因此 **bridging 时 meta artifact 会带上源格式 timings**，取代今天的 nil）。aggregation 逻辑（`buildAggregatedArtifact`）保留，仅 body 来源改为快照。

### 5. 删除 `pkg/server/line_timing_recorder.go`
- 全仓仅这两处使用，重构后无引用，整文件删除。`asReadCloser`（`gateway_unified_helpers.go:631`）仍保留（tee 仍需包 extractor）。

## 行为变化

- **identity（绝大多数请求）**：无可见变化；内部从 2 buffer 降为 1 buffer，去掉 `LineTimingRecorder` 分配。
- **transforming（跨格式 / web-search）**：
  - upstream 的 live 现在显示**上游格式**字节 + 上游流 timings（修正了今天显示源格式的不一致），与 upstream artifact 一致；
  - meta 的 live 仍是源格式（内容不变，改由 meta 自己的 buffer 提供）；
  - meta artifact 现在带**源格式每行到达时间**（今天为空），与 meta live 一致。

## Verification

1. `gofmt` + `go build ./...`（含 `cmd/llmbridge-wasm` 受 tee 改动影响时 `mise run wasm`）。
2. `go test ./pkg/llmbridge/... ./pkg/server/...`（tee 改签名后确认编译/现有测试通过）。
3. 起服务 `mise run server`（需 `docker compose up -d`）+ `mise run web`，手动验证：
   - **identity 流式请求**（如直传 anthropic→anthropic）：请求进行中实时面板「响应体(至今)」+ 勾选「显示到达时间」逐行时间正常；请求完成后 artifact「原始响应 / 显示到达时间」与 live 末态一致（行数、到达时间一致）。
   - **bridging 流式请求**（源格式≠上游，如 openai 客户端经 anthropic 上游）：
     - 选 meta span：live 显示源格式 + 到达时间；完成后 meta artifact 同样带到达时间且与 live 一致。
     - 选 upstream span：live 显示**上游格式** + 到达时间；完成后 upstream artifact 与之一致。
   - 非流式 bridging：live/artifact body 一致，timings 为单批（可接受）。
   - 打断(interrupt) 仍可用，live 在进行中可拉取。
4. 确认 `liveProgress` 并发安全：所有 body/timings 读取（`Snapshot`、`artifactRecord`）均在锁内返回拷贝；tee 适配器与客户端循环在同一 success goroutine 写、API goroutine 读。
