// 对所有 deepseek-v4 开头的模型，使用 axonhub deepseek 驱动重写
// beforeTransform 仅在 /api/picotera/v1/ 下生效
picotera.hooks.beforeTransform.tap('deepseek', function (ctx, input) {
  const modelName = ctx.routedModel?.name ?? ''
  if (modelName.startsWith('deepseek-v4')) {
    input.type = 'deepseek'
  }
  return input
})
