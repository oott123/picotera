# 端点栏宽度限制 + 端点 path 模板化

## 原始需求

1. 给请求页面（RequestsView）的"端点"栏加一个最大宽度，比"用户消息"栏稍微窄一点。
2. 设置（写入）这一栏的值时，如果是 gemini 这种 path 里带有模型名或其它 path var 的情况，要保存为占位符形式，例如 `/v1beta/models/{model}:generateContent`，而不是把具体模型名写进去的 `/v1beta/models/gemini-2.5-flash:generateContent`。unified 路由同理。

## 补充说明（澄清后确认）

- 经核实，path-based 网关（普通 endpoint 路由）记录到 `request.endpoint_path` 的本来就是 endpoint 配置里的模式（含 `{model}` 占位符），不带具体模型名，无需改动。
- 唯一会把具体模型名写进 `endpoint_path` 的是 **unified 路由的 meta 行**：它用 `r.URL.Path`（实际请求 path）作为虚拟 endpoint 的 path。unified 的 upstream 行用的是 provider_endpoint 的模式，已经正确。
- unified 模板化后**保留 `/api/unified` 前缀**，即 `/api/unified/v1beta/models/{model}:generateContent`，与端点筛选下拉（label 列表）展示的值保持一致。
