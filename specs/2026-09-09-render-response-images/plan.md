# 执行计划

全部改动在 `dashboard/` 下。工作目录 `dashboard/`，命令用 `pnpm --dir dashboard <script>`。

## 步骤 1：新增 `src/composables/images.ts`

- 定义并导出 `ImageSource { src, mediaType, byteLength }`。
- `decodeBase64Prefix(data: string): Uint8Array | null`：取 `data.slice(0, 24)`（不足 24 时向下取到 4 的倍数），`atob` 包 try/catch，转 `Uint8Array`。
- `sniffImageMediaType(bytes: Uint8Array): string | null`：PNG `89 50 4E 47`、JPEG `FF D8 FF`、GIF `47 49 46 38`、WEBP（`52 49 46 46` 且偏移 8 起为 `57 45 42 50`）。其余返回 `null`。
- `base64ByteLength(data: string): number`：按长度与 `=` padding 计算。
- `imageFromBase64(data, declaredMediaType?)`：`data` 非空字符串才继续；`declaredMediaType` 是以 `image/` 开头的字符串时直接采用，否则嗅探，嗅探失败返回 `null`；返回 `{ src: 'data:' + mediaType + ';base64,' + data, mediaType, byteLength }`。
- `imageFromUrl(url)`：字符串才继续。`data:` 开头时用正则拆 `data:(image/[^;,]+)(;base64)?,` ——非 `image/*` 返回 `null`，`;base64` 时算 `byteLength`，否则 `byteLength = null`；`http://` / `https://` 开头时返回 `{ src: url, mediaType: '', byteLength: null }`；其余一律 `null`。

## 步骤 2：`src/composables/conversation.ts` 接入图片

1. `import type { ImageSource } from './images'` 与 `imageFromBase64` / `imageFromUrl`。
2. `ConversationPart` 的 media 变体改为 `{ kind: 'media'; mediaType: string; label: string; image: ImageSource | null }`；加 `pushMedia(parts, mediaType, image)` 统一生成 `label: \`[${mediaType}]\``，替换现有 5 处 `parts.push({ kind: 'media', ... })`（含 `input_audio` / `input_file` / 未知 type 等 `image: null` 的分支）。
3. `parseOpenAIContentParts`：`image_url` 分支从 `asRecord(part.image_url)?.url` 取；`input_image` 分支从 `part.image_url`（字符串）取；均走 `imageFromUrl`。
4. `parseAnthropicContent` 的 `image` 分支：读 `asRecord(block.source)`，`type === 'base64'` → `imageFromBase64(source.data, source.media_type)`；`type === 'url'` → `imageFromUrl(source.url)`；其余 `null`。
5. `parseGeminiParts`：`inlineData` → `imageFromBase64(inlineData.data, inlineData.mimeType)`；`fileData` → `imageFromUrl(fileData.fileUri)`。
6. `parseOpenAIResponseItem` 增加 `item.type === 'image_generation_call'` 分支：`imageFromBase64(item.result)` 得到非空结果时返回一条 `role: 'assistant'` 的 media 消息，否则 `null`。
7. `parseOpenAIResponsesResponse` 的 `output[]` 循环增加同名分支，把 media part 追加进 `assistantParts`（与 `function_call` 同级），使其与文本 / 思考同属一条 assistant 消息。
8. `ConversationFormat` 增加 `'openaiImages'`；`detectFormat(json, 'response')` 在 `候选 output` / `choices` 判定**之前**插入：`Array.isArray(root.data)` 且 `asRecord(root.data[0])` 存在且其 `b64_json` 或 `url` 为字符串 → `'openaiImages'`。
9. 新增 `parseOpenAIImagesResponse(json)`：遍历 `root.data`，`b64_json` 走 `imageFromBase64`、`url` 走 `imageFromUrl`，全部塞进一条 assistant 消息；无可用图片时返回 `[]`。在 `parseResponseConversation` 的 switch 中接上（`parseRequestConversation` 的 switch 也需补齐这一分支以满足穷尽性，返回 `[]`）。
10. 新增导出 `collectConversationImages(messages: ConversationMessage[]): ImageSource[]`。

## 步骤 3：新增 `src/components/ImageAttachment.vue`

- Props：`image: ImageSource`、`alt?: string`、`filename?: string`、`maxHeightClass?: string`（默认 `'max-h-[640px]'`）；Emits：`load`。
- 本地 `formatSize(byteLength)`：≥1 MB 显示 `x.x MB`，≥1 KB 显示 `x.x KB`，否则 `n B`；`null` 时不显示体积。
- 本地 `defaultFilename()`：由 `mediaType` 的子类型推扩展名（`jpeg → jpg`，其余取子类型），`mediaType` 为空时用 `image`。
- 模板：`figure.m-0.overflow-hidden.rounded-md.border.border-line-soft.bg-surface-50` 包 `<img :src="image.src" :alt="alt ?? '响应图片'" class="block w-full object-contain" :class="maxHeightClass" loading="lazy" decoding="async" @load="$emit('load')">`；底部 `figcaption` 一行 flex：左侧 `text-2xs text-ink-muted` 显示 `PNG · 2.3 MB`（`mediaType` 为空时显示 URL host），右侧 `IconButton` 包 `<a :href="image.src" :download="filename ?? defaultFilename()">` + `<Icon name="cloud-download" :size="13" />`，`title` / `aria-label` 为「下载图片」。

## 步骤 4：`ConversationView.vue` 渲染图片

- 引入 `ImageAttachment`。
- `part.kind === 'media'` 分支拆成两支：`part.image` 存在 → `<ImageAttachment :image="part.image" max-height-class="max-h-[320px]" @load="scheduleMeasure" />`；否则保留现有 chip（`v-else`）。
- 确认 `scheduleMeasure` 在图片 `load` 后被调用，带图消息能正确出现「展开」按钮。

## 步骤 5：`ResponseArtifactView.vue` 接入

- 删除 `openAIImageGeneration` computed 与其专用的本地 `asRecord`（确认文件内无其它调用后再删）。
- 引入 `parseResponseConversation`、`collectConversationImages`、`ImageAttachment`。
- 新增 `renderSource` computed（聚合优先、其次 `jsonBody`）与 `responseImages` computed。
- 模板「渲染」分支：原 `figure` 块替换为 `v-for` 的 `<ImageAttachment v-for="(img, i) in responseImages" :key="i" :image="img" :filename="..." :alt="`响应图片 ${i + 1}`" />`，位置保持在思考过程之前。
- 「无可渲染内容」的 `v-if` 中 `!openAIImageGeneration` 换成 `!responseImages.length`。

## 步骤 6：校验

1. `pnpm --dir dashboard type-check`
2. `pnpm --dir dashboard lint`
3. `pnpm --dir dashboard format`
4. 起 `mise run web`，对照两条真实数据人工验收：
   - `dagbgl8s9a2dcq2ic740`（Codex Responses 流式，含 `image_generation_call`）：响应 tab →「渲染」出图、可下载、体积提示为 PNG 约 2.3 MB；「对话」tab 的 assistant 消息内出图，展开 / 折叠正常。
   - `dagbjpos9a2dcq2ic770`（`images/generations` 非流式）：「渲染」仍出图（改走新路径，mime 由嗅探得出）；「对话」tab 从「无法解析为对话」变为显示该图。
   - 任取一条无图请求，确认「渲染」与「对话」行为无回归。
