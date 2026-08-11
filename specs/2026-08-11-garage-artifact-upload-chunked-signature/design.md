# 设计：artifact 上传关闭 SigV4 分块流式签名

## 问题根因

`pkg/artifacts/sink.go:184` 的 `minioSink.upload` 通过 minio-go 的 `PutObject` 上传 artifact。minio-go 的签名模式选择在 `api.go:971`：

```go
case metadata.streamSha256 && !c.secure:
    req = signer.StreamingSignV4(...)
```

其中 `streamSha256 = !opts.DisableContentSha256`（默认即 `true`），`c.secure` 来自 `minio.Options.Secure`（即 `PICOTERA_S3_USE_SSL`）。因此：

- **HTTPS 端点**：走 `default` 分支，`putObject` 传入的 `contentSHA256Hex` 为空、也没有 trailer，于是 `X-Amz-Content-Sha256: UNSIGNED-PAYLOAD`，body 是原始字节。
- **HTTP 端点**（Garage 部署的常见形态）：走 `StreamingSignV4`，`prepareStreamingRequest`（`pkg/signer/request-signature-streaming.go:126`）设置 `X-Amz-Content-Sha256: STREAMING-AWS4-HMAC-SHA256-PAYLOAD`，body 改成逐块签名的 aws-chunked 帧格式。

关键在于：**无 trailer 的这条路径上，minio-go 从不设置 `Content-Encoding: aws-chunked`**（只有 trailer 分支才设 `TransferEncoding`），而我们又显式把 `Content-Encoding` 覆盖成了 `zstd`。

Garage 对 `STREAMING-*` 系列做严格校验：`x-amz-content-sha256` 声明为流式时，`Content-Encoding` 必须包含 `aws-chunked`，否则直接 400 —— 就是日志里的那条报错。MinIO 服务端对此宽容，所以本地 docker-compose 的 MinIO 一直正常，只有换到 Garage 才暴露。

这与 SDK 版本无关，也不是反代改写请求头导致的：它是 minio-go 在明文 HTTP 下的默认签名模式与 Garage 严格校验之间的固有冲突。

## 修复方案

在 `PutObject` 的 options 上设置 `DisableContentSha256: true`：

```go
minio.PutObjectOptions{
    ContentType:          "application/json",
    ContentEncoding:      "zstd",
    DisableContentSha256: true,
}
```

效果：`reqMetadata.streamSha256` 变为 `false`，无论端点是否 TLS 都走 `default` 分支，发出 `X-Amz-Content-Sha256: UNSIGNED-PAYLOAD` 的普通 SigV4 请求，body 为原始字节，`Content-Encoding` 保持我们想要的 `zstd`。

选择这个方案的理由：

1. **零回归面**。改完之后 HTTP 端点的行为与今天 HTTPS 端点的行为逐字节一致 —— 后者本来就是 `UNSIGNED-PAYLOAD`。也就是说这条代码路径早已在生产中被验证过。
2. **Garage 明确支持**。Garage 的 `parse_x_amz_content_sha256` 对 `UNSIGNED-PAYLOAD` 有专门分支（缺失该头时同样按 unsigned 处理），不存在兼容性疑问；MinIO / AWS S3 亦然。
3. **不污染对象元数据**。另一条思路是把 `Content-Encoding` 写成 `aws-chunked,zstd` 去迎合校验，但那要依赖服务端在落盘时剥掉 `aws-chunked`（各家实现不一致，Cloudflare R2 就有过不剥的 bug），会让 artifact 的 `Content-Encoding` 元数据变脏，进而影响预签名 GET 时浏览器的解码行为。不采纳。
4. **顺带省一次哈希**。原路径会对整个 payload 逐块算 SHA256，新路径不算。

该选项对 multipart 同样生效：超过 16 MiB 的 artifact 走 `putObjectMultipartStreamOptionalChecksum`，其 `uploadPartParams.streamSha256` 同样取自 `!opts.DisableContentSha256`。

### 关于完整性校验

`UNSIGNED-PAYLOAD` 下 body 不参与签名，明文 HTTP 上没有端到端完整性保证。可选的补法是 `SendContentMd5: true`（`Content-Md5` 属签名头）。本次明确不做 —— 与今天 HTTPS 部署的保证水平持平即可。

## 影响面

`minioSink.upload` 是仓库内唯一的 S3 写入点（`Put` 被 `gateway_flow_success.go` 等处的 artifact 上传统一收敛），改一处即可覆盖全部 artifact 上传。`PresignedGet` 走独立的签名路径，不受影响。

## 不做的事

- 不新增 `PICOTERA_S3_*` 配置开关来切换签名模式：无条件生效更干净，不需要按服务端实现分叉。
- 不改 `Content-Encoding: zstd` 的语义。
- 不加回归测试（按需求确认）。
