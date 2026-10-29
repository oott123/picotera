# 原始需求

`fixtures/zai-stream-error.sse` 识别这个文件中的 SSE 响应，把它也当成一种流式错误记录。

补充需求：`fixtures/zai-context-window-error.sse` 也需要加入同一类识别。

补充要求：不要遍历 `choices`；只用 `gjson` 直接读取 `choices[0].finish_reason`（实现路径为 `choices.0.finish_reason`）来判断该类错误。
