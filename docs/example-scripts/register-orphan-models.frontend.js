// 一键注册所有未注册模型（只有前端部分，服务端源码留空）
//
// 把这个文件的内容粘贴到一个脚本的「前端」标签页。模型页顶部会出现「注册所有未注册模型」按钮：
// 它找出渠道模型列表里出现过、但还没注册成模型的名字，确认后逐个注册（启用、不带注解）。

export default function (host) {
  const { client, queryKeys, queryClient } = host

  async function registerOrphans() {
    const [models, providers] = await Promise.all([
      queryClient.fetchQuery({ queryKey: queryKeys.models.all, queryFn: client.listModels }),
      queryClient.fetchQuery({ queryKey: queryKeys.providers.all, queryFn: client.listProviders }),
    ])
    const registered = new Set(models.map((m) => m.name))
    const orphans = new Set()
    for (const p of providers) {
      for (const entry of p.providerModels ?? []) {
        if (entry.model && !registered.has(entry.model)) orphans.add(entry.model)
      }
    }
    if (orphans.size === 0) {
      window.alert('没有未注册的模型')
      return
    }

    host.composables.useConfirm().require({
      message: `确定要注册 ${orphans.size} 个模型吗？`,
      accept: async () => {
        for (const name of [...orphans].sort()) {
          await client.upsertModel({ name, disabled: false, annotations: {} })
        }
        // 按钮本身在确认框弹出时就已经执行完了，所以这里要自己刷新模型列表
        client.invalidateModels(queryClient)
      },
    })
  }

  return {
    buttons: [
      {
        slot: 'models.toolbar',
        label: '注册所有未注册模型',
        icon: 'plus',
        onClick: registerOrphans,
      },
    ],
  }
}
