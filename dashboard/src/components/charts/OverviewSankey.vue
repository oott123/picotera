<script setup lang="ts">
import { computed, watch, ref } from 'vue'
import VChart from 'vue-echarts'
import { usePreferencesStore } from '@/stores/preferences'
import { groupColor, getThemeAxisStyle } from './colors'
import type { CallbackDataParams } from 'echarts/types/dist/shared'
import type { EChartsOption } from './echarts'
import './echarts'

export interface SankeyNode {
  id: string
  label: string
  layer: number
}

export interface SankeyLink {
  source: string
  target: string
  value: number
}

export interface SankeyLayer {
  label: string
  index: number
}

const props = defineProps<{
  nodes: SankeyNode[]
  links: SankeyLink[]
  valueFormat?: (value: number) => string
  layers?: SankeyLayer[]
  hiddenLayerIndices?: number[]
}>()

const emit = defineEmits<{
  (e: 'toggleLayer', index: number): void
}>()

const nodeTotals = computed(() => {
  const incoming = new Map<string, number>()
  const outgoing = new Map<string, number>()
  for (const link of props.links) {
    incoming.set(link.target, (incoming.get(link.target) ?? 0) + link.value)
    outgoing.set(link.source, (outgoing.get(link.source) ?? 0) + link.value)
  }
  return { incoming, outgoing }
})

const fmt = (v: number) => (props.valueFormat ? props.valueFormat(v) : String(v))

function escape(s: string) {
  return s.replace(/[&<>"']/g, (c) =>
    c === '&' ? '&amp;' : c === '<' ? '&lt;' : c === '>' ? '&gt;' : c === '"' ? '&quot;' : '&#39;',
  )
}

const prefs = usePreferencesStore()
const themeVersion = ref(0)
watch(
  () => prefs.theme,
  () => {
    themeVersion.value++
  },
)

const layerDepthMap = computed(() => {
  const presentLayers = new Set<number>()
  for (const node of props.nodes) {
    presentLayers.add(node.layer)
  }
  const sorted = [...presentLayers].sort((a, b) => a - b)
  const map = new Map<number, number>()
  sorted.forEach((layer, idx) => map.set(layer, idx))
  return map
})

const option = computed<EChartsOption>(() => {
  void themeVersion.value
  const axis = getThemeAxisStyle()
  const faintColor = getComputedStyle(document.documentElement)
    .getPropertyValue('--color-ink-faint')
    .trim()

  const nodeMap = new Map(props.nodes.map((n) => [n.id, n]))

  return {
    animation: false,
    tooltip: {
      trigger: 'item',
      triggerOn: 'mousemove',
      backgroundColor: axis.tooltipBg,
      borderColor: axis.tooltipBorder,
      textStyle: { color: axis.tooltipText, fontSize: 10 },
      formatter: (params: CallbackDataParams | CallbackDataParams[]) => {
        const p = Array.isArray(params) ? params[0]! : params
        const d = p.data as Record<string, unknown>
        if (p.dataType === 'edge') {
          const sourceName = d.source as string
          const targetName = d.target as string
          const sourceNode = nodeMap.get(sourceName)
          const targetNode = nodeMap.get(targetName)
          return `<div class="text-xs"><div class="font-medium">${escape(sourceNode?.label ?? sourceName)} → ${escape(targetNode?.label ?? targetName)}</div><div class="mono tabular text-ink-muted">${fmt(d.value as number)}</div></div>`
        }
        if (p.dataType === 'node') {
          const nodeId = p.name as string
          const node = nodeMap.get(nodeId)
          const total = Math.max(
            nodeTotals.value.incoming.get(nodeId) ?? 0,
            nodeTotals.value.outgoing.get(nodeId) ?? 0,
          )
          return `<div class="text-xs"><div class="font-medium">${escape(node?.label ?? nodeId)}</div><div class="mono tabular text-ink-muted">${fmt(total)}</div></div>`
        }
        return ''
      },
    },
    series: [
      {
        type: 'sankey',
        nodeGap: 14,
        nodeWidth: 14,
        layoutIterations: 32,
        label: {
          show: true,
          color: axis.axisLabel,
          fontSize: 10,
          fontFamily: 'Geist, Geist Fallback, ui-sans-serif, system-ui, sans-serif',
          formatter: (params) => {
            return nodeMap.get(params.name)?.label ?? params.name
          },
        },
        lineStyle: { color: 'gradient', opacity: 0.2 },
        data: props.nodes.map((n) => ({
          name: n.id,
          depth: layerDepthMap.value.get(n.layer) ?? n.layer,
          itemStyle: {
            color: n.id.startsWith('__other__') ? faintColor : groupColor(n.layer),
          },
        })),
        links: props.links.map((l) => ({
          source: l.source,
          target: l.target,
          value: l.value,
        })),
        emphasis: { focus: 'trajectory' },
        blur: {
          label: { opacity: 0.4 },
          itemStyle: { opacity: 0.4 },
          lineStyle: { opacity: 0.08 },
        },
      },
    ],
  }
})
</script>

<template>
  <div>
    <VChart :option="option" style="height: 288px" autoresize />
    <div
      v-if="layers && layers.length > 0"
      class="flex flex-wrap items-center justify-center gap-3 mt-2"
    >
      <button
        v-for="layer in layers"
        :key="layer.index"
        class="flex items-center gap-1.5 text-xs transition-opacity"
        :class="[
          layer.index === 0 ? 'cursor-default' : 'cursor-pointer',
          (hiddenLayerIndices ?? []).includes(layer.index)
            ? 'opacity-40 line-through'
            : 'opacity-100',
        ]"
        :disabled="layer.index === 0"
        @click="layer.index > 0 && emit('toggleLayer', layer.index)"
      >
        <span
          class="w-3 h-3 rounded-full shrink-0"
          :style="{ backgroundColor: groupColor(layer.index) }"
        />
        <span>{{ layer.label }}</span>
      </button>
    </div>
  </div>
</template>
