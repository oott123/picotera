# 网关 CORS 与测试页去 Cookie

## 需求

1. 为所有网关接口增加 CORS 响应头，使浏览器可以跨源调用网关。所有网关接口指：
   - 路径匹配的 catch-all 网关（`endpoint` 表配置的端点，含 model-list 端点）。
   - `/api/unified/*` 五条统一生成路由。
   - 不含 `/api/picotera` 管理接口、静态资源。
2. 在“测试”页发起**网关测试**（`postGatewayTest`）时，让浏览器不再携带 cookies；短路测试（`postTestDirect`，命中管理接口）保持原样。
