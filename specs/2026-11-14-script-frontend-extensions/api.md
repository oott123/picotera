# 接口

## 数据库

`db/migrations/053_script_frontend.sql`：

```sql
-- +goose Up
ALTER TABLE script ADD COLUMN frontend TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE script DROP COLUMN frontend;
```

`db/queries/script.sql`：

```sql
-- name: InsertScript :one
INSERT INTO script (id, name, source, frontend, enabled)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateScript :one
UPDATE script
SET id = $2, name = $3, source = $4, frontend = $5, enabled = $6, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListEnabledScriptFrontends :many
SELECT id, name, frontend FROM script
WHERE enabled = TRUE AND frontend <> ''
ORDER BY id ASC;
```

## 管理 API

### 脚本 CRUD

路径和权限都不变（admin 组）。

```go
type ScriptView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	Frontend  string `json:"frontend"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type ScriptMutateBody struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Source   string `json:"source"`
	Frontend string `json:"frontend"`
	Enabled  bool   `json:"enabled"`
}
```

`frontend` 是必填字段；空字符串表示这个脚本没有前端部分。服务端不检查它的内容。

### `GET /api/picotera/script-frontends`（mgmt 组）

`pkg/contract/script_frontend.go`：

```go
type ScriptFrontendView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Frontend string `json:"frontend"`
}

type ListScriptFrontendsResponse struct {
	Body []ScriptFrontendView
}

var OperationListScriptFrontends = huma.Operation{
	OperationID: "listScriptFrontends",
	Method:      http.MethodGet,
	Path:        "/script-frontends",
	Summary:     "List frontend modules of enabled scripts",
}
```

返回 `ListEnabledScriptFrontends` 的结果；没有数据时返回 `[]`。

## 扩展作者 API（`dashboard/src/extensions/types.ts`）

`frontend` 是一个 ES module，默认导出一个函数：

```ts
export default function (host: ExtensionHost): ExtensionDescriptor | Promise<ExtensionDescriptor>
```

```ts
import type * as Vue from 'vue'
import type * as VueRouter from 'vue-router'
import type * as VueQuery from '@tanstack/vue-query'
import type * as UI from '@/ui'
import type * as Client from '@/api/client'
import type { Component } from 'vue'
import type { ApiClient } from '@/api/plugin'
import type { queryKeys } from '@/api/queryKeys'
import type { MeView, ProviderView } from '@/api'
import type { IconName } from '@/ui/icons/paths'
import type { useConfirm } from '@/composables/useConfirm'
import type { useSidePanel } from '@/composables/useSidePanel'
import type { useMe } from '@/composables/useMe'
import type { useProvidersMap } from '@/composables/useProvidersMap'
import type { useProjectsMap } from '@/composables/useProjectsMap'
import type { useCurrencyContext } from '@/composables/useCurrencyContext'

export interface ExtensionHost {
  vue: typeof Vue
  vueRouter: typeof VueRouter
  vueQuery: typeof VueQuery
  ui: typeof UI
  api: ApiClient
  client: typeof Client
  queryKeys: typeof queryKeys
  queryClient: VueQuery.QueryClient
  router: VueRouter.Router
  composables: {
    useConfirm: typeof useConfirm
    useSidePanel: typeof useSidePanel
    useMe: typeof useMe                       // setup only
    useProvidersMap: typeof useProvidersMap   // setup only
    useProjectsMap: typeof useProjectsMap     // setup only
    useCurrencyContext: typeof useCurrencyContext // setup only
  }
  me: MeView
}

export type ExtensionSlot = 'providers.toolbar' | 'providers.row' | 'models.toolbar'

export interface ExtensionSlotContext {
  'providers.toolbar': Record<string, never>
  'providers.row': { provider: ProviderView }
  'models.toolbar': Record<string, never>
}

export interface ExtensionPage {
  id: string          // ^[a-zA-Z0-9_-]+$, 1–64, unique within the script
  title: string       // non-empty
  icon?: IconName     // sidebar icon, defaults to 'puzzle'
  component: Component
}

export type ExtensionButton = {
  [S in ExtensionSlot]: {
    slot: S
    label: string     // non-empty; tooltip on 'providers.row'
    icon?: IconName   // required on 'providers.row'
    onClick: (context: ExtensionSlotContext[S]) => void | Promise<void>
  }
}[ExtensionSlot]

export interface ExtensionDescriptor {
  pages?: ExtensionPage[]
  buttons?: ExtensionButton[]
}
```

运行时的校验规则见 `design.md`「描述符校验」。

页面路由：`/ext/:scriptId/:pageId`。

示例：

```js
export default function (host) {
  const { h, computed, defineComponent } = host.vue
  const { useQuery } = host.vueQuery
  const { DataCard, AutoDataTable, StateText } = host.ui
  const { client, queryKeys } = host

  const ProviderList = defineComponent({
    setup() {
      const q = useQuery({ queryKey: queryKeys.providers.all, queryFn: client.listProviders })
      const items = computed(() => q.data.value ?? [])
      return () =>
        q.isLoading.value
          ? h(StateText, null, () => 'Loading…')
          : h(DataCard, null, () =>
              h(AutoDataTable, {
                columns: [{ key: 'id', header: 'ID' }, { key: 'name', header: 'Name' }],
                items: items.value,
                rowKey: (p) => p.id,
              }),
            )
    },
  })

  return {
    pages: host.me.isAdmin ? [{ id: 'providers', title: 'Providers', component: ProviderList }] : [],
    buttons: [{
      slot: 'providers.row',
      label: 'Log provider',
      icon: 'activity',
      onClick: ({ provider }) => console.log(provider.id),
    }],
  }
}
```
