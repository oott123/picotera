# 完整移除"模拟"功能

完整移除 PicoTera 中的"模拟"（simulate / dispatch 模拟）功能，包括后端 API、前端页面、路由、侧边栏入口，以及由其产生的 OpenAPI schema 和生成的 TypeScript 类型。

移除后不保留任何兼容层、占位入口或死代码；所有仅服务于该功能的辅助函数一并删除。
