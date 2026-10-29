# 设计：JSON 查看器折叠对象的摘要

## 现状

`JsonViewer.vue` 把可见节点拍平成行（`jsonTree.ts` 的 `flatten`），行的 `preview` 在遍历时算好。
折叠的非空对象固定显示 `{…}`，展开后 preview 置空；数组显示 `[N 项]`。

## 方案

新增纯函数模块 `dashboard/src/components/json-viewer/jsonSummary.ts`，不依赖 Vue，只 import `@/lib/toolFormat`
（复用 `toolCallTokens` 格式化工具调用参数）。它按字段名从对象里提取摘要，不区分 Anthropic / OpenAI / Gemini
格式：这几种格式的消息、内容块、工具定义、SSE 事件用的是同一批字段名（`type`、`role`、`name`、`text`、`content`、
`parts`、`delta` …），按字段名匹配就能覆盖全部格式，不需要先识别格式。

`flatten` 只给**折叠的非空对象**算摘要，结果放在 `JsonRow.summary`。已展开的对象、空对象、数组、标量的
`summary` 都是 `null`。每个对象的提取量有固定上限（见下文），所以即使有 `ROW_BUDGET` 行，开销也有上限，
和 `stringPreview` 的限制方式一样。

### 数据结构

```ts
export type TagKey = 'type' | 'role' | 'object'

export interface SummaryTag {
  /** 这个标签取自哪个字段。 */
  key: TagKey
  text: string
}

export interface ObjectSummary {
  /** 类标签：`type` / `role` / `object` 的值，按这个顺序，按 text 去重。 */
  tags: SummaryTag[]
  /** 名字，已截断到 NAME_LIMIT。 */
  name: string | null
  /** 工具调用参数 `(key="value", …)`，整体截断到 TEXT_LIMIT。只和 name 一起出现，且与 text 互斥。 */
  args: ToolToken[] | null
  /** 正文片段，空白已压缩，已截断到 TEXT_LIMIT。 */
  text: string | null
}

export const TAG_MAX_CHARS = 32
export const NAME_LIMIT = 40
export const TEXT_LIMIT = 128

export function objectSummary(record: Record<string, unknown>): ObjectSummary | null
```

tags 为空且 name / args / text 都没有提取到时返回 `null`，这一行仍然只显示 `{…}`。

### 提取规则

**tags**：依次读 `type`、`role`、`object`。值必须是非空字符串、长度不超过 `TAG_MAX_CHARS`、不含空白字符，
否则跳过这个键（这类字段里是长句的话就不是类标签）。和已收集的值相同时也跳过。tags 只取最外层对象自己的字段。

**块（name / args / text）**：`blockOf(record, depth)` 从一个对象里提取 `{name, args, text}`，最外层以
`depth = TEXT_DEPTH = 2` 调用：

1. name：按顺序取第一个非空字符串：`name`、`function.name`、`functionCall.name`、`functionResponse.name`，
   超过 `NAME_LIMIT` 就截断并加 `…`。
2. 有 name 时找参数对象：依次看 `input`、`args`、`functionCall.args`，第一个是对象的就是参数；都不是时，
   依次看 `arguments`、`function.arguments`，是不超过 `ARGS_PARSE_LIMIT = 8192` 个 UTF-16 单元的字符串、
   且 `JSON.parse` 结果是对象的就是参数。找到参数时这是一次工具调用，返回 `{name, args, text: null}`，不再往下找。
3. 否则按 `TEXT_KEYS` 的顺序，对每个字段的值调用 `valueBlock(value, depth)`，取第一个非空结果作为 inner：

   ```ts
   const TEXT_KEYS = ['text', 'content', 'parts', 'thinking', 'summary', 'delta', 'output', 'arguments', 'description', 'function', 'tool_calls']
   ```

4. 对象自己有 name：返回 `{name, args: null, text: inner?.text}`。没有 name：直接返回 inner，也就是 name、args、
   text 全部来自第一个有内容的嵌套块。这样名字和正文总是来自同一个块，消息的第一个块是 `tool_use` 时显示成这次调用。

`valueBlock(value, depth)`：

- 字符串：包含非空白字符时返回 `{text: value}`，否则 `null`。
- `depth === 0` 的容器：`null`。
- 数组：依次看前 `ARRAY_SCAN = 4` 个元素，返回第一个 `valueBlock(element, depth - 1)` 非空的结果。
- 对象：`blockOf(value, depth - 1)`。

参数格式化：取参数对象的前 `ARGS_SCAN = 32` 个键组成新对象，交给 `toolCallTokens(name, head, TEXT_LIMIT)`，
去掉第一个 name token；剩下的 token 累计长度超过 `TEXT_LIMIT` 时，把当前 token 截到剩余长度，后面接
`…)` 并结束。

片段处理：先从第一个非空白字符起取 `TEXT_LIMIT * 4` 个 UTF-16 单元（不对整串操作，多兆字节的字符串也不会整串
处理），把连续空白压缩成一个空格，再截到 `TEXT_LIMIT` 个 UTF-16 单元，截断点不能拆开代理对。被截断时（切片后
仍超长，或原串在切片之后还有内容）末尾加 `…`。这里改的只是显示用的副本，不改数据。

### 效果

| 对象 | 折叠后显示 |
| --- | --- |
| `{role:"user", content:"Hello world"}` | `{…} user "Hello world"` |
| `{role:"assistant", content:[{type:"thinking", thinking:"Let me check"},{type:"text", text:"Sure"}]}` | `{…} assistant "Let me check"` |
| `{role:"assistant", content:[{type:"tool_use", name:"Bash", input:{command:"ls"}}]}` | `{…} assistant Bash(command="ls")` |
| `{role:"assistant", content:[{type:"text", text:"I'll run"},{type:"tool_use", …}]}` | `{…} assistant "I'll run"` |
| `{type:"tool_use", id, name:"Read", input:{file_path:"/a/b", limit:20}}` | `{…} tool_use Read(file_path="/a/b", limit=20)` |
| `{type:"tool_result", tool_use_id, content:"total 8"}` | `{…} tool_result "total 8"` |
| `{name:"Bash", description:"Executes…", input_schema}` | `{…} Bash "Executes…"` |
| `{type:"function", function:{name:"get_weather", description:"Get weather", parameters}}` | `{…} function get_weather "Get weather"` |
| `{id, type:"function", function:{name:"get_weather", arguments:"{\"city\":\"Paris\"}"}}` | `{…} function get_weather(city="Paris")` |
| `{role:"assistant", content:null, tool_calls:[{…function:{name:"f", arguments:"{}"}}]}` | `{…} assistant f()` |
| `{type:"message", role:"user", content:[{type:"input_text", text:"Hi"}]}` | `{…} message user "Hi"` |
| `{type:"function_call", name:"shell", arguments:"{\"cmd\":[\"ls\"]}"}` | `{…} function_call shell(cmd=["ls"])` |
| `{type:"function_call", name:"shell", arguments:"not json"}` | `{…} function_call shell "not json"` |
| `{role:"model", parts:[{functionCall:{name:"search", args:{q:"x"}}}]}` | `{…} model search(q="x")` |
| `{type:"content_block_delta", index:0, delta:{type:"text_delta", text:"He"}}` | `{…} content_block_delta "He"` |

### 渲染

`JsonViewer.vue` 的容器分支在 `{{ row.preview }}` 后面接着渲染 `row.summary`：

- tags：每个前面留 `ml-1.5`；颜色由 `tagClass(key)` 决定，`role` 用 `text-ink`，`type` / `object` 用 `text-ink-muted`
- name：`ml-1.5 font-medium text-ink`
- args：紧跟在 name 后面，按 token 分色，与对话页工具调用一致（`ARG_TOKEN_CLASS`）：key `text-ink-muted`、
  punct `text-ink-faint`、value `text-ink`
- text：`ml-1.5 text-ink-faint`，前后加 `"`

摘要和 key、preview 在同一个 inline run 里。软换行模式下，容器行（对象、数组）也不换行，固定一行，超出行宽的部分
用省略号截掉（`truncate`）。省略号的颜色继承自这一行外层的 span，所以外层 span 由 `containerTailClass(row)`
取最后一段的颜色：有 text 时 `text-ink-faint`，有 args 时 `ARG_TOKEN_CLASS.value`，有 name 时 `text-ink`，
有 tags 时取最后一个 tag 的 `tagClass`，都没有时用 preview 的颜色。字符串行照常换行。搜索高亮不作用在摘要上：搜索模式下
可见的容器都是展开的，不会出现摘要。
