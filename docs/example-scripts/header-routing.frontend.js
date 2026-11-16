// 按请求头分流到指定渠道（前端部分）
//
// 配合 header-routing.js 使用：把这个文件的内容粘贴到同一个脚本的「前端」标签页。
// 侧边栏「扩展」分组里会出现「请求头分流」页面：左栏是请求头 X-Picotera-Route 的取值，右栏是渠道。
// 先点左栏的一个取值，再点右栏的渠道，就会连上（或断开）这条线；点「保存」写入 KV。
// 页面要调用管理员接口，所以只对管理员显示。

const KV_KEY = 'header-routing'
const ROW_HEIGHT = 36
const ROW_GAP = 8
const LINK_WIDTH = 160

export default function (host) {
  if (!host.me.isAdmin) return {}

  const { h, ref, computed, watch, defineComponent } = host.vue
  const { useQuery } = host.vueQuery
  const { Button, IconButton, Icon, Input, DataCard, StateText } = host.ui
  const { api, client, queryKeys, queryClient } = host

  // client.getKvEntry 不暴露状态码，这里直接用 api 区分「键不存在」
  async function loadRules() {
    const { data, error, response } = await api.GET('/api/picotera/kv/{key}', {
      params: { path: { key: KV_KEY } },
    })
    if (response.status === 404) return {}
    if (error) throw new client.ApiRequestError(error, '加载分流规则失败')
    const { rules } = JSON.parse(data.value)
    if (typeof rules !== 'object' || rules === null || Array.isArray(rules)) {
      throw new Error(`KV 键 ${KV_KEY} 的 rules 必须是对象`)
    }
    return rules
  }

  function rowTop(index) {
    return index * (ROW_HEIGHT + ROW_GAP)
  }

  // 行高决定连线的端点位置，所以用像素写在内联样式里；其余样式用 Tailwind 类名
  function rowClass(active) {
    return [
      'flex items-center gap-2 px-2.5 rounded-md border text-sm text-ink cursor-pointer box-border',
      active ? 'border-accent bg-accent-faint' : 'border-line bg-surface-0 hover:bg-surface-50',
    ]
  }

  function columnStyle(rows) {
    return { gap: `${ROW_GAP}px`, minHeight: `${Math.max(rows, 1) * (ROW_HEIGHT + ROW_GAP) - ROW_GAP}px` }
  }

  const HeaderRouting = defineComponent({
    setup() {
      const rulesQuery = useQuery({ queryKey: queryKeys.kv.detail(KV_KEY), queryFn: loadRules })
      const providersQuery = useQuery({
        queryKey: queryKeys.providers.all,
        queryFn: client.listProviders,
      })

      // 编辑中的规则；第一次拿到 KV 数据时初始化
      const draft = ref(null)
      watch(
        () => rulesQuery.data.value,
        (rules) => {
          if (draft.value === null && rules) draft.value = JSON.parse(JSON.stringify(rules))
        },
        { immediate: true },
      )

      const providers = computed(() =>
        [...(providersQuery.data.value ?? [])].sort((a, b) => a.id - b.id),
      )
      const values = computed(() => Object.keys(draft.value ?? {}))
      const selected = ref(null)
      const newValue = ref('')
      const message = ref('')
      const saving = ref(false)
      const dirty = computed(
        () => JSON.stringify(draft.value) !== JSON.stringify(rulesQuery.data.value),
      )

      function addValue() {
        const value = newValue.value
        if (value === '') {
          message.value = '取值不能为空'
          return
        }
        if (Object.hasOwn(draft.value, value)) {
          message.value = `取值「${value}」已存在`
          return
        }
        draft.value[value] = []
        selected.value = value
        newValue.value = ''
        message.value = ''
      }

      function removeValue(value) {
        delete draft.value[value]
        if (selected.value === value) selected.value = null
      }

      function toggleLink(providerId) {
        if (selected.value === null) {
          message.value = '先在左栏选择一个取值'
          return
        }
        const ids = draft.value[selected.value]
        const index = ids.indexOf(providerId)
        if (index === -1) ids.push(providerId)
        else ids.splice(index, 1)
        message.value = ''
      }

      async function save() {
        saving.value = true
        message.value = ''
        try {
          await client.upsertKvEntry(KV_KEY, { value: JSON.stringify({ rules: draft.value }) })
          client.invalidateKv(queryClient)
        } catch (e) {
          message.value = e instanceof Error ? e.message : String(e)
        } finally {
          saving.value = false
        }
      }

      function renderLinks() {
        const providerIndex = new Map(providers.value.map((p, i) => [p.id, i]))
        const rows = Math.max(values.value.length, providers.value.length, 1)
        const height = rows * (ROW_HEIGHT + ROW_GAP) - ROW_GAP
        const lines = []
        values.value.forEach((value, i) => {
          for (const id of draft.value[value]) {
            const j = providerIndex.get(id)
            if (j === undefined) continue
            const y1 = rowTop(i) + ROW_HEIGHT / 2
            const y2 = rowTop(j) + ROW_HEIGHT / 2
            lines.push(
              h('path', {
                key: `${value}:${id}`,
                d: `M 0 ${y1} C ${LINK_WIDTH / 2} ${y1}, ${LINK_WIDTH / 2} ${y2}, ${LINK_WIDTH} ${y2}`,
                fill: 'none',
                stroke: value === selected.value ? 'var(--color-accent)' : 'var(--color-ink-faint)',
                'stroke-width': value === selected.value ? 2 : 1.25,
              }),
            )
          }
        })
        return h('svg', { width: LINK_WIDTH, height, class: 'flex-none' }, lines)
      }

      function renderValues() {
        return h(
          'div',
          { class: 'flex flex-col flex-1 min-w-0', style: columnStyle(values.value.length) },
          values.value.map((value) =>
            h(
              'div',
              {
                key: value,
                class: rowClass(value === selected.value),
                style: { height: `${ROW_HEIGHT}px` },
                onClick: () => (selected.value = value === selected.value ? null : value),
              },
              [
                h('span', { class: 'flex-1 font-mono' }, value),
                h(
                  IconButton,
                  {
                    size: 'sm',
                    variant: 'danger',
                    title: '删除取值',
                    'aria-label': '删除取值',
                    onClick: (e) => {
                      e.stopPropagation()
                      removeValue(value)
                    },
                  },
                  () => h(Icon, { name: 'trash', size: 12 }),
                ),
              ],
            ),
          ),
        )
      }

      function renderProviders() {
        const linked = new Set(selected.value === null ? [] : draft.value[selected.value])
        return h(
          'div',
          { class: 'flex flex-col flex-1 min-w-0', style: columnStyle(providers.value.length) },
          providers.value.map((p) =>
            h(
              'div',
              {
                key: p.id,
                class: rowClass(linked.has(p.id)),
                style: { height: `${ROW_HEIGHT}px` },
                onClick: () => toggleLink(p.id),
              },
              [
                h('span', { class: 'text-ink-faint tabular-nums' }, `#${p.id}`),
                h('span', null, p.name),
              ],
            ),
          ),
        )
      }

      return () => {
        if (rulesQuery.isError.value) {
          return h(StateText, { dashed: false }, () => rulesQuery.error.value.message)
        }
        if (draft.value === null || providersQuery.isLoading.value) {
          return h(StateText, null, () => '加载中…')
        }
        return h('div', { class: 'flex flex-col gap-3.5' }, [
          h('div', { class: 'flex items-center gap-2' }, [
            h(Input, {
              modelValue: newValue.value,
              'onUpdate:modelValue': (v) => (newValue.value = v),
              placeholder: 'X-Picotera-Route 的取值',
              mono: true,
              class: 'w-60',
              onKeydown: (e) => e.key === 'Enter' && addValue(),
            }),
            h(Button, { variant: 'ghost', onClick: addValue }, () => '添加取值'),
            h('span', { class: 'flex-1 text-sm text-ink-faint' }, message.value),
            h(
              Button,
              { disabled: !dirty.value || saving.value, onClick: save },
              () => (saving.value ? '保存中…' : '保存'),
            ),
          ]),
          h(DataCard, null, () =>
            h('div', { class: 'flex p-4' }, [
              renderValues(),
              renderLinks(),
              renderProviders(),
            ]),
          ),
        ])
      }
    },
  })

  return {
    pages: [{ id: 'header-routing', title: '请求头分流', icon: 'route', component: HeaderRouting }],
  }
}
