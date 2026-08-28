// 获取模型列表时不传 anthropic 特征头
// 对 CLIProxyAPI 之类识别客户端返回不同模型的程序应该有用
// 用法：在渠道上添加标注 `models-no-anthropic-headers` = `yes`
picotera.hooks.rewriteRequest.tap('fetch-models-no-anthropic-header', function (ctx, pending) {
  if (ctx.endpointType !== 'fetchModels') return
  if (ctx.provider.annotations['models-no-anthropic-headers'] !== 'yes') return
  delete pending.headers['anthropic-version']
  return pending
})
