# 执行计划

1. 依赖调整（在仓库根目录执行）：
   - `pnpm --dir dashboard add @tanstack/markdown@0.0.15 --save-exact`
   - `pnpm --dir dashboard remove marked dompurify @types/dompurify`
2. 修改 `dashboard/src/composables/useSSEParser.ts`：
   - 删除 `marked` 和 `DOMPurify` 的 import，改为 `import { renderHtml } from '@tanstack/markdown/html'`。
   - `renderMarkdown` 改为 `return renderHtml(text, { frontmatter: false, headingIds: false })`，
     在函数上方用一行注释说明两个选项关闭的原因，以及原始 HTML 按文本转义。
3. 修改 `dashboard/src/composables/images.ts` 中 `imageFromUrl` 的注释：去掉 "without passing through DOMPurify" 的说法，
   改为说明这些 URL 直接进入 `<img :src>`、中间没有任何过滤层。
4. 全局搜索 `marked`、`DOMPurify`、`dompurify`，确认 `dashboard/src` 下已无引用。
5. 更新 `dashboard/CLAUDE.md` 中 `useSSEParser` 条目：写明 markdown 由 `@tanstack/markdown` 渲染，
   原始 HTML 按文本显示，不再经过 DOMPurify。
6. 更新根目录 `flake.nix` 的 `pnpmDeps.hash`：运行 `nix build .#picotera-dashboard`，用失败输出里给出的 hash 替换，
   再运行一次确认构建成功。
7. 验证：
   - `pnpm --dir dashboard type-check`
   - `pnpm --dir dashboard lint`
   - `pnpm --dir dashboard build`
   - 用 node 脚本调用 `renderMarkdown` 同样的 `renderHtml` 配置，确认：`hello <b>x</b>` 中标签被转义；
     `<div>\nline **md**\n</div>` 中 `**md**` 渲染为 `<strong>`；`<script>` 被转义为文本；以 `---` 开头的回复正文不丢失；
     标题不带 `id`；`[x](javascript:alert(1))` 不产生链接。
   - `mise run web` 打开对话页、响应「渲染」视图、搜索结果、测试页，找包含 HTML 标签和表格 / 代码块 / 列表的回复，
     确认 HTML 显示为字面文本、其余排版与改动前一致。
