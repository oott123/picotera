# 执行计划

路径均相对 `dashboard/src/`。

1. 新建 `lib/toolFormat.ts`，按 design.md「工具格式化模块」实现 `TOOL_ARG_MAX_CHARS`、`ToolToken`、
   `ToolResultSegment`、`formatToolValue`、`toolCallTokens`、`formatToolCall`、`toolResultSegments`、
   `toolResultTokens`、`formatToolResult`。不 import 任何模块。
2. 修改 `composables/conversation.ts`：
   1. 按 design.md「格式识别」：`hasAnthropicContentBlocks` 替换为 `hasAnthropicFeatures(root, messages)`；
      `hasOpenAIChatFeatures` 去掉 `role === 'system'`，加上 `image_url` / `input_audio` / `file` 内容部件；
      `detectFormat` 请求分支改为 gemini → openaiResponses → 空 `messages` 为 `null` → anthropic → openaiChat →
      兜底 openaiChat。
   2. `thinking` 部件的 `text` 改为 `string | null`；新增 `pushHiddenThinking(parts)` 推入 `text: null`。
      `pushThinking` 保持"空串不推"的行为，继续用于 `reasoning_content` / `reasoning` / Gemini `thought` 这类可选字段。
   3. 新增导出类型 `ToolResultContent`；`toolResult` 部件新增 `content: ToolResultContent[] | null`。
   4. 从 `parseAnthropicContent` 抽出 `anthropicImage(block): ImageSource | null`；新增
      `toolResultContent(output): ToolResultContent[] | null`（基于 `toolResultSegments`，图片块经
      `anthropicImage` / `imageFromUrl` 转 `media`，标签用 `[image]`）。
   5. 新增 `toolResultPart(id, name, output, isError)` 统一构造 toolResult（内部调用 `toolResultContent`），
      替换 OpenAI Chat、Responses、Anthropic、Gemini 四处手写的 toolResult 对象。
   6. `parseAnthropicContent`：`thinking` 空明文 → `pushHiddenThinking`；`redacted_thinking` → `pushHiddenThinking`；
      `server_tool_use` / `mcp_tool_use` 与 `tool_use` 同分支；`type` 以 `_tool_result` 结尾的块与 `tool_result` 同分支。
   7. `parseAnthropicRequest`：`role === 'system'` 的消息以 system 角色保留。
   8. 用 `responseItemParts(item)` 替换 `parseOpenAIResponseItem` 与 `parseOpenAIResponseMessage`，覆盖
      `message`、`reasoning`、`function_call`、`custom_tool_call`、`web_search_call`、`function_call_output`、
      `custom_tool_call_output`、`image_generation_call`；`parseOpenAIResponsesRequest` 与
      `parseOpenAIResponsesResponse` 改为调用它，响应侧 role 为 assistant 的 parts 合并进同一条助手消息。
   9. 新增导出 `linkToolResultNames(messages)`。
3. 修改 `components/ConversationArtifactView.vue`：`merged` 改为对拼接后的数组调用 `linkToolResultNames`。
4. 修改 `ui/icons/paths.ts`：从 `@tabler/icons-vue` 引入 `IconTool`、`IconCornerDownRight`，在 `IconName` 联合类型与
   映射表中登记 `tool`、`corner-down-right`。
5. 新建 `components/ConversationToolPart.vue`：props `part`（toolCall | toolResult），emit `toggle`；按 design.md
   「工具块」实现摘要行（`toolCallTokens` / `toolResultTokens` 分色渲染、错误标记）与展开区（逐参数 / 逐段渲染，
   `<pre>`、`JsonViewer`、`ImageAttachment`、媒体标签）。
6. 修改 `components/ConversationView.vue`：
   - toolCall / toolResult 分支替换为 `ConversationToolPart`，`@toggle="scheduleMeasure"`；删除 `toolTitle`。
   - thinking 分支：`part.text === null` 时渲染不可展开的"思考过程（未返回明文）"行，否则保持现有 `<details>`。
7. 检查其它读取 `ConversationPart` 的代码（`ResponseArtifactView.vue` 的 `collectConversationImages` 调用、
   全局搜索 `kind === 'thinking'` / `kind === 'toolResult'`），适配 `text: string | null` 与新增的 `content` 字段。
8. 更新 `dashboard/CLAUDE.md`「Layout」：加一条 `src/lib/` —— 与 Vue 无关的纯函数模块，列出 `toolFormat.ts`
   （工具调用 / 结果的 Python 风格单行格式化，供多处复用）。
9. 验证：
   - `pnpm --dir dashboard type-check`、`pnpm --dir dashboard lint`、`pnpm --dir dashboard format`、`pnpm --dir dashboard build`。
   - 在仓库外写临时脚本，用 `dashboard/node_modules/.bin/jiti`（`createJiti` 配 `alias: { '@': <dashboard/src> }`）
     加载 `lib/toolFormat.ts` 与 `composables/conversation.ts`，确认：
     - `formatToolCall('Bash', { command: 'ls' })` 为 `Bash(command="ls")`；200 字符的参数输出 128 个原文字符加 `…"`；
       换行输出为 `\n`；`true` / `null` 输出 `True` / `None`；非标识符键带引号；字符串输入输出为位置参数；`null` 输入为 `Bash()`。
     - `formatToolResult('Bash', [{ type: 'text', text: 'a' }, { type: 'image', source: {} }])` 为 `Bash → "a\n[image]"`。
     - 本地 MinIO 中 `/api/unified/v1/messages` 的 Claude Code 请求样本（`messages` 含 `role: "system"`）被识别为
       anthropic，解析结果含 thinking、toolCall、toolResult 部件且不含 `[thinking]` / `[tool_use]` / `[tool_result]` 媒体标签，
       system 消息被保留，toolResult 经 `linkToolResultNames` 后带上 `Bash` 等工具名。
     - `/backend-api/codex/responses` 请求样本解析出 `custom_tool_call`、`custom_tool_call_output`、`reasoning`
       （`text: null`）对应的部件。
     - 一个带 `role: "system"` 消息和 `tool_calls` 的 OpenAI Chat 请求识别为 openaiChat；根级无 `system`、
       消息只有 `role: "system"` / `user` 纯文本的请求识别为 openaiChat 并解析出两条消息；根级无 `system`、
       `messages` 里有 `role: "system"` 和 `thinking` 块的请求识别为 anthropic。
   - `mise run web` 打开上述两类请求的对话页，确认思考块、未返回明文的思考行、工具调用摘要与分色、结果摘要与工具名、
     展开后的逐参数 / 逐段显示、错误结果样式、折叠高度重新测量均正常。
