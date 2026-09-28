# 设计：对话页的思考与工具调用渲染

## 现状与根因

对话页由 `ConversationArtifactView.vue` 解析请求 / 响应体，交给 `ConversationView.vue` 渲染，解析逻辑在
`dashboard/src/composables/conversation.ts`。

`[thinking]` 的来源：新版 Claude Code 发出的 Anthropic 请求会在 `messages` 里插入 `role: "system"` 的消息
（环境更新、`<total_tokens>` 提示、hook 附加上下文等）。`detectFormat` 对请求先跑 `hasOpenAIChatFeatures`，
它看到 `role === 'system'` 就判定为 OpenAI Chat，之后 `parseOpenAIContentParts` 不认识 `thinking` /
`tool_use` / `tool_result` 块，一律走 `pushMedia(parts, part.type)`，渲染成 `[thinking]`、`[tool_use]`、
`[tool_result]` 这样的标签。只有思考、没有工具的对话通常不带这些 system 消息，所以看起来是"思考 + 工具"时才出问题。

除此之外还有几处缺失：

- Anthropic `messages` 里的 `role: "system"` 消息被直接丢弃；`redacted_thinking`、只有签名没有明文的 `thinking`
  （`thinking: ""`）不显示；`server_tool_use` / `web_search_tool_result` 等服务端工具块显示成标签。
- OpenAI Responses 的 `reasoning`（请求 `input` 里只在响应侧处理了 summary）、`custom_tool_call`、
  `custom_tool_call_output`、`web_search_call` 被丢弃。Codex 的工具调用全部是 `custom_tool_call`，因此对话页里看不到。
- 工具调用只显示工具名，工具结果只显示"工具结果"，展开后是 JSON 树；看不到参数摘要，结果也不知道对应哪个调用。

## 格式识别

`role: "system"` 消息现在两种格式都会出现，不再作为 OpenAI Chat 的特征。两个特征函数只收各自独有的信号：

- `hasAnthropicFeatures(root, messages)`：根级有 `system` 字段；或任一消息的内容块 `type` 属于 `tool_use`、
  `tool_result`、`thinking`、`redacted_thinking`、`server_tool_use`、`mcp_tool_use`、`image`、`document`，
  或以 `_tool_result` 结尾。取代原 `hasAnthropicContentBlocks`。
- `hasOpenAIChatFeatures(messages)`：任一消息 `role` 为 `tool` / `developer`；或带 `tool_calls` 数组；或内容部件
  `type` 属于 `image_url`、`input_audio`、`file`。去掉原来的 `role === 'system'` 判断。

`detectFormat` 的请求分支：

1. `Array.isArray(root.contents)` → `gemini`（不变）
2. `'input' in root` → `openaiResponses`（不变）
3. `messages` 为空 → `null`
4. `hasAnthropicFeatures` → `anthropic`
5. `hasOpenAIChatFeatures` → `openaiChat`
6. 其余 → `openaiChat`

第 6 步兜底的是两边都没有独有特征的请求：消息只有 `system` / `user` / `assistant`，内容只有字符串或
`{type: "text"}` 部件。两个解析器对这种输入解析出的部件完全相同，所以固定交给 `openaiChat`。原来这类请求
（比如只有 user / assistant 纯文本）判为 `null`、显示"无法解析"，改后可以正常显示。两边特征同时出现时以
Anthropic 为准。

## 工具格式化模块 `dashboard/src/lib/toolFormat.ts`

纯函数模块，不依赖 Vue、不 import 任何 `@/` 路径，输入是任意 JSON 值。对话页用它，后续其它页面直接复用。

```ts
export const TOOL_ARG_MAX_CHARS = 128

export type ToolToken =
  | { kind: 'name'; text: string }   // 工具名
  | { kind: 'key'; text: string }    // 参数名
  | { kind: 'value'; text: string }  // 参数值 / 结果值
  | { kind: 'punct'; text: string }  // ( ) = , →

export type ToolResultSegment =
  | { kind: 'text'; text: string }
  | { kind: 'block'; block: Record<string, unknown> }

export function formatToolValue(value: unknown, maxChars?: number): string
export function toolCallTokens(name: string, input: unknown, maxChars?: number): ToolToken[]
export function formatToolCall(name: string, input: unknown, maxChars?: number): string
export function toolResultSegments(output: unknown): ToolResultSegment[] | null
export function toolResultTokens(name: string | null, output: unknown, maxChars?: number): ToolToken[]
export function formatToolResult(name: string | null, output: unknown, maxChars?: number): string
```

`maxChars` 默认 `TOOL_ARG_MAX_CHARS`。`formatToolCall` / `formatToolResult` 就是对应 tokens 的 `text` 拼接；
tokens 版本供需要分色渲染的地方使用。

### `formatToolValue`：Python 字面量风格

| 值 | 输出 |
|---|---|
| 字符串 | `"..."`，转义 `\\`、`"`、`\n`、`\r`、`\t`，其余 U+0000–U+001F 与 U+007F 输出为 `\xNN`；原文超过 `maxChars` 个字符时截取前 `maxChars` 个，在收尾引号前加 `…`，如 `"ls -la …"` |
| 数字 | `String(value)` |
| 布尔 | `True` / `False` |
| `null` / `undefined` | `None` |
| 数组 | `[a, b]`，元素递归按本表输出（内部字符串不单独截断） |
| 对象 | `{"key": value}`，键按字符串输出 |

字符串的计数对象是原文字符（按码点，不含引号和转义产生的字符）；数组 / 对象的计数对象是输出文本，超过
`maxChars` 个码点时截取前 `maxChars` 个再加 `…`。数组 / 对象的拼接带预算，输出一旦超过 `maxChars` 就停止遍历，
大对象不会被完整序列化。字符串截断同样逐码点遍历、到上限即停，不对整串做 `Array.from`。

### `toolCallTokens`：`Name(key=value, ...)`

- `input` 是普通对象（非 `null`、非数组）：`Name(k1=v1, k2=v2)`，按对象自身键的顺序输出。键匹配
  `/^[\p{L}_][\p{L}\p{N}_]*$/u` 时原样输出，否则输出为带引号的字符串（`formatToolValue(key, Infinity)`）。
  每个值单独调用 `formatToolValue(value, maxChars)`，即"每个参数不超过 128 个字符"。空对象输出 `Name()`。
- `input` 是 `null` / `undefined`：`Name()`。
- 其它（字符串、数组、数字……）：作为单个位置参数，`Name(value)`。Codex 的 `custom_tool_call` 输入是原始文本，
  输出形如 `exec("const r = await tools.exec_command(…")`。

示例：`Bash(command="todou --json issue view PHB-90 | jq -r '.issue.title' && todou spec status PHB-90")`。

### 工具结果

`toolResultSegments(output)`：

- 字符串 → `[{ kind: 'text', text }]`
- 非空数组，且每个元素都是带字符串 `type` 字段的对象 → 逐个映射：`type` 为 `text` / `input_text` /
  `output_text` 且 `text` 是字符串时为 `text` 段，其余为 `block` 段（原对象）
- 其它 → `null`

`toolResultTokens(name, output)` 输出 `Name → value`；`name` 为 `null` 时输出 `→ value`。`value` 的取法：
segments 非 `null` 时把 `text` 段原文与 `block` 段的 `[${type}]` 用 `\n` 连接成一个字符串，再 `formatToolValue`；
否则直接 `formatToolValue(output)`。例：`Bash → "pulled spec v2 (3 files) into /tmp/phb-90\n  design.md\n …"`。

## 对话解析 `conversation.ts`

### 部件类型

```ts
export type ToolResultContent =
  | { kind: 'text'; text: string }
  | { kind: 'media'; mediaType: string; label: string; image: ImageSource | null }
  | { kind: 'json'; value: unknown }

export type ConversationPart =
  | { kind: 'text'; text: string }
  | { kind: 'thinking'; text: string | null }   // null：有思考块但没有明文
  | { kind: 'toolCall'; id: string | null; name: string; input: unknown }
  | {
      kind: 'toolResult'
      id: string | null
      name: string | null
      output: unknown
      content: ToolResultContent[] | null
      isError: boolean
    }
  | ...  // media、searchResults 不变
```

`toolResult.content` 由新函数 `toolResultContent(output)` 生成：调用 `toolResultSegments`，`text` 段原样保留，
`block` 段里 Anthropic 的 `type: "image"`（`source.type` 为 `base64` / `url`）与 Responses 的 `type: "input_image"`
（`image_url` 字符串）转成 `media`，其余转成 `json`；segments 为 `null` 时 `content` 为 `null`。所有构造 toolResult
的地方统一经过这个函数。Anthropic 内容块里已有的图片解析逻辑抽成 `anthropicImage(block)`，
`parseAnthropicContent` 与 `toolResultContent` 共用。

### 各格式补齐

**Anthropic**（`parseAnthropicContent` / `parseAnthropicRequest`）：

- `messages` 里 `role: "system"` 的消息按 system 角色解析，内容走 `parseAnthropicContent`（字符串和带
  `cache_control` 的 text 块数组两种形态都覆盖）。
- `thinking`：`thinking` 是非空字符串时 `text` 为原文，否则 `text: null`。`redacted_thinking` → `text: null`。
- `server_tool_use`、`mcp_tool_use` 与 `tool_use` 同样处理为 `toolCall`（取 `id` / `name` / `input`）。
- `type` 以 `_tool_result` 结尾的块（`web_search_tool_result`、`web_fetch_tool_result`、
  `code_execution_tool_result`、`mcp_tool_result` 等）与 `tool_result` 同样处理为 `toolResult`（`tool_use_id`、
  `content`、`is_error === true`）。

**OpenAI Responses**：新增 `responseItemParts(item)`，返回 `{ role, parts } | null`，请求 `input[]` 与响应
`output[]` 共用：

- `message` → 原逻辑。
- `reasoning` → 每条 `summary[].text` 一个 thinking；没有 summary 文本时一个 `text: null` 的 thinking。role 为 assistant。
- `function_call` → toolCall，`input = parseMaybeJson(arguments)`；`custom_tool_call` → toolCall，
  `input = item.input`（原始文本，不做 JSON 解析）；`web_search_call` → toolCall，`name = "web_search"`，
  `input = item.action ?? null`。三者 `id` 取 `call_id`，没有时取 `id`，role 为 assistant。
- `function_call_output` → toolResult，`output = parseMaybeJson(item.output)`；`custom_tool_call_output` →
  toolResult，`output = item.output`。role 为 tool。
- `image_generation_call` → 原逻辑。
- 其它类型（`additional_tools` 等）→ `null`。

`parseOpenAIResponsesRequest` 对每个 input 项调用它；`parseOpenAIResponsesResponse` 把 role 为 assistant 的
parts 并入同一条助手消息，其它 role 各自成消息（与现在 `message` / `function_call` 的合并方式一致）。

**OpenAI Chat**：tool 消息的 `content` 为数组时（text 部件数组）也经 `toolResultContent` 生成分段。

**Gemini**：不变，`functionResponse.response` 是对象，`content` 为 `null`。

### 结果与调用配对

新增 `export function linkToolResultNames(messages: ConversationMessage[]): ConversationMessage[]`：先收集所有
toolCall 的 `id → name`，再为 `name === null` 且 `id` 命中的 toolResult 填上名称，返回新数组，不修改入参。
`ConversationArtifactView` 对合并后的请求 + 响应消息调用一次，这样历史里的每个工具结果都显示对应调用的工具名；
找不到对应调用的结果保持 `name: null`，摘要行以 `→` 开头。

## 渲染

### 思考块

`text` 为字符串时保持现有 `<details>`。`text === null` 时渲染一个不可展开的同款行：不带箭头，文字为
"思考过程（未返回明文）"，`text-ink-faint`。

### 工具块：新组件 `ConversationToolPart.vue`

`ConversationView` 中 toolCall / toolResult 分支改为 `<ConversationToolPart :part="part" @toggle="scheduleMeasure" />`，
组件内部仍是 `<details>`，外观沿用现有边框 / 背景，错误结果保持 `border-err`。

**摘要行**（`<summary>`）：箭头 + 图标（调用用新增的 `tool` 图标，结果用新增的 `corner-down-right` 图标）+
tokens。tokens 用 `font-mono text-xs` 渲染，`name` 为 `text-ink font-semibold`，`key` 为 `text-ink-muted`，
`punct` 为 `text-ink-faint`，`value` 为 `text-ink`；整行 `min-w-0 break-all`，允许换行（每个值已被限制在 128 字符，
不再用 `truncate` 截成一行）。错误结果在 tokens 后加一个"错误"标记，样式为 `text-err-ink`。

**展开区**：

- toolCall，`input` 为普通对象且至少一个键：逐个参数一行块，上方是参数名（`font-mono text-2xs text-ink-muted`），
  下方是完整值：字符串用 `<pre>`（`whitespace-pre-wrap break-words font-mono text-xs`，`max-h-96 overflow-auto`）；
  数字 / 布尔 / `null` 用同样的等宽文本显示 `formatToolValue(value, Infinity)`；数组 / 对象用 `JsonViewer`。
- toolCall，`input` 为字符串：一个 `<pre>`。其它（`null`、空对象、数组、数字）：`JsonViewer`。
- toolResult，`content` 非 `null`：逐段渲染，`text` 段用 `<pre>`，`media` 段有图时用 `ImageAttachment`、
  没图时用现有的媒体标签样式，`json` 段用 `JsonViewer`。`content` 为 `null`：`JsonViewer` 显示 `output`。

`ImageAttachment` 的 `load` 事件与 `<summary>` 的点击都向上 `emit('toggle')`，由 `ConversationView` 重新测量折叠高度。

### 图标

`ui/icons/paths.ts` 新增 `tool`（`IconTool`）与 `corner-down-right`（`IconCornerDownRight`），均来自已有依赖
`@tabler/icons-vue`。
