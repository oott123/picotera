# 设计：放开 llmbridge 插件 gRPC 消息尺寸上限

## 问题根因

流式（SSE / JSONL）上游响应在结束后会被聚合成一份完整响应，用于 artifact 存储与 token/TTFT 提取。聚合流程：

1. `pkg/server/live_requests.go` 的 `liveProgress.body`（`bytes.Buffer`）在流式过程中把**整条响应流逐 chunk 累积**到内存，结束时通过 `artifactRecord()` 返回完整 body。
2. `pkg/server/response_aggregation.go:buildAggregatedArtifact` 把这份完整 body 交给 `bridge.AggregateStream(...)`。
3. `pkg/llmbridge/plugin_client.go:AggregateStream`（238 行）把整份 body 放进 `AggregateStreamRequest.Body`，作为**单个 gRPC unary 请求**发送到独立的插件子进程（HashiCorp go-plugin，gRPC 通信）。
4. 插件进程 `cmd/picotera-llmbridge-plugin/main.go` 接收、聚合，再把结果作为 `AggregateStreamResponse.Body` 返回。

gRPC 默认的单条消息上限是 **4 MiB**：
- server 端默认 `MaxRecvMsgSize = 4 MiB`
- client 端默认 `MaxCallRecvMsgSize = 4 MiB`

当超长流式响应拼接后的 body 超过 4 MiB 时：
- host → 插件方向（请求 body 过大）触发**插件 server 端 RecvMsg 超限**；
- 插件 → host 方向（聚合结果过大）触发**host client 端 RecvMsg 超限**。

gRPC 返回 `codes.ResourceExhausted`，错误文案形如 `received message larger than max (xxx vs. 4194304)`，即用户看到的"请求尺寸太大"。

同样的 4 MiB 上限也作用于 `BridgeRequest` / `BridgeNonStream` 这两个 unary 调用——大体积的非流式请求体 / 响应体一样会被卡住。`BridgeStream` 走 bidi streaming，按 32 KiB 分帧发送，不受单条消息上限影响，无需改动。

## 方案

把插件 gRPC 通信两侧的单条消息上限统一调高到一个充裕的固定值，覆盖请求与响应两个方向，让所有 unary 调用（`AggregateStream` / `BridgeRequest` / `BridgeNonStream`）都不再被 4 MiB 卡住。

### 限值

在 `pkg/llmbridge` 包内定义导出常量：

```go
// MaxGRPCMessageSize 是插件 gRPC 单条消息的尺寸上限。聚合超长流式响应时整条
// 拼接后的 body 通过单个 unary 调用跨进程传输，远超 gRPC 默认的 4 MiB。
const MaxGRPCMessageSize = 256 << 20 // 256 MiB
```

选 256 MiB：约为默认值的 64 倍，足以覆盖任何现实中的超长流式聚合 body，同时仍是一个有界上限（gRPC 仅在实际消息达到该尺寸时才分配，常量本身不预占内存）。

### 两侧改动

**插件 server 端**（`cmd/picotera-llmbridge-plugin/main.go` 的 `GRPCServer` 回调）追加：

```go
grpc.MaxRecvMsgSize(llmbridge.MaxGRPCMessageSize),
grpc.MaxSendMsgSize(llmbridge.MaxGRPCMessageSize),
```

**host client 端**（`pkg/llmbridge/plugin_client.go` 的 `startPlugin`，go-plugin `ClientConfig`）追加 `GRPCDialOptions`：

```go
GRPCDialOptions: []grpc.DialOption{
    grpc.WithDefaultCallOptions(
        grpc.MaxCallRecvMsgSize(MaxGRPCMessageSize),
        grpc.MaxCallSendMsgSize(MaxGRPCMessageSize),
    ),
},
```

server 与 client 的 send 方向默认本就接近无上限（`math.MaxInt32`），仍一并显式设置，使两侧两个方向对称、行为可预期。

### 为什么用常量而非配置项

插件是独立子进程，host 仅通过 `cmd.Env` 传少量环境变量给它；要让上限可配置就需要新增 env、贯穿 `configx` → `server.go` → `llmbridge.Config` → host 拨号选项，并额外让插件进程读取一个新 env。这套链路对一个"放开默认上限"的修复属于过度设计。固定常量两侧共享一处定义，足以缓解问题。

## 不在本次范围

- `liveProgress.body` 把整条流式响应累积进内存（`bytes.Buffer` 无上限）是独立的内存占用问题，与本次的跨进程消息上限无关，不在此次改动内。
</content>
