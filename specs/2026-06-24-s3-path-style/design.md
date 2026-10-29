# 设计:S3 path style 配置

## 背景

artifact sink(`pkg/artifacts/sink.go`)用 minio-go v7 构造两个客户端:

- `client`——上传对象(`PutObject`),endpoint 为 `PICOTERA_S3_ENDPOINT`。
- `urlSignerClient`——签发 presigned GET URL,endpoint 为 `PICOTERA_S3_PUBLIC_URL` 的 host。

两个客户端当前都用 `minio.Options{}` 的默认 `BucketLookup`(= `BucketLookupAuto`),无法被
operator 控制。需要新增 `PICOTERA_S3_PATH_STYLE` 来强制 path style 的开关。

## minio BucketLookup 映射

minio-go v7.0.100 的 `BucketLookupType`:

| 取值 | 寻址方式 | 请求形态 |
| --- | --- | --- |
| `BucketLookupAuto` | 自动探测 | 由 endpoint 形态决定 |
| `BucketLookupPath` | path style | `https://host/bucket/key`,签名 Host = `host` |
| `BucketLookupDNS` | virtual-hosted | `https://bucket.host/key`,签名 Host = `bucket.host` |

三态配置 `PathStyle *bool` 映射:

```
nil    -> BucketLookupAuto
true   -> BucketLookupPath
false  -> BucketLookupDNS
```

## 配置

在 `configx.S3Config` 增加字段:

```go
PathStyle *bool `mapstructure:"path_style"`
```

- 环境变量 `PICOTERA_S3_PATH_STYLE`,经 `bindEnvs` 自动绑定。
- 不设默认值:未设置时 `viper.Unmarshal` 保持指针为 `nil`(已验证 viper 能把
  `"true"`/`"false"` 字符串正确解码进 `*bool`,且未设置时为 `nil`)。

## sink 改动

新增辅助函数把三态映射成 `minio.BucketLookupType`,在两个 `minio.New(...)` 调用的
`Options` 里都填上 `BucketLookup`,使上传与 presign 都遵循同一设置。

## public URL 拼接

`PresignedGet` 现有逻辑:用 `urlSignerClient` 签名 → 当 `publicURL` 非空时,把 `u.Scheme`/
`u.Host` 覆盖为 public URL 的 scheme/host,并把 public URL 的 path 作为前缀拼到 `u.Path` 前。

这套逻辑配合 `urlSignerClient` 的 `BucketLookup` 后,天然满足需求,无需为 path style 关闭单独
加分支:

- **path style 开启**(`BucketLookupPath`):minio 签出 `host/bucket/key`,`u.Path` 已含
  `/bucket/key`;覆盖 host 为 public host、拼上 public path 前缀 → `{public_url}/bucket/key`。
  bucket 在 path 中,符合 path style 预期。
- **path style 关闭**(`BucketLookupDNS`):minio 签出 `bucket.host/key`,`u.Path` 仅为
  `/key`(virtual-hosted 不把 bucket 放进 path);`u.Host` 被硬覆盖为 public host,`bucket.`
  子域被去掉 → `{public_url}/key`。bucket 既不在 host 也不在 path,符合需求最后一条。
- `publicURL` 为空时走 `return u.String()` 原样返回,不涉及拼接,不受影响。

> 说明:virtual-hosted 模式下签名 Host 为 `bucket.public_host`,而最终交付 URL 的 host 为
> `public_host`。这是 operator 显式要求的形态——通常对应 CDN/反向代理已把 public host 直接
> 映射到目标 bucket。host 与签名是否严格匹配由 operator 的基础设施负责,网关只按要求产出
> URL 形态。

## 文档清理

README 与 `docs/deploy/docker-compose.yaml` 里现存的 `PICOTERA_S3_FORCE_PATH_STYLE`(从未接线)
替换为 `PICOTERA_S3_PATH_STYLE`。

## 测试

`pkg/artifacts/` 现无单元测试(依赖外部 S3,无测试 harness)。本次为纯 `BucketLookupType`
映射函数补一个表驱动单测覆盖三态;presign/上传路径不引入新测试,与现状一致。
