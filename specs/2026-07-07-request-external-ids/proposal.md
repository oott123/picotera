# Proposal: Request External IDs

为 `request` 表增加 `external_request_id` 和 `external_response_id` 两个 nullable 字段。

- **`external_request_id`**（仅 meta 请求，type=0）：记录外部请求（客户端发来的）请求 ID header 作为 `external_request_id`。upstream 行（type=1）该字段为 NULL。
- **`external_response_id`**（upstream 和 meta 行）：记录上游响应的 request-id header 作为 `external_response_id`。meta 行的值取自最终回写给客户端的那个上游响应。

两个 header 字段名分别通过环境变量配置。每个环境变量接受逗号分隔的多个 header name，匹配时依次匹配，取第一个非空值。两个环境变量各自默认为 `X-Request-Id,X-Log-Id,Cf-Ray`。
