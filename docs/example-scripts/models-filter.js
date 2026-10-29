// 拉取模型时用正则过滤模型
// 用法： annotations 设置 models-filter 为正则
picotera.hooks.rewriteProviderModels.tap('models-filter', function({ provider: { annotations }}, models) {
  if (!models) return
  const filter = annotations['models-filter']
  if (!filter) return

  const regex = new RegExp(filter, 'i')

  const newModels = []
  for (const model of models) {
    if (model.model?.match(regex) || model.upstreamModelName?.match(regex)) {
      newModels.push(model)
    }
  }
  return newModels
}, 10)
