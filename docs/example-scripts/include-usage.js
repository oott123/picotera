// 为 openai chat completions 请求添加 include_usage 字段
picotera.hooks.rewriteRequest.tap('add-include-usage', function (ctx, pending) {
  if (ctx.format !== "openaiChatCompletions") return // 这样会仅对 unified 路由生效，gateway 路由不会有这个字段

  if (!pending.body) return

  if (!pending.body.stream_options) {
    pending.body.stream_options = { include_usage: true }
    console.log('Added stream_options to body.')
  } else if (!pending.body.stream_options.include_usage) {
    pending.body.stream_options.include_usage = true
    console.log('Added include_usage to stream options.')
  }

  return pending
})