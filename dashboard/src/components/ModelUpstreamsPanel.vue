<script setup lang="ts">
import { computed } from 'vue'
import { SidePanel, Button, StateText, Tag, Icon } from '@/ui'

export type Upstream = {
  providerId: number
  providerName: string
  upstreamModelName: string
  endpointPaths: string[]
  priority: number
  providerPriority: number
  expandedFromProvider: boolean
  providerDisabled: boolean
  entryDisabled: boolean
}

const props = defineProps<{
  modelName: string
  modelDisabled: boolean
  upstreams: Upstream[]
  endpointNames?: Record<string, string>
}>()
const emit = defineEmits<{ close: [] }>()

type Group = { paths: string[]; upstreams: Upstream[] }

function sortUpstreams(list: Upstream[]): Upstream[] {
  return [...list].sort((a, b) => {
    const score = b.priority + b.providerPriority - (a.priority + a.providerPriority)
    if (score !== 0) return score
    if (a.providerId !== b.providerId) return b.providerId - a.providerId
    return a.upstreamModelName.localeCompare(b.upstreamModelName)
  })
}

function signatureOf(list: Upstream[]): string {
  return list
    .map((u) =>
      [
        u.providerId,
        u.upstreamModelName,
        u.priority,
        u.providerPriority,
        u.providerDisabled ? 1 : 0,
        u.entryDisabled ? 1 : 0,
      ].join('|'),
    )
    .join('||')
}

const groups = computed<Group[]>(() => {
  const byPath = new Map<string, Upstream[]>()

  for (const u of props.upstreams) {
    if (!u.endpointPaths.length) continue
    for (const p of u.endpointPaths) {
      const list = byPath.get(p)
      if (list) list.push(u)
      else byPath.set(p, [u])
    }
  }

  const bySig = new Map<string, Group>()
  for (const [path, list] of byPath) {
    const sorted = sortUpstreams(list)
    const sig = signatureOf(sorted)
    const existing = bySig.get(sig)
    if (existing) existing.paths.push(path)
    else bySig.set(sig, { paths: [path], upstreams: sorted })
  }

  const result = Array.from(bySig.values())
  for (const g of result) g.paths.sort()
  result.sort((a, b) => (a.paths[0] ?? '').localeCompare(b.paths[0] ?? ''))

  return result
})

const mergedUpstreams = computed(() =>
  [...props.upstreams].sort((a, b) => {
    const score = b.priority + b.providerPriority - (a.priority + a.providerPriority)
    if (score !== 0) return score
    if (a.providerId !== b.providerId) return b.providerId - a.providerId
    return a.upstreamModelName.localeCompare(b.upstreamModelName)
  }),
)
</script>

<template>
  <SidePanel :title="modelName" kicker="上游" @close="emit('close')">
    <div class="flex flex-col gap-3">
      <section v-if="mergedUpstreams.length" class="flex flex-col gap-2">
        <div class="flex items-baseline justify-between">
          <span class="text-xs font-medium text-ink-muted uppercase tracking-[0.03em]"> 聚合 </span>
          <span class="text-xs text-ink-faint tabular-nums">{{ mergedUpstreams.length }} 上游</span>
        </div>
        <ul
          class="list-none m-0 p-0 flex flex-col border border-line rounded-md bg-surface-0 overflow-hidden"
        >
          <li
            v-for="(u, i) in mergedUpstreams"
            :key="`merged:${u.providerId}:${i}`"
            class="px-2.5 py-2 border-t border-line-soft first:border-t-0 flex items-center gap-1.5 flex-wrap"
            :class="u.providerDisabled || u.entryDisabled ? 'opacity-55' : ''"
          >
            <span class="text-sm font-semibold text-ink">{{ u.providerName }}</span>
            <Tag v-if="u.providerDisabled" variant="muted">渠道已禁用</Tag>
            <Icon name="chevron-down" :size="12" class="-rotate-90 text-ink-faint" />
            <Tag variant="accent">{{ u.upstreamModelName }}</Tag>
            <Tag v-if="u.providerPriority + u.priority != 0" variant="more">
              P{{ u.providerPriority + u.priority }}
            </Tag>
            <Tag v-if="u.entryDisabled" variant="muted">上游已禁用</Tag>
          </li>
        </ul>
      </section>
      <section class="flex flex-col gap-2">
        <div class="flex items-baseline justify-between">
          <span class="text-xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >按端点分组</span
          >
          <span class="text-xs text-ink-faint tabular-nums"> {{ groups.length }} 端点 </span>
        </div>
        <div
          v-if="modelDisabled"
          class="px-2.5 py-1.5 border border-line rounded-md bg-surface-50 flex items-center gap-1.5 text-xs text-ink-muted"
        >
          <Icon name="puzzle-off" :size="13" />
          <span>此模型已禁用，所有上游均不参与调度</span>
        </div>
        <StateText v-if="!groups.length" compact>
          {{ upstreams.length ? '该模型的上游所属渠道均未绑定端点，无法路由' : '该模型暂无上游' }}
        </StateText>
        <ul v-else class="list-none m-0 p-0 flex flex-col gap-2">
          <li
            v-for="(group, gi) in groups"
            :key="gi"
            class="border border-line rounded-md bg-surface-0 overflow-hidden"
          >
            <header
              class="px-2.5 py-1.5 bg-surface-50 border-b border-line flex items-baseline gap-2"
            >
              <span
                class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em] flex-none"
              >
                {{ group.paths.length > 1 ? `端点 · ${group.paths.length}` : '端点' }}
              </span>
              <div class="flex flex-col gap-0.5 min-w-0">
                <div v-for="p in group.paths" :key="p" class="flex items-baseline gap-1.5 min-w-0">
                  <span
                    class="text-sm font-medium text-ink truncate"
                    :title="endpointNames?.[p] ?? p"
                    >{{ endpointNames?.[p] ?? p }}</span
                  >
                  <code
                    v-if="endpointNames?.[p]"
                    class="font-mono text-2xs text-ink-faint truncate"
                    :title="p"
                    >{{ p }}</code
                  >
                </div>
              </div>
            </header>
            <ul class="list-none m-0 p-0 flex flex-col">
              <li
                v-for="(u, i) in group.upstreams"
                :key="`${gi}:${u.providerId}:${i}`"
                class="px-2.5 py-2 border-t border-line-soft first:border-t-0 flex items-center gap-1.5 flex-wrap"
                :class="u.providerDisabled || u.entryDisabled ? 'opacity-55' : ''"
              >
                <span class="text-sm font-semibold text-ink">{{ u.providerName }}</span>
                <Tag v-if="u.providerDisabled" variant="muted">渠道已禁用</Tag>
                <Icon name="chevron-down" :size="12" class="-rotate-90 text-ink-faint" />
                <Tag variant="accent">{{ u.upstreamModelName }}</Tag>
                <Tag v-if="u.providerPriority + u.priority != 0" variant="more">
                  P{{ u.providerPriority + u.priority }}
                </Tag>
                <Tag v-if="u.entryDisabled" variant="muted">上游已禁用</Tag>
              </li>
            </ul>
          </li>
        </ul>
      </section>
    </div>

    <template #footer>
      <Button variant="ghost" @click="emit('close')">关闭</Button>
    </template>
  </SidePanel>
</template>
