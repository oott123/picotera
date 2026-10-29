# 执行计划:PICOTERA_S3_PATH_STYLE

## 1. 配置字段

`pkg/configx/configx.go`:

- 在 `S3Config` 增加 `PathStyle *bool` 字段,tag `mapstructure:"path_style"`。
- 不在 `Parse()` 中为它设 `viper.SetDefault`(保持未设置时为 `nil`)。

`bindEnvs` 已对非 struct 字段统一 `BindEnv`,指针字段会走 default 分支自动绑定
`s3.path_style`,无需额外改动。

## 2. sink 应用 BucketLookup

`pkg/artifacts/sink.go`:

- 新增辅助函数:

  ```go
  func bucketLookup(pathStyle *bool) minio.BucketLookupType {
      if pathStyle == nil {
          return minio.BucketLookupAuto
      }
      if *pathStyle {
          return minio.BucketLookupPath
      }
      return minio.BucketLookupDNS
  }
  ```

- 在 `NewSink` 里两处 `minio.New(...)`(`client` 与 `urlSignerClient`)的 `minio.Options`
  中都加上 `BucketLookup: bucketLookup(cfg.PathStyle)`。

`PresignedGet` 的 host/path 拼接逻辑保持不变(见 design.md 论证,已天然满足需求)。

## 3. 文档

- `README.md`:把 `PICOTERA_S3_FORCE_PATH_STYLE=true` 改为 `PICOTERA_S3_PATH_STYLE=true`。
- `docs/deploy/docker-compose.yaml`:把 `PICOTERA_S3_FORCE_PATH_STYLE: true` 改为
  `PICOTERA_S3_PATH_STYLE: "true"`。

## 4. 测试

`pkg/artifacts/sink_test.go`(新建):对 `bucketLookup` 做表驱动单测——`nil → Auto`、
`true → Path`、`false → DNS`。

## 5. 验证

```bash
go build ./...
go test ./pkg/artifacts/ ./pkg/configx/
```
