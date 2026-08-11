# 修复 artifact 上传到 Garage 失败

## 原始需求

上传到 garage 时，报错

```
level=warning msg="artifact: upload failed" error="Bad request: content-encoding does not contain aws-chunked for STREAMING-*-PAYLOAD" key=artifacts/2026-08-11/d9th47d9j43g008dtusg.response.json.zst
```

看看怎么修。

## 澄清后的补充信息

- 修复范围：只关闭 minio-go 的分块流式签名（改用 `UNSIGNED-PAYLOAD`），不额外引入 `Content-MD5` 完整性校验。
- 不需要补 httptest 级别的回归测试，只改代码，靠手工对 Garage 验证。
