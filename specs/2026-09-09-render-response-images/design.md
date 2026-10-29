# 设计：渲染响应 / 请求中的图片

纯前端改动，不涉及 Go 侧、OpenAPI 或数据库。不引入第三方库。

## 现状

图片在前端有两条互不相干的路径，且都不真正显示图片：

- **「渲染」子视图**（`ResponseArtifactView.vue`）：只有一个 `openAIImageGeneration` computed，从**原始 JSON body** 取 `data[0].b64_json`，硬编码拼 `data:image/png;base64,`。对参考请求这类 SSE 响应完全不生效——图片在 `payload.aggregated.body` 里，而不是可解析的 JSON body。
- **「对话」tab**（`conversation.ts` → `ConversationView.vue`）：各格式的图片一律降级成 `{kind:'media', mediaType, label}`，渲染为一个 `[image]` 文本 chip；`image_generation_call` 这种 item 在 `parseOpenAIResponseItem` 里直接返回 `null`，连 chip 都没有。

## 核心决策：两个视图共用一条格式解析路径

不为「渲染」子视图单独写一套图片抽取器。`conversation.ts` 已经是「按格式把 payload 拆成 parts」的唯一权威，图片能力加在它的 `media` part 上，「渲染」子视图则复用 `parseResponseConversation()` 的结果、把其中带图的 media part 摊平成一个列表。

这样 `image_generation_call` 的解析只写一次，两个视图同时生效；也避免了「渲染」和「对话」对同一份响应给出不同判断。

## 模块划分

### 1. `src/composables/images.ts`（新增）

只管「一段数据能不能当图片显示」，不认识任何 LLM 格式。

```ts
export interface ImageSource {
  src: string          // 可直接进 <img>：data: URL 或 http(s) URL
  mediaType: string    // 'image/png' 等；远端 URL 且无从判断时为 ''
  byteLength: number | null  // base64 解码后的字节数；远端 URL 为 null
}

export function imageFromBase64(data: unknown, declaredMediaType?: unknown): ImageSource | null
export function imageFromUrl(url: unknown): ImageSource | null
```

**MIME 判定规则（两条，不做第三种猜测）**：

- payload 自带真正的 MIME（Anthropic `source.media_type`、Gemini `inlineData.mimeType`）且以 `image/` 开头 → 原样采用，不嗅探。
- 否则嗅探字节魔数：取前 24 个 base64 字符 `atob` 出 18 字节，识别 PNG（`89 50 4E 47`）、JPEG（`FF D8 FF`）、GIF（`GIF8`）、WEBP（`RIFF`+ 偏移 8 处 `WEBP`）。识别不出 → 返回 `null`，调用方退回文本 chip，**不猜成 png**。

Images API 的 `output_format: "png"` 是个裸格式词而非 MIME，**不参与判定**——字节本身就是权威，一条嗅探规则覆盖它。这同时修掉了现有硬编码 `image/png` 的问题。

**`imageFromUrl` 的 scheme 白名单**：只接受 `data:image/…` 与 `http(s):`，其余（`gs://`、`javascript:`、`file:` 等）返回 `null`。图片 URL 来自上游 / 客户端的不可信 payload，`<img :src>` 不经过 DOMPurify，所以协议校验在这里做闭合。Gemini 的 `fileData.fileUri` 因此自然落到 chip。

`byteLength` 由 base64 长度算出（`len/4*3` 减去 padding），供体积提示与下载文件名使用。

### 2. `src/composables/conversation.ts`（改）

`media` part 携带图片：

```ts
| { kind: 'media'; mediaType: string; label: string; image: ImageSource | null }
```

保留 `label`，`image` 为 `null` 时行为与今天完全一致（chip）。各格式接入点：

| 位置 | 取源 |
| --- | --- |
| `parseOpenAIContentParts` / `image_url`（Chat） | `part.image_url.url` → `imageFromUrl` |
| `parseOpenAIContentParts` / `input_image`（Responses） | `part.image_url`（此处是字符串）→ `imageFromUrl`；只有 `file_id` 时无图 |
| `parseAnthropicContent` / `image` | `source.type='base64'` → `imageFromBase64(source.data, source.media_type)`；`source.type='url'` → `imageFromUrl(source.url)` |
| `parseGeminiParts` / `inlineData` | `imageFromBase64(data, mimeType)` |
| `parseGeminiParts` / `fileData` | `imageFromUrl(fileUri)`（`gs://` 会被拒，落 chip） |
| `parseOpenAIResponseItem` / `image_generation_call`（新增分支） | `imageFromBase64(item.result)`，`result` 非空字符串才产出 part |

`image_generation_call` 加在 `parseOpenAIResponseItem` 里而不是只加在响应侧：该函数被请求 `input[]` 与响应 `output[]` 共用，Codex 多轮对话会把上一轮的 `image_generation_call` 原样回填进 `input`，一处改动两边都对。响应侧 `parseOpenAIResponsesResponse` 的 `output[]` 循环需要把这一 item 类型并进 assistant 的 parts（与 `function_call` 同级处理），保持它与文本、思考在同一条 assistant 消息里。

**新增格式 `openaiImages`**：`detectFormat(json, 'response')` 增加一条判定——`root.data` 是数组且首元素是对象且带 `b64_json: string` 或 `url: string`。判定放在 `choices` / `output` 之前，且要求首元素形状匹配，因此 embeddings 响应（`data: [{embedding}]`）不会误命中。`parseOpenAIImagesResponse` 把 `data[]` 全部元素转成一条 assistant 消息的多个 media part（`b64_json` 走 `imageFromBase64`，`url` 走 `imageFromUrl`）。这一条既修好了「渲染」子视图的 Images API 分支，也让「对话」tab 对 `images/generations` 响应从「无法解析为对话」变成显示图片。

**新增导出** `collectConversationImages(messages): ImageSource[]`：遍历消息取出所有 `image` 非空的 media part，供「渲染」子视图使用。

**已知连带影响（有意接受）**：Gemini 的 `inlineData` 在请求与响应共用 `parseGeminiParts`，因此 Gemini 的图片输出也会被渲染。为「只渲染请求侧」而给解析器加方向开关，代价高于收益，故不做。

### 3. `src/components/ImageAttachment.vue`（新增）

两个视图共用的展示单元。Props：`image: ImageSource`、`alt?: string`、`filename?: string`、`maxHeightClass?: string`（默认 `max-h-[640px]`，对话内传更小值）。Emits：`load`。

结构沿用现有 Images API 卡片：`figure` + `rounded-md border border-line-soft bg-surface-50`，`<img class="block w-full object-contain" loading="lazy" decoding="async">`，底部一行 `text-2xs text-ink-muted` 显示 `PNG · 2.3 MB`（`mediaType` 取子类型大写，体积由 `byteLength` 算 KB/MB，远端 URL 时显示 host），右侧一个 `IconButton` + `cloud-download` 图标做下载。

下载直接用 `<a :href="image.src" :download="filename">`——`data:` URL 原生支持 `download`，无需 Blob 中转。`filename` 默认由 mediaType 推子扩展名（`image.png`）；「渲染」子视图传 `${requestId}-image-${n}.<ext>`。

`load` 事件用于对话视图重算折叠高度（见下）。

### 4. `src/components/ConversationView.vue`（改）

`part.kind === 'media'` 分支：`part.image` 非空时渲染 `<ImageAttachment :image="part.image" max-height-class="max-h-[320px]" @load="scheduleMeasure" />`，否则保持现有 chip。

**必须处理的时序问题**：消息体折叠靠 `measureOverflow()` 读 `scrollHeight`，而图片是异步解码的——首次测量时 `<img>` 高度为 0，会把带图消息误判为不溢出、不给「展开」按钮。故 `ImageAttachment` 的 `load` 事件必须回调 `scheduleMeasure()`。折叠阈值 80px 不变，带图消息默认折叠、点「展开」看全图。

### 5. `src/components/ResponseArtifactView.vue`（改）

删除 `openAIImageGeneration` 与本地 `asRecord`，替换为：

```ts
const renderSource = computed(() => {
  if (payload.aggregated?.body !== undefined && !payload.aggregated.error)
    return { json: payload.aggregated.body, format: payload.aggregated.format }
  if (jsonBody.ok) return { json: jsonBody.value, format: undefined }
  return null
})
const responseImages = computed(() =>
  collectConversationImages(parseResponseConversation(source.json, source.format) ?? []))
```

与「对话」tab 的 `responseMessages()` 取源顺序一致：优先聚合结果，其次可解析的 JSON body。这让非流式的 Responses 响应（后端不产出 `aggregated`）也能出图，同时不改动文本 / 思考的现有取源（仍只走 `extractContentFromAggregated`）。

模板里图片区块**保持在最上方**（现有 Images API 的位置），多张图纵向堆叠；「无可渲染内容」的判空条件把 `openAIImageGeneration` 换成 `responseImages.length`。

## 不做的事

- 不改 Go 侧、不加接口、不动 artifact 存储格式。
- 不支持 Chat Completions `message.images`（OpenRouter 形态）——库里无样本。
- 不做图片灯箱 / 缩放 / 相册。内联 + 下载即为全部交互。
- 不做懒解码或体积上限。图片数据本来就已随 artifact 全量加载进内存，再加一层门槛只增加点击成本。
