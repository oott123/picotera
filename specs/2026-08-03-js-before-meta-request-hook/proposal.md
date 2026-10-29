# beforeMetaRequest 脚本 Hook

增加一个脚本 hook 点，叫 `beforeMetaRequest` 吧，执行时机在 `sortProviders` 之后，准备尝试第一个上游之前。输入输出是 `undefined` 或者 `ResponseShape`，如果是 `undefined` 则正常执行后续的流程，如果是 `ResponseShape` 则用脚本返回的值作为响应返回给客户端。`sortProviders` 返回的上游是空数组的时候也执行这个回调。`ResponseShape` 里要包括响应状态码、Body（默认 json）和 headers。如果走这条路径快速响应，后续没填写的一些字段比如上游 id、tokens，都当作是没有，和报错类似处理吧，完成原因如果 status 是 2xx 也算正常结束，否则算内部错误。

## 澄清补充

- **`ResponseShape` 增加 `tokens` 字段**，可以填 tokens 数据；填了就记到请求记录上，没填仍按「没有」处理。
- **不在 `ctx` 上暴露候选渠道信息**（既不暴露 `ctx.candidates`，也不暴露候选数量）。脚本若需要在 `beforeMetaRequest` 里判断「无可用渠道」，自行在 `sortProviders` 的 tap 中把状态挂到 `ctx` 的自定义字段上（`ctx` 的自定义字段在整个会话内保留）。
