# 执行计划

## 1. 定义共享常量

在 `pkg/llmbridge/plugin_protocol.go` 的常量区（`pluginName` / `pluginABI` 附近）新增导出常量：

```go
// MaxGRPCMessageSize 是插件 gRPC 单条消息的尺寸上限。聚合超长流式响应时整条
// 拼接后的 body 通过单个 unary 调用跨进程传输，远超 gRPC 默认的 4 MiB。
const MaxGRPCMessageSize = 256 << 20 // 256 MiB
```

## 2. host client 端设置上限

在 `pkg/llmbridge/plugin_client.go:startPlugin` 的 `plugin.NewClient(&plugin.ClientConfig{...})` 中追加 `GRPCDialOptions` 字段：

```go
GRPCDialOptions: []grpc.DialOption{
    grpc.WithDefaultCallOptions(
        grpc.MaxCallRecvMsgSize(MaxGRPCMessageSize),
        grpc.MaxCallSendMsgSize(MaxGRPCMessageSize),
    ),
},
```

需在该文件 import 中加入 `"google.golang.org/grpc"`。

## 3. 插件 server 端设置上限

在 `cmd/picotera-llmbridge-plugin/main.go` 的 `GRPCServer` 回调里，向 `opts` 追加两个 server option：

```go
opts = append(opts,
    grpc.MaxRecvMsgSize(llmbridge.MaxGRPCMessageSize),
    grpc.MaxSendMsgSize(llmbridge.MaxGRPCMessageSize),
    grpc.ChainUnaryInterceptor(recoverUnary),
    grpc.ChainStreamInterceptor(recoverStream),
)
```

`grpc` 与 `llmbridge` 两个包该文件已 import，无需新增。

## 4. 验证

```bash
mise run llmbridge-plugin                 # 编译插件，确认 main.go 改动通过
go build ./...                            # 确认 host 侧改动通过
```

无现成针对 gRPC 上限的单元测试；本次为常量与拨号 / serve 选项配置，编译通过即可。如需手测：构造一个会产生 >4 MiB 聚合 body 的超长流式请求，确认聚合 artifact 不再因 `ResourceExhausted` 失败（聚合错误会记录在 artifact 的 `aggregated.Error` 字段，见 `response_aggregation.go`）。
</content>
