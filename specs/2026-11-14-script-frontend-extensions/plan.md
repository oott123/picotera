# 执行计划

设计见 `design.md`，接口见 `api.md`。

## 1. 数据库与后端

1. 新建 `db/migrations/053_script_frontend.sql`（内容见 `api.md`）。
2. `db/queries/script.sql`：`InsertScript` / `UpdateScript` 按 `api.md` 修改，新增 `ListEnabledScriptFrontends`，然后执行 `sqlc generate`。
3. `pkg/contract/script.go`：`ScriptView` 和 `ScriptMutateBody` 新增 `Frontend`，`ToScriptView` 赋值。新建 `pkg/contract/script_frontend.go`（见 `api.md`）。
4. `pkg/server/handle_script.go`：`InsertScriptParams` / `UpdateScriptParams` 传入 `Frontend`；新增 `handleListScriptFrontends`。`pkg/server/server.go` 的 `register()` 把 `OperationListScriptFrontends` 注册到 mgmt 组。
5. `go build ./... && go test ./pkg/jsx/... ./pkg/server/...`。
6. `mise run openapi`，再执行 `pnpm --dir dashboard generate-openapi`。

## 2. 控制台：扩展运行时

1. 新建 `dashboard/src/extensions/types.ts`（内容见 `api.md`）。
2. 新建 `dashboard/src/extensions/validate.ts`：实现 `validateDescriptor`，规则见 `design.md`「描述符校验」。图标校验用 `iconComponents` 的键。
3. 新建 `dashboard/src/extensions/host.ts`：实现 `createExtensionHost`。
4. `api/client.ts` 新增 `listScriptFrontends`，`invalidateScripts` 同时让 `scriptFrontends.all` 失效；`api/queryKeys.ts` 新增 `scriptFrontends`；`api/index.ts` 导出 `ScriptFrontendView`。
5. 新建 `dashboard/src/stores/extensions.ts`：`entries`、`me`、`buttonError`、`sync`、`pages`、`buttonsFor`、`findPage`，行为见 `design.md`「加载器」。
6. 新建 `dashboard/src/composables/useExtensionsLoader.ts`，在 `App.vue` 的 setup 中调用。
7. 新建 `dashboard/src/extensions/ExtensionBoundary.vue` 和 `dashboard/src/extensions/ExtensionButtons.vue`。

## 3. 控制台：接入现有界面

1. `router/index.ts`：注册 `extensionPage` 路由（不加入 `ADMIN_ROUTES`）。
2. 新建 `dashboard/src/views/ExtensionPageView.vue`。
3. `App.vue`：`pageMeta` 处理 `extensionPage`；在 `<RouterView />` 上方显示 `buttonError`。
4. `AppSidebar.vue`：增加「扩展」分组。
5. `ProvidersView.vue`：接入 `providers.toolbar` 和 `providers.row` 两个插槽。
6. `ModelsView.vue`：接入 `models.toolbar` 插槽。
7. `ScriptForm.vue`：拆成服务端 / 前端两个标签页，提交时带上 `frontend`，并显示加载错误。
8. `ScriptsView.vue`：增加前端状态标记；切换启用状态时带上 `frontend`。
9. `pnpm --dir dashboard lint && pnpm --dir dashboard type-check && pnpm --dir dashboard format`。

## 4. 示例与文档

1. 新建 `docs/example-scripts/header-routing.js`、`docs/example-scripts/header-routing.frontend.js`、`docs/example-scripts/register-orphan-models.frontend.js`，行为见 `design.md`「示例脚本」。
2. `docs/scripting.md`：新增「控制台扩展」一节，并更新「示例」一节的列表。
3. 更新根目录 `CLAUDE.md` 和 `dashboard/CLAUDE.md`（见 `design.md`「文档」）。

## 5. 手动验证

本地启动 `docker compose up -d`、`mise run server`、`mise run web`，然后以管理员身份逐项确认：

1. 新建脚本，把两个示例的前端代码粘贴进去：
   - 侧边栏出现「扩展」分组，点进去能看到请求头分流页面；
   - 页面中添加规则、连线、保存后，不刷新页面直接切到 KV 页面，就能看到 `header-routing` 的新值（扩展和控制台共用同一份查询缓存）；
   - 带上 `X-Picotera-Route` 请求头调用网关，请求只会落到连线的渠道上。
2. 模型页的「注册所有未注册模型」按钮执行后，未注册模型的列表被清空。
3. 编辑前端代码并保存后，不刷新页面，页面和按钮就会更新。
4. 分别保存这几种前端代码：有语法错误的、默认导出不是函数的、描述符带有未知键的。每种情况下，脚本列表都显示「前端加载失败」，编辑表单中能看到错误信息，其他扩展和控制台本身都正常。
5. 页面组件在渲染时抛错，只会让这个页面显示错误；按钮的 `onClick` 抛错，会在页面顶部显示错误提示，并且可以关闭。
6. 禁用脚本后，它的页面和按钮都会消失。
7. 再新建一个脚本，前端代码返回一个不判断 `host.me` 的页面。以普通用户登录，或者以管理员身份扮演普通用户：这个页面出现在侧边栏「扩展」分组里，请求头分流页面不出现；结束扮演后，请求头分流页面重新出现。
8. 用普通用户调用 `GET /api/picotera/script-frontends` 返回 200，调用 `GET /api/picotera/scripts` 返回 403。
