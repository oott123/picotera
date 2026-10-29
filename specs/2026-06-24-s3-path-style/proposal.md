# 需求:PICOTERA_S3_PATH_STYLE

增加一个 `PICOTERA_S3_PATH_STYLE` 配置项,用于控制 S3 的 path style 寻址方式:

- 如果设置了该字段,上传对象到 S3 时,强制开启或关闭 path style。
- 签名 public URL(presigned GET)时,也遵循同一设置。
- 如果 path style 是关闭的(virtual-hosted style),那么在拼接 public URL 时,如果显式设置了
  public URL,不要把 bucket 拼到 host name 和 path 里面去——最终 URL 形如
  `{public_url}/{key}`,bucket 不出现在 host 或 path 中。

## 补充说明(规划阶段确认)

- 该字段为三态:未设置 = 沿用 minio 自动探测(`BucketLookupAuto`);`true` = 强制 path style
  (`BucketLookupPath`);`false` = 强制 virtual-hosted style(`BucketLookupDNS`)。
- 现有 README 与 `docs/deploy/docker-compose.yaml` 中出现的 `PICOTERA_S3_FORCE_PATH_STYLE`
  从未在配置结构体中接线,是失效文档。本次以用户指定的 `PICOTERA_S3_PATH_STYLE` 为准,并清理
  掉旧的 `FORCE_PATH_STYLE` 文档引用。
