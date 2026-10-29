# 对 400 上游错误的默认透传

为特定上游错误做默认处理：当上游响应状态码恰好为 `400` 时，网关默认停止继续尝试其他 provider，直接把该上游响应透传回请求方。

这一行为等价于 `afterUpstreamError` hook 在该场景下默认 `break=true`，并且可以被该 hook 改写——脚本可以读取到默认已是 `break=true`，并通过返回 `{ break: false }` 让网关继续尝试后续 provider，或返回自定义的 `statusCode` / `message` 来覆盖透传内容。

仅针对状态码恰好等于 `400` 的情况，不包含其他 4xx。仅适用于响应尚未开始流式写出（`streamed=false`）的情况；in-stream SSE 错误已经开始流式写出，`break` 本就被忽略，不受影响。
