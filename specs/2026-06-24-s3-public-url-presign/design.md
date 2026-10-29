# 设计:S3 public URL 预签名修复

## 背景

artifact sink 当前为上传和 presigned GET 分别创建 minio client。presigned GET client 使用
`PICOTERA_S3_PUBLIC_URL` 的 host 作为 endpoint,然后在 `PresignedGet` 里再次把签出的 URL
改写成 `PICOTERA_S3_PUBLIC_URL` 的 scheme/host/path。

当配置为:

```text
PICOTERA_S3_BUCKET=tokens-artifacts
PICOTERA_S3_PATH_STYLE=false
PICOTERA_S3_PUBLIC_URL=https://tokens-artifacts.tos-s3-cn-beijing.volces.com
```

minio-go 会按 virtual-hosted style 在签名阶段把 bucket 拼到 signer endpoint 前面,得到
`tokens-artifacts.tokens-artifacts.tos-s3-cn-beijing.volces.com`。随后代码把 URL host 改回
`tokens-artifacts.tos-s3-cn-beijing.volces.com`。SigV4 的 `host` 是 signed header,host 被改写
后签名必然不匹配。

## 行为

上传路径保持不变:

- `PutObject` 继续使用 `PICOTERA_S3_ENDPOINT` 创建的 minio client。
- `PICOTERA_S3_BUCKET` 继续作为上传 bucket。
- `PICOTERA_S3_PATH_STYLE` 继续映射到上传 client 的 `BucketLookup`。

presigned GET 路径按配置分流:

- `PICOTERA_S3_PUBLIC_URL` 为空时,继续返回 minio-go 基于 S3 endpoint 签出的 URL。
- `PICOTERA_S3_PUBLIC_URL` 非空且 `PICOTERA_S3_PATH_STYLE=true` 或未设置时,保留现有 minio-go
  presign 行为和 public URL 改写行为。
- `PICOTERA_S3_PUBLIC_URL` 非空且 `PICOTERA_S3_PATH_STYLE=false` 时,不再通过 minio-go client 对
  public host 做 virtual-hosted bucket 拼接。代码先构造最终 public URL 形态的 `http.Request`,
  再调用 `github.com/minio/minio-go/v7/pkg/signer.PreSignV4` 生成 presigned GET:
  - canonical host 为 `PICOTERA_S3_PUBLIC_URL` 的 host。
  - canonical path 为 `PICOTERA_S3_PUBLIC_URL` 的 path 前缀加 object key。
  - canonical query 包含 `X-Amz-Algorithm`、`X-Amz-Credential`、`X-Amz-Date`、
    `X-Amz-Expires`、`X-Amz-SignedHeaders` 和最终 `X-Amz-Signature`。
  - signed headers 为 `host`。
  - payload hash 为 `UNSIGNED-PAYLOAD`。
  - bucket name 不进入 public host 或 path。

## API

不新增管理 API,不新增配置项。

## 测试

新增 artifact sink 单元测试覆盖:

- `bucketLookup` 三态映射。
- `PICOTERA_S3_PUBLIC_URL + PICOTERA_S3_PATH_STYLE=false` 的 presigned URL host/path 形态。
- 使用测试 secret 复算签名,确认签名绑定到最终 public host 和 path。
