# 执行计划

## 1. 修改 `pkg/artifacts/sink.go`

在 `minioSink.upload`（`sink.go:181`）的 `PutObjectOptions` 中加上 `DisableContentSha256: true`：

```go
func (s *minioSink) upload(j job) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := s.client.PutObject(ctx, s.bucket, j.key, bytes.NewReader(j.payload), int64(len(j.payload)), minio.PutObjectOptions{
		ContentType:     "application/json",
		ContentEncoding: "zstd",
		// 明文 HTTP 端点下 minio-go 默认走 SigV4 分块流式签名，会发出
		// X-Amz-Content-Sha256: STREAMING-AWS4-HMAC-SHA256-PAYLOAD 却不带
		// Content-Encoding: aws-chunked，Garage 会以 400 拒绝。关掉它，统一
		// 用 UNSIGNED-PAYLOAD —— 与 HTTPS 端点上的既有行为一致。
		DisableContentSha256: true,
	})
	if err != nil {
		s.logger.WithError(err).WithField("key", j.key).Warn("artifact: upload failed")
	}
}
```

注释保持项目现有密度：只解释「为什么」，不复述代码。

## 2. 构建校验

```bash
go build ./...
go test ./pkg/artifacts/
```

## 3. 手工验证

1. 指向 Garage 启动服务（`PICOTERA_S3_ENDPOINT` 为明文 HTTP 的 Garage 地址，`PICOTERA_S3_USE_SSL=false`）。
2. 打一次网关请求，确认日志里不再出现 `artifact: upload failed`。
3. 在 dashboard 请求详情页展开 request / response artifact，确认能正常拉取并解压渲染（验证预签名 GET 与 `Content-Encoding: zstd` 未受影响）。
4. 回归本地 docker-compose 的 MinIO：同样跑一次请求，确认上传与读取仍正常。
