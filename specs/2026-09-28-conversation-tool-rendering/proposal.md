# 对话页完善 thinking / tool_use / tool_result 的渲染

“对话”页面对 thinking 的渲染，似乎是 思考+工具 的时候，不会渲染成思考内容，而是一个文本的 `[thinking]` 这不太好。另外 tool_use, tool_result 最好是能更好地渲染，比如渲染成 python named args 格式吧： `Bash(command="xxx")` 这样？每个参数不要超过 128 个字符好了，多出来的就省略号。tool result 也类似。总之把这个渲染搞得完善点。

## 补充

- 工具调用 / 工具结果的格式化要抽成独立函数，后面别的地方也要用。
- `role: "system"` 的消息现在 Anthropic 也会发，不应再作为识别 OpenAI Chat 的依据。
