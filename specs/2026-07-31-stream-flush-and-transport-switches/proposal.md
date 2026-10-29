# Proposal

最近总有些请求，报错 http2: timeout awaiting response headers ，之前已经调查过了，你可以看看 specs 目录最新的一条。现在的问题是，缓解措施不太有效，我已经开了 PICOTERA_GATEWAY_DISABLE_KEEP_ALIVES 了，还是经常能看见，似乎并发的时候特别明显。要不我们试着关闭 http2 吧。另外就是代理 transport 也别复用了，每次都开个新的。还有就是我上游是另一个 picotera，而且是 transform 路径，是不是转换之后写出去的时候，没有 flush ？或者没用 chunked encoding ？这几个你都帮我检查下，然后看看还有可能是为啥，都尝试修修。

## 补充背景

- 前一条 spec：`specs/2026-07-30-h2-conn-reuse-timeout/`（连接隔离 + instrumentation + DISABLE_KEEP_ALIVES 开关），其实现已在工作区（未提交），是本 spec 的基线。
- 上游是另一个 picotera 实例，走 transform（unified/llmbridge 桥接）路径。
- 用户已在上游 picotera 侧核实：超时对应的请求，upstream 行**和 meta 行**都记录了 body chunk 写出——上游不仅收到了它上游的响应，也已把转换后的响应写给了下游。
- 时间戳判据已跑：B 的首个 meta chunk **落在 A 的 91s 窗口内**。
- 并发规模只有约 4，并不大；B 前面的反向代理是用户长期使用的成熟网关，从未在别处遇到过这个问题。
- 用户当前的怀疑方向：A 侧 HTTP 客户端实例的复用或冲突。
