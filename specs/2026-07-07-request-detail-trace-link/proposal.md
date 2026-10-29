# 请求详情：移除 Span / Parent Span，增加追踪链接

## 原始需求

移除请求详情中的"Span"和"Parent Span"。增加"追踪"，如果能匹配到对应追踪，则显示追踪 ID 和一个小图标链接，点击链接则可以跳转到 `/requests?traceId=${traceId}`。
