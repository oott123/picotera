# 渲染响应中的图片

修改前端「渲染」部分，对响应中有图片的情况，也渲染图片。参考 `dagbgl8s9a2dcq2ic740` 这个请求。

## 澄清后的范围（与用户确认）

**覆盖视图**（三项全选）：

1. 响应 tab 的「渲染」子视图；
2. 「对话」tab 的响应侧图片；
3. 「对话」tab 的请求侧图片（用户上传的图）。

**响应图片来源格式**：

1. OpenAI Responses 的 `output[].type = "image_generation_call"`，其 `result` 为裸 base64（无 mime，需要嗅探魔数）——即参考请求 `dagbgl8s9a2dcq2ic740` 的形态；
2. 顺带修好现有 OpenAI Images API 实现：当前只渲染 `data[0]` 且把 mime 硬编码为 `image/png`，改为渲染全部 `data[]`，并支持 `data[].url`。

未选中：Gemini `inlineData` 输出、Chat Completions `message.images`（OpenRouter 形态）。

**展示方式**：直接内联展示 + 下载按钮。与现有 Images API 渲染一致的卡片式布局（`max-h` 限高、`object-contain`），附下载原图按钮与类型 / 体积提示。

## 参考数据

- `dagbgl8s9a2dcq2ic740`：Codex `/backend-api/codex/responses` 流式响应，`aggregated.format = "openaiResponses"`，
  `aggregated.body.output[5] = {id, type: "image_generation_call", status: "completed", result: "<3 MB 裸 base64 PNG>"}`，
  同一响应还含 `web_search_call`、`reasoning`、`message` 项。请求侧无图片。
- `dagbjpos9a2dcq2ic770`：`/backend-api/codex/images/generations` 非流式响应，
  body 为 `{created, background, data: [{b64_json}], output_format: "png", quality, size, usage}`，无 `aggregated`。
