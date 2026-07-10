# 执行计划

## 步骤 1：传递 `modelsEndpointUrl` 到 ProviderEndpointsPanel

**文件**：`dashboard/src/views/ProvidersView.vue`

在 `toggleBindings` 函数中，将当前 provider 的 `modelsEndpointUrl` 传入 `ProviderEndpointsPanel` 的 props：

```ts
function toggleBindings(p: ProviderView) {
  panel.toggle(
    ProviderEndpointsPanel,
    {
      providerId: p.id,
      providerName: p.name,
      modelsEndpointUrl: p.modelsEndpointUrl || undefined,
    },
    { key: bindingKey(p.id) },
  )
}
```

## 步骤 2：增强 `guessUpstreamUrl` fallback 逻辑

**文件**：`dashboard/src/components/ProviderEndpointsPanel.vue`

1. 在 props 定义中增加可选字段：
   ```ts
   const props = defineProps<{
     providerId: number
     providerName: string
     modelsEndpointUrl?: string
   }>()
   ```

2. 重写 `guessUpstreamUrl` 函数：
   ```ts
   function guessUpstreamUrl(endpointPath: string) {
     if (!endpointPath) return ''

     // 1. 优先从已有绑定推断
     const shortestMatchedBinding = providerEndpoints.value
       .filter((pe) => pe.upstreamUrl.endsWith(pe.endpointPath))
       .sort((a, b) => a.upstreamUrl.length - b.upstreamUrl.length)[0]

     if (shortestMatchedBinding) {
       const prefix = shortestMatchedBinding.upstreamUrl.slice(
         0,
         shortestMatchedBinding.upstreamUrl.length - shortestMatchedBinding.endpointPath.length,
       )
       return `${prefix}${endpointPath}`
     }

     // 2. fallback：从 provider 的 modelsEndpointUrl 推断
     const modelsUrl = props.modelsEndpointUrl
     if (modelsUrl) {
       if (modelsUrl.endsWith('/v1/models')) {
         return `${modelsUrl.slice(0, -'/v1/models'.length)}/${endpointPath}`
       }
       if (modelsUrl.endsWith('/models')) {
         return `${modelsUrl.slice(0, -'/models'.length)}/${endpointPath}`
       }
     }

     return endpointPath
   }
   ```

## 步骤 3：验证

运行以下命令确保 TypeScript 类型检查和 lint 通过：

```bash
pnpm --dir dashboard type-check
pnpm --dir dashboard lint
```

无新增测试文件；本改动是 UI 层面的启发式建议逻辑，无需后端或 E2E 测试。

## 预估工作量

- 文件修改：2 个 frontend 文件
- 无需后端、数据库、OpenAPI 变更
- 验证时间：< 2 分钟
