# 设计：脚本前端扩展

接口细节（数据库字段、管理 API、扩展作者 API）见 `api.md`。

## 现状

- `script` 表只有 `source` 一份源码，内容是服务端 QuickJS 代码。每个网关请求都会新建一个 VM，把所有启用的脚本 eval 一遍（`pkg/jsx/session.go` `newSession`）。脚本 CRUD 属于 admin 组，控制台对应的页面是 `ScriptsView.vue` + `ScriptForm.vue`。
- 控制台的路由、侧边栏导航、页头标题都是静态写死的（`router/index.ts`、`AppSidebar.vue`、`App.vue` 的 `pageMeta`）。管理员页面由 `ADMIN_ROUTES` 和 `useMe().isAdmin` 控制可见性。
- 静态资源没有设置 CSP，`blob:` 模块可以直接 `import()`。

## 方案概览

1. `script` 表新增 `frontend TEXT NOT NULL DEFAULT ''`。非空时，它的内容是一个浏览器端 ES module；为空表示这个脚本没有前端部分。`frontend` 和 `source` 共用 `enabled` 开关，但两者的执行环境互不相关：服务端照常 eval `source`，从不读取 `frontend`。
2. 新增 mgmt 组接口 `GET /api/picotera/script-frontends`，所有登录用户都能调用，只返回启用、且 `frontend` 非空的脚本的 `id`、`name`、`frontend`。`listScripts` 返回内容里有服务端源码，仍然只对管理员开放。
3. 控制台对所有登录用户加载这个接口返回的脚本：把每段代码转成 blob URL 后 `import()`，再用一个 `host` 对象调用模块的默认导出，拿到一份声明（页面 + 按钮）。`host.me` 是当前用户，作者自己决定声明里的哪些内容只给管理员。
4. 页面挂在路由 `/ext/:scriptId/:pageId` 下，出现在侧边栏新增的「扩展」分组里。按钮挂在控制台预留的三个插槽上：渠道列表工具栏、渠道行操作、模型列表工具栏。这三个插槽所在的页面本身只有管理员能进，所以按钮实际上只有管理员看得到。
5. 每个脚本单独隔离错误：加载失败、声明不合法、页面渲染出错、按钮执行出错，都只影响这个脚本自己的位置，并显示错误信息。
6. 脚本保存以后，`invalidateScripts` 会让前端代码列表重新获取；加载器对比代码有没有变化，只重新加载变化了的脚本，不需要刷新页面。当前用户变化时（开始或结束扮演）全部重新加载，因为 `host.me` 变了。

## 服务端

- 迁移 `053_script_frontend.sql`：`ALTER TABLE script ADD COLUMN frontend TEXT NOT NULL DEFAULT ''`。
- `InsertScript` / `UpdateScript` 增加 `frontend` 参数。`ListEnabledScripts` 用的是 `SELECT *`，生成代码后 `db.Script` 会自带这个字段，jsx 不需要改。
- 新查询 `ListEnabledScriptFrontends`，按 `id` 升序返回启用、且 `frontend <> ''` 的脚本的 `id`、`name`、`frontend`。
- `ScriptView` 和 `ScriptMutateBody` 新增必填字段 `frontend`，服务端原样存储，不做语法检查：QuickJS 编译 module 时会解析静态 `import` 的远程地址，没法单独校验语法。语法错误会在控制台加载时暴露出来。`source` 的 `ValidateSyntax` 保持不变。
- 新 contract 文件 `pkg/contract/script_frontend.go`：`ScriptFrontendView`、`ListScriptFrontendsResponse`、`OperationListScriptFrontends`。新 handler `handleListScriptFrontends` 写在 `pkg/server/handle_script.go` 里，在 `register()` 中注册到 **mgmt** 组，放在 `OperationGetMe` 附近。

## 控制台

### 文件划分

新目录 `dashboard/src/extensions/`：

- `types.ts`：`ExtensionHost`、`ExtensionDescriptor`、`ExtensionPage`、`ExtensionButton`、`ExtensionSlot` 以及各插槽的上下文类型（`api.md`「扩展作者 API」）。
- `host.ts`：`createExtensionHost()`，组装 `host` 对象。
- `validate.ts`：`validateDescriptor(value: unknown): ExtensionDescriptor`，校验失败时抛出 `Error`，错误信息指明具体是哪个字段。
- `ExtensionButtons.vue`：渲染某个插槽上的所有按钮。
- `ExtensionBoundary.vue`：错误边界组件。
- `ExtensionPageView.vue`：路由 `/ext/:scriptId/:pageId` 对应的页面组件（放在 `src/views/`，与其他路由页面放在一起）。

新 store `dashboard/src/stores/extensions.ts`（Pinia）。

### 加载器（`stores/extensions.ts`）

store 的状态是 `entries: Record<scriptId, ExtensionEntry>`：

```ts
type ExtensionEntry = {
  scriptId: string
  scriptName: string
  frontend: string          // 本次加载对应的源码，用于判断是否变化
  generation: number        // 每次重新加载递增，用于丢弃过期的加载结果
  status: 'loading' | 'ready' | 'error'
  error: string             // status === 'error' 时的错误信息
  descriptor: ExtensionDescriptor | null
}
```

另外维护 `buttonError: { scriptId: string; label: string; message: string } | null`（见「按钮」）。

store 还保存一个 `me: MeView | null`，记录最近一次加载时用的用户。

`sync(frontends: ScriptFrontendView[], me: MeView)`：

1. 如果 `me.id` 或 `me.isAdmin` 和 store 里的 `me` 不同，先把所有条目视为需要重新加载，再更新 store 的 `me`。
2. 已有条目中，不在 `frontends` 里的直接删除。
3. `frontends` 里的脚本，如果还没有条目、条目的 `frontend` 和当前代码不同，或者第 1 步要求重新加载，就新建或覆盖条目（`status = 'loading'`，`generation + 1`），然后异步加载。只有 `name` 变了的话，直接更新 `scriptName`，不重新加载。
4. 加载过程：先编译源码里用到的 Tailwind 类名（见「Tailwind 编译」），再 `new Blob([frontend], { type: 'text/javascript' })` → `URL.createObjectURL` → `import(/* @vite-ignore */ url)` → `finally` 里 `URL.revokeObjectURL`。模块的 `default` 必须是函数，否则报错 `default export must be a function`。用 `createExtensionHost(me)` 调用它，`await` 返回值，再交给 `validateDescriptor` 校验。
5. 结果写回条目之前，先比较 `generation`。不一致说明这期间又有新的加载，丢弃本次结果。成功时 `status = 'ready'`；任何异常都记为 `status = 'error'`，`error` 取 `e instanceof Error ? e.message : String(e)`。

派生 getter（按 `scriptId` 字典序排列，同一脚本内保持声明顺序）：

- `pages`：所有 `ready` 条目的页面，每项带上 `scriptId`、`scriptName`。
- `buttonsFor(slot)`：所有 `ready` 条目在这个插槽上的按钮，每项带上 `scriptId`。
- `findPage(scriptId, pageId)`。

谁来驱动：新增组合函数 `useExtensionsLoader()`（`src/composables/`），只在 `App.vue` 里调用一次。它通过 `useMe()` 拿到 `me`，再 `useQuery({ queryKey: queryKeys.scriptFrontends.all, queryFn: listScriptFrontends })`；`watch` 这两者，都有数据时调用 `sync(frontends, me)`。

`api/client.ts` 新增 fetcher `listScriptFrontends`；`queryKeys` 新增 `scriptFrontends: { all: ['scriptFrontends'] }`；`invalidateScripts` 同时让 `scriptFrontends.all` 失效，所以保存、启用 / 禁用、删除脚本都会触发热重载。扮演开始和结束时，`queryClient.clear()` 已经会让 `me` 和这个列表重新获取，再由第 1 步触发全部重新加载。

模块无法卸载。重新加载就是 import 一份新模块，旧模块对象不再被引用；模块里注册的全局副作用（定时器、全局事件监听）由作者自己负责清理，文档中说明。

### Tailwind 编译（`extensions/tailwind.ts`）

- 控制台 `index.css` 里的 `@theme` 块拆到 `src/theme.css`，由 `index.css` `@import`，主样式表的编译结果不变。
- `tailwind.ts` 用 `tailwindcss` 包的 `compile()` 在浏览器里编译，输入是 `@layer theme, base, components, utilities;` 加上 `tailwindcss/theme.css`（`layer(theme)`）、`tailwindcss/utilities.css`（`layer(utilities)`）和 `theme.css`。三份样式表通过 `?raw` 引入，由 `loadStylesheet` 提供。不包含 preflight 和 base 样式，因为控制台的样式表里已经有了；生成的工具类进入同名的 cascade layer。
- 候选类名的提取方式和 Tailwind 扫描源文件一样：用空白、引号、反引号、反斜杠把源码切成 token，去重后全部交给 `build()`，不是工具类的 token 由 `build()` 丢弃。
- 编译器只创建一次。`build()` 会累积历次的候选类名，所以所有扩展共用一个 `<style data-picotera-extensions>`，每次加载都用最新结果覆盖它的内容。卸载或修改扩展时不删除旧的工具类，刷新页面后才会清掉。
- 加载器通过 `await import('@/extensions/tailwind')` 动态加载这个模块，编译器单独打成一个 chunk，只有存在扩展时才会下载。

### host

```ts
import * as Vue from 'vue'
import * as VueRouter from 'vue-router'
import * as VueQuery from '@tanstack/vue-query'
import * as UI from '@/ui'
import * as client from '@/api/client'

createExtensionHost(me: MeView): ExtensionHost => ({
  vue: Vue,
  vueRouter: VueRouter,
  vueQuery: VueQuery,
  ui: UI,
  api,
  client,
  queryKeys,
  queryClient,
  router,
  composables: { useConfirm, useSidePanel, useMe, useProvidersMap, useProjectsMap, useCurrencyContext },
  me,
})
```

- 三个库（`vue`、`vue-router`、`@tanstack/vue-query`）都暴露整个命名空间，而且必须是控制台自己用的那一份：扩展组件里调用 `useQuery` / `useRoute` 时，拿到的是控制台注入的 `QueryClient` 和 router，和控制台共享缓存。
- `client` 是 `@/api/client` 的整个命名空间，包括所有带类型的 fetcher、`invalidate*` 辅助函数和 `ApiRequestError`；`queryKeys` 是 `@/api/queryKeys` 里的 `queryKeys`。作者按控制台自己的约定使用：`useQuery({ queryKey: queryKeys.providers.all, queryFn: client.listProviders })`。
- `composables` 只放和具体页面无关的通用组合函数。调用规则和 Vue 一样：需要 `inject` 或 `useQuery` 的那几个（`useMe`、`useProvidersMap`、`useProjectsMap`、`useCurrencyContext`）只能在组件的 `setup` 里调用。
- `me` 是加载这个模块时的当前用户（扮演时是被扮演的用户）。作者靠 `me.isAdmin` 决定要不要返回只有管理员能用的页面，因为普通用户调用 admin 接口会得到 403。
- `api` 就是 `api/plugin.ts` 导出的那个 `api` 实例，自带扮演身份请求头和 oidc 的 401 跳转逻辑。
- 控制台打包的是 runtime-only 的 Vue，没有模板编译器，所以作者只能用 render function 写组件。文档中说明。

### 描述符校验（`validate.ts`）

严格校验，任何不符合的地方都抛错，不做任何容错：

- 描述符必须是普通对象（非数组、非 null），只能有 `pages`、`buttons` 两个键，都可以省略；出现其他键就报错（防止拼写错误被静默忽略）。
- `pages` 是数组，每个元素只能有 `id`、`title`、`icon`、`component` 四个键：
  - `id` 匹配 `^[a-zA-Z0-9_-]+$`、长度 1–64，并且在同一个脚本内不能重复；
  - `title` 是非空字符串；
  - `icon` 可选，有的话必须是 `iconComponents` 的键之一；
  - `component` 必须是对象或函数（Vue 组件）。
- `buttons` 是数组，每个元素只能有 `slot`、`label`、`icon`、`onClick` 四个键：
  - `slot` 必须是 `ExtensionSlot` 之一；
  - `label` 是非空字符串；
  - `icon` 的规则同上，但在 `providers.row` 插槽上是必填的（行操作只显示图标按钮）；
  - `onClick` 必须是函数。

### 页面

- 路由：`{ path: '/ext/:scriptId/:pageId', name: 'extensionPage', component: () => import('@/views/ExtensionPageView.vue') }`，不加入 `ADMIN_ROUTES`。
- `ExtensionPageView`：用 `findPage` 查找页面。如果对应条目是 `loading`，显示「加载中…」；如果是 `error`，显示「扩展「{scriptName}」加载失败：{error}」；找不到条目或页面，显示「扩展页面不存在」。找到后渲染 `<ExtensionBoundary :key="scriptId + ':' + generation"><component :is="page.component" /></ExtensionBoundary>`，脚本重新加载后会重新挂载，错误状态也随之清空。
- `ExtensionBoundary`：用 `onErrorCaptured` 捕获错误，记录错误信息并返回 `false`；有错误时显示 `StateText`「扩展渲染出错：{message}」，不再渲染子树。
- `App.vue` 的 `pageMeta`：当路由是 `extensionPage` 时，标题取页面的 `title`（页面还没加载出来时显示「扩展」），提示语取 `scriptName`。
- `AppSidebar.vue`：在最后一个分组之后（管理员是「全局」，普通用户是「用户」），如果 `pages` 不为空，就增加一个「扩展」分组，不论是否管理员都显示。每一项链接到 `{ name: 'extensionPage', params: { scriptId, pageId } }`，图标用 `icon ?? 'puzzle'`；当前路由是 `extensionPage` 且两个参数都相同时，这一项显示为激活状态。

### 按钮

`ExtensionButtons.vue`，props 是 `extensionSlot: ExtensionSlot` 和 `context`（对应插槽的上下文类型）。prop 不叫 `slot`，因为模板里的 `slot` 属性会被 eslint 的 `vue/no-deprecated-slot-attribute` 当成 Vue 2 的具名插槽语法：

- `providers.toolbar` / `models.toolbar`：每个按钮渲染成 `Button`，有 `icon` 时显示图标，后面跟 `label`。
- `providers.row`：每个按钮渲染成 `IconButton`，`label` 用作 `title` 和 `aria-label`。
- 点击时调用 `onClick(context)` 并 `await` 结果；执行期间这个按钮禁用。结束后（不管成功还是失败）都调用 `queryClient.invalidateQueries()`，因为按钮可能改动了任意资源。抛出的异常写入 store 的 `buttonError`。
- `App.vue` 在 `<RouterView />` 上方显示 `buttonError`：用 `StateText` 显示「扩展「{scriptId}」的按钮「{label}」执行失败：{message}」，旁边放一个关闭按钮把它清空。下一次按钮执行失败时直接覆盖。

插槽位置：

- `providers.toolbar`：`ProvidersView` 顶部「新增渠道」按钮的左边，上下文 `{}`。
- `providers.row`：`ProvidersView` 每一行操作按钮组的最前面，上下文 `{ provider: ProviderView }`。
- `models.toolbar`：`ModelsView` 顶部「新增模型」按钮的左边，上下文 `{}`。

### 脚本管理界面

- `ScriptForm.vue`：「源代码」改为 `Tabs` 切换的两个 `CodeEditor`，标签分别是「服务端」（`source`）和「前端」（`frontend`），提交时两者都带上。编辑已有脚本时，如果 store 里这个脚本的状态是 `error`，在编辑器上方用 `StateText` 显示加载错误。
- `ScriptsView.vue`：
  - 名称后面增加状态标记：`frontend` 非空且启用时，`ready` 显示 `Tag variant="accent"`「前端」，`error` 显示 `Tag`「前端加载失败」（`title` 为错误信息），`loading` 不显示标记。
  - 切换启用状态的 mutation 带上 `frontend`。

## 示例脚本

放在 `docs/example-scripts/`。前端部分单独存成 `*.frontend.js` 文件，和同名的服务端 `.js` 文件配合使用：

- `header-routing.js` + `header-routing.frontend.js`：
  - KV 键 `header-routing`，值是 `JSON.stringify({ rules: { [headerValue]: providerId[] } })`，与服务端 `picotera.kv.get` 的 JSON 约定一致。
  - 服务端 `sortProviders`：读取 `ctx.request.headers['x-picotera-route']` 的第一个值（`ctx.request.headers` 的键是小写的）。没有这个头，或者没有对应的规则时直接放行；命中规则时，只保留 `provider.id` 在列表里的候选。
  - 前端：只在 `host.me.isAdmin` 时返回这个页面（它要调用 admin 接口）。左栏是请求头的取值（可以新增、删除），右栏是渠道列表。用 SVG 连线表示规则：先点左栏的一个值，再点右栏的渠道，就切换这条连线。点「保存」时写入 KV。没有引入第三方库。渠道列表用 `useQuery({ queryKey: queryKeys.providers.all, queryFn: client.listProviders })` 获取；KV 用 `host.api.GET('/api/picotera/kv/{key}')` 读取，404 视为空配置（`client.getKvEntry` 不暴露状态码）；保存用 `client.upsertKvEntry`，然后调用 `client.invalidateKv(queryClient)`。
- `register-orphan-models.frontend.js`（只有前端代码，服务端源码留空）：在 `models.toolbar` 插槽加一个「注册所有未注册模型」按钮。它用 `queryClient.fetchQuery` 配合 `queryKeys.models.all` / `queryKeys.providers.all` 和 `client.listModels` / `client.listProviders` 取数据，找出所有渠道 `providerModels[].model` 里、还没有出现在模型列表中的名字。经 `composables.useConfirm().require` 确认数量后，逐个调用 `client.upsertModel({ name, disabled: false, annotations: {} })`。

## 文档

- `docs/scripting.md` 新增「控制台扩展」一节：`frontend` 字段、默认导出与 `host`（逐项列出各个字段，`client` 只列出用法并指向 `dashboard/src/api/client.ts`）、描述符和校验规则、三个插槽及上下文、错误隔离、热重载与副作用清理、只能用 render function、第三方库用绝对 URL 导入（不支持相对路径导入）、对所有登录用户加载（用 `host.me.isAdmin` 区分管理员）、前端代码会下发给所有用户（不要在里面写密钥）。「示例」一节的列表加上两个新示例。
- 根目录 `CLAUDE.md` 的 Scripts 一节增加一段，说明 `frontend` 字段、`script-frontends` 接口和加载机制；「User isolation & authorization」一节的 mgmt 列表加上 `script-frontends`。`dashboard/CLAUDE.md` 增加 `src/extensions/` 和 store 的说明，并在路由列表里加上 `/ext/:scriptId/:pageId`。
