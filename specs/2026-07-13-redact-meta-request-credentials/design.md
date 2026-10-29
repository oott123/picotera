# 设计

## 现状

请求 artifact 有两类上传点，均在 `pkg/server/`：

| artifact | 上传点 | 是否脱敏 |
| --- | --- | --- |
| upstream 请求（PicoTera → provider） | `gateway_flow_attempts.go:167-168` | 是，调用 `redactUpstreamCredentials(header.Clone(), url)` |
| meta 请求（client → PicoTera） | `gateway_flow.go:324` | **否**，直接写入原始 header / URL |

`uploadRequestArtifact` 自身不做脱敏，脱敏由调用方负责（响应 artifact 的 cookie 脱敏则在 `uploadResponseArtifact` 内部完成，两者机制不同，本次不动响应侧）。

meta 请求携带的凭证与 upstream 请求落在完全相同的位置：

- `Authorization: Bearer <picotera-key>`
- `X-Api-Key: <picotera-key>`
- `X-Goog-Api-Key: <picotera-key>`
- URL `?key=<picotera-key>`（Gemini 格式）
- `Cf-Access-Client-Id` / `Cf-Access-Client-Secret`（客户端在 Cloudflare Access 后访问时）

因此现有的 `redactUpstreamCredentials` 覆盖的字段集合对 meta 请求同样完全适用，无需新增脱敏逻辑。

## 方案

复用同一个脱敏函数，在 meta 请求上传点同样调用它。

因为该函数将同时用于 meta 与 upstream 两类请求 artifact，其名字 `redactUpstreamCredentials` 会产生误导。按照仓库约定（干净替换、更新所有调用点、不保留兼容层），将其重命名为 **`redactRequestCredentials`**，语义为“对一次请求 artifact 的凭证脱敏”。

- 函数签名、实现不变，仅改名。
- 更新两个调用点（upstream 侧、meta 侧）与测试文件。
- 与响应侧的 `redactResponseHeaders` 命名保持对称（request / response）。

meta 侧改动落在 `gateway_flow.go:324`：先对 `f.meta.RequestHeader.Clone()` 与 `f.meta.RequestURL` 脱敏，再传入 `uploadRequestArtifact`。使用 `Clone()` 保证只脱敏 artifact 副本，不影响 `f.meta.RequestHeader` 本身（其在别处并无被再次使用的凭证读取需求，但为与 upstream 侧保持一致且避免任何隐式副作用，仍走 clone）。

## 影响范围

- meta 请求 artifact 中上述凭证字段将显示为 `[REDACTED]`；dashboard 请求详情随之呈现脱敏值。
- upstream 侧行为不变（仅函数改名）。
- 响应 artifact、meta 请求 body 不在本次范围内（body 脱敏属于 OTR / datamask 的独立议题）。

## 文档

`CLAUDE.md` 中「Upstream credential hygiene」段落当前描述为 “... so meta artifacts are untouched”，本次改动后该表述失效，需同步更新为：meta 与 upstream 请求 artifact 均经 `redactRequestCredentials` 脱敏。
