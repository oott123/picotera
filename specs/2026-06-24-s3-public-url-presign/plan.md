# 执行计划:S3 public URL 预签名修复

## 1. 调整 sink 状态

`pkg/artifacts/sink.go`:

- 在 `minioSink` 保存 access key、secret key、region、public URL 和 path style。
- 上传 client 保持现有构造方式,继续使用 `PICOTERA_S3_ENDPOINT`。

## 2. 增加 public virtual-hosted signer

`pkg/artifacts/sink.go`:

- 新增 `presignedGetPublicVirtualHosted`。
- 该函数直接使用 `PICOTERA_S3_PUBLIC_URL` 构造最终 URL。
- 该函数把最终 URL 包装成 `GET` request,再调用 minio-go 暴露的 `signer.PreSignV4` 生成
  AWS SigV4 query presign。
- 该函数不读取或拼接 bucket name。

## 3. 切换 PresignedGet 分支

`pkg/artifacts/sink.go`:

- 当 `publicURL != "" && PathStyle != nil && *PathStyle == false` 时调用新的 public virtual-hosted signer。
- 其他情况保持现有 minio-go presign 行为。

## 4. 测试

`pkg/artifacts/sink_test.go`:

- 覆盖 `bucketLookup(nil/true/false)`。
- 覆盖 public virtual-hosted presign 的 host、path、query scope。
- 删除签名参数后复算 canonical request 和 HMAC,断言签名使用最终 URL 的 host/path。

## 5. 验证

运行:

```bash
go test ./pkg/artifacts
go test ./pkg/configx
go build ./cmd/picotera
```
