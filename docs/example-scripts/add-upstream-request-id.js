// 给发往上游的请求添加 PicoTera 内部请求 ID
// 上游设置 request-id-header 标注即生效
picotera.hooks.rewriteRequest.tap('add-upstream-request-id', function (ctx, pending) {
  const headerName = ctx.provider.annotations['request-id-header']
  if (!headerName || !ctx.upstreamRequest) return pending

  pending.headers[headerName] = [ctx.upstreamRequest.id]

  return pending
})
