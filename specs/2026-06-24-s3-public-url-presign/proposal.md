# 需求:S3 public URL 预签名修复

修复 artifact presigned GET URL 在 S3 virtual-hosted style 下的签名不匹配问题。

要求:

- `PICOTERA_S3_PUBLIC_URL` 的配置格式保持不变,可以配置为已经包含 bucket 的 public host,例如 `https://tokens-artifacts.tos-s3-cn-beijing.volces.com`。
- 当 `PICOTERA_S3_PATH_STYLE=false` 时,生成的 public URL 不再把 bucket name 拼接到 public URL 的域名或 path 中。
- 最终签名必须基于返回给浏览器的 URL 形态,避免签名时 host 与实际请求 host 不一致。
- 上传过程不受影响,仍然使用 `PICOTERA_S3_ENDPOINT`、`PICOTERA_S3_BUCKET` 和原有 minio client 上传对象。
