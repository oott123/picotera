// 渠道 x 模型 熔断

// 记录错误次数
picotera.hooks.afterUpstreamError.tap('circuit-breaking', function (ctx, input) {
  const provider = ctx.provider
  const model = ctx.routedModel.name

  const key = `fail:${provider.id}:${model}`

  let errCount = picotera.kv.get(key) ?? 0
  errCount += 1
  console.log(`渠道 ${provider.name} 模型 ${model} 累计了 ${errCount} 次错误`)
  picotera.kv.setex(key, 60, errCount)

  return input
})

picotera.hooks.beforeRequest.tap('circuit-breaking', function (ctx, input) {
  const provider = ctx.provider
  const model = ctx.routedModel.name

  const key = `fail:${provider.id}:${model}`

  const errCount = picotera.kv.get(key) ?? 0
  if (errCount >= 10) {
    console.log(`渠道 ${provider.name} 模型 ${model} 熔断生效中`)
    return { next: true, delay: 0 }
  }

  return input
})
