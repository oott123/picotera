# 请求自定义 Annotations（proposal）

我想给请求设计一套自定义元数据的功能，就 KV String pairs 吧，比如用户可以用脚本设置某个请求的元数据是 `{"agent": "claude-code"}`，然后它可以通过接口列出所有 agent == "claude-code" 的请求，方便它查询。

但是 requests 表是比较核心的表，它是 timescaledb 的 hypertable，我希望这个功能足够高效，不会拖垮原本的效率，同时也不要增加太大的磁盘负担。查询我可以接受比如只能查最近 7 天这样的代价。

目前这些元数据都由用户自己挂脚本写进去，暂时不做任何界面相关修改。

## 规划过程中的补充澄清

- **命名：这套请求级元数据叫 annotations**（不叫 metadata）。
- **meta 请求（type=0）和 upstream 请求（type=1）分别记录**各自的 annotations。
- **JS API 形态**：`ctx.metaRequest.annotations` 与 `ctx.upstreamRequest.annotations`，两个 annotation 对象作为 Proxy，读写直通 Go 侧 API，并在写入时校验类型（值必须是字符串）。
- 方案必须与将来在 `request` 表上启用 TimescaleDB 压缩兼容；可以接受建索引，但不接受 hypertable 边表。
- 存储方案确认：`request` 表加 nullable JSONB `annotations` 列 + partial GIN 索引（`jsonb_path_ops`，仅收录非 NULL 行）；将来启用压缩时把 `compress_after` 设为大于查询窗口（例如只压缩 90 天以上的数据）。
- 带 annotations 过滤的查询时间窗：和 requestId 查询一致——缺省默认最近 30 天，调用方可显式传时间范围覆盖。
- 单请求 annotations 不做条数/长度硬性限制。
