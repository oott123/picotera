# 需求：上游模型列表支持 Codex 格式

解析上游模型时，支持 codex 格式，参考 fixtures 的 `codex-models.json` 文件，要点就是 `$.models[].slug`。

## 澄清（规划阶段确认）

- **不做任何过滤**：`$.models[]` 里的每个 `slug` 都收下。fixture 中 `gpt-reserve`、`codex-auto-review` 两个模型 `visibility` 为 `hide`，但仍可通过 API 调用；是否保留由运营者在渠道模型面板里自行删除，解析层不看 `visibility` / `supported_in_api` 等附加字段。
- **只加 `models[].slug` 一条路径**：不顺带支持 Gemini 的 `{"models":[{"name":"models/xxx"}]}`。
