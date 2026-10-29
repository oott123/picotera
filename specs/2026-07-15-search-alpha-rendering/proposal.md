# Search Alpha 输出渲染

## 原始需求

前端的渲染，支持一下 OpenAI 的 search alpha 接口。JSON 里面，渲染 `output` 这个字段，是个字符串，长这样：

```
Visitor Information | Singapore Botanic Gardens (https://sbg.nparks.gov.sg/visit/general-info/)
citeturn0search0 [wordlim: 200] Published: 5 months ago; Crawled: 5 days ago; Map of the Singapore Botanic Gardens ... Admission charges only apply for entry into the National Orchid Garden. ... Opening Hours ... All indoor attractions, including the Heritage Museum, Botanical Art Gallery and Centre for Ethnobotany, are strictly out of bounds for pets.
--------------------------------------------------------------------------------
Singapore Botanic Gardens | Homepage (https://www.nparks.gov.sg/sbg/)
citeturn0search1 [wordlim: 200] Published: 4 months ago; Crawled: today; Event 17 July 2025 - 30 November 2026 Roots of Knowledge ... Today, the Gardens is an important botanical institute ... Opening Hours
```

其中正文里可能包含 Markdown。当前系统只支持聊天类响应（OpenAI Chat / Responses、Anthropic、Gemini）的渲染，没有 search alpha 的支持，需要新加。

> 注：上面样例里的 `citeturn0search0` 是粘贴时特殊字符被过滤后的样子。引用标记实际使用私有区 Unicode 定界符，形如 `U+E200` `cite` `U+E202` `turn0search15` `U+E201`（即 `citeturn0search15`）。`[wordlim: …]` 及 `Published` / `Crawled` 等元信息出现在闭合定界符 `U+E201` **之后**。

## 已确认的设计决策

- **出现位置**：有一个专门的搜索 endpoint，其响应体是一个 JSON，形如 `{"encrypted_output": "gAAAAA…", "output": "<上面那段字符串>"}`。渲染其中明文的 `output` 字段（`encrypted_output` 是 Fernet 密文，忽略）。不是嵌在某个对话格式里的工具结果。
- **结果切分**：结果之间的横线分隔符**不可靠**（真实数据里 18 条结果只有 12 条横线，且横线经常直接粘在上一条正文末尾、不独立成行）。因此按每条结果的**引用标记**切分，而不是按横线。
- **引用标记**：真实格式为私有区定界符包裹 `U+E200` `cite` `U+E202` `turn0search15` `U+E201`。**提取成徽章**展示，文案 `Cite: turn0search15`（一条引用含多个 `U+E202turn…` 段时用逗号连接）；私有区定界符不进入正文。
- **元信息**（`Published` / `Crawled` / `wordlim`）：从正文里**拆出来，作为独立徽章展示**；正文只保留实际摘要内容（含其中的 Markdown）。
- **正文**：按 Markdown 渲染。
- **渲染入口**：两处都渲染——请求详情页的「对话」Tab，以及「原始响应 → 渲染」子视图。两处共用同一个来源卡片组件。
- **范围**：纯前端改动，不涉及后端 / OpenAPI / 数据库。
