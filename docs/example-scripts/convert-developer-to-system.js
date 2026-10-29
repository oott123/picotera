// 将 OpenAI Chat Completions 格式中 role=developer 的消息自动改写为 role=system
// 适用于上游 provider 不支持 developer role 的场景
// 需要手动设置渠道或模型标注 rewrite-developer-role 为 yes 才能生效
picotera.hooks.rewriteRequest.tap('responses-developer-to-system', function (ctx, pending) {
  if (ctx.annotations['rewrite-developer-role'] !== 'yes') return
  if (ctx.format !== 'openaiChatCompletions') return

  var body = pending.body
  if (!body) return

  var messages = body.messages
  if (!messages || !Array.isArray(messages)) return

  var changed = false
  for (var i = 0; i < messages.length; i++) {
    var item = messages[i]
    if (item && item.role === 'developer') {
      item.role = 'system'
      if (!changed) {
        changed = true
        console.log('"developer" role detected and converted to "system".', ctx.format, ctx.sourceFormat)
      }
    }
  }

  return pending
})