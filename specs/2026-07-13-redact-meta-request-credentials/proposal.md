# 请求头脱敏：meta 请求也需要脱敏

## 原始需求

> 我发现请求头脱敏这个功能，对上游请求没脱敏啊，你看看咋回事，修一下。

## 澄清

经排查确认：**上游（upstream）请求的凭证已正确脱敏**（`gateway_flow_attempts.go` 调用 `redactUpstreamCredentials` 对 upstream 请求 artifact 的 header 和 URL 脱敏）。

真正没有脱敏的是 **meta 请求**（客户端 → PicoTera 的那一跳）。用户澄清：

> 哦，是 meta 没脱敏，upstream 是脱敏了。

meta 请求 artifact 在 `gateway_flow.go` 的上传点直接写入了 `f.meta.RequestHeader`（即 `f.r.Header.Clone()`，客户端原始请求头）和 `f.meta.RequestURL`（客户端原始 URL），没有经过任何脱敏处理。因此客户端用于访问 PicoTera 的 API key（出现在 `Authorization` / `X-Api-Key` / `X-Goog-Api-Key` 头，或 Gemini 格式的 `?key=` 查询参数中）会以明文存入 meta 请求 artifact，并在 dashboard 请求详情中明文可见。

## 目标

对 meta 请求 artifact 的请求头与 URL 施加与 upstream 请求相同的凭证脱敏，使客户端访问 PicoTera 的 API key 不再明文落盘 / 展示。
