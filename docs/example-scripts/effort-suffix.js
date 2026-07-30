picotera.hooks.rewriteModel.tap('check-effort-suffix', function (ctx, m) {
  const hasSuffix = m.match(/^(.*):(none|minimal|low|medium|high|xhigh|max)$/)
  if (!hasSuffix) {
    return m
  }

  const modelName = hasSuffix[1]
  const suffix = hasSuffix[2]
  ctx.rewriteReasoningSuffix = suffix
  return modelName
})

picotera.hooks.rewriteRequest.tap('add-effort', function (ctx, pending) {
  if (!ctx.rewriteReasoningSuffix) return

  const effort = ctx.rewriteReasoningSuffix

  switch (ctx.format) {
    case 'openaiResponses': {
      if ('reasoning' in ctx.body) {
        ctx.body.reasoning.effort = effort
      } else {
        ctx.body.reasoning = { effort }
      }
      break
    }
    case 'openaiChatCompletions': {
      ctx.body.reasoning_effort = effort
      break
    }
    case 'anthropicMessages':
    case 'anthropicCountTokens': {
      if ('output_config' in ctx.body) {
        ctx.body.output_config.effort = effort
      } else {
        ctx.body.output_config = { effort }
      }
      break
    }
    case 'geminiGenerateContent':
    case 'geminiStreamGenerateContent': {
      if ('generation_config' in ctx.body) {
        ctx.body.generation_config.thinking_level = effort
      } else {
        ctx.body.generation_config = { thinking_level: effort }
      }
      break
    }
    default:
      console.warn(`effort detected but format ${ctx.format} (transformed from ${ctx.sourceFormat}) is not supported`)
  }

  return
})
