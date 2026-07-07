# 网关 CORS 头去重

## 需求

当上游响应已经带有 `Access-Control-Allow-Origin` 和 `Access-Control-Expose-Headers` 时，网关会把它们与自身写入的 `*` 拼成 `*, *` 这样的格式。这不对——下游响应里这两个头应当直接是 `*`（单值），而不是 `*, *`。
