// 按请求头分流到指定渠道（服务端部分）
//
// 配合 header-routing.frontend.js 使用：把那个文件的内容粘贴到同一个脚本的「前端」标签页，
// 然后在控制台侧边栏「扩展」分组的「请求头分流」页面里用连线配置规则。
//
// 规则存在 KV 键 `header-routing` 里，值为 { "rules": { "<请求头取值>": [渠道 ID, ...] } }。
// 请求带上 `X-Picotera-Route: <取值>` 时，只保留连到这个取值的渠道；一条线都没连的取值会让请求没有可用渠道。
// 没有这个请求头，或者取值没有对应的规则时，不做任何干预。

picotera.hooks.sortProviders.tap('header-routing', function (ctx, candidates) {
  // ctx.request.headers 的键是小写的
  const values = ctx.request.headers['x-picotera-route']
  if (!values || values.length === 0) return candidates

  const config = picotera.kv.get('header-routing')
  const providerIds = config?.rules?.[values[0]]
  if (!providerIds) return candidates

  return candidates.filter(c => providerIds.includes(c.provider.id))
})
