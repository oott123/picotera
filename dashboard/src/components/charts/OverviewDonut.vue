<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import VChart from 'vue-echarts'
import { Tag } from '@/ui'
import { usePreferencesStore } from '@/stores/preferences'
import { groupColor, getThemeAxisStyle } from './colors'
import type { CallbackDataParams } from 'echarts/types/dist/shared'
import type { EChartsOption } from './echarts'
import './echarts'

interface DonutDatum {
  key: string
  label: string
  value: number
}

const props = defineProps<{
  data: DonutDatum[]
  centralLabel?: string
  centralSubLabel?: string
  valueFormat?: (value: number, datum: DonutDatum) => string
}>()

const hiddenKeys = ref<Set<string>>(new Set())

function toggleSeries(key: string) {
  if (hiddenKeys.value.size === props.data.length - 1 && !hiddenKeys.value.has(key)) {
    hiddenKeys.value = new Set()
    return
  }
  const next = new Set(hiddenKeys.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  hiddenKeys.value = next
}

function isolateSeries(key: string) {
  if (hiddenKeys.value.size === props.data.length - 1 && !hiddenKeys.value.has(key)) {
    hiddenKeys.value = new Set()
  } else {
    const next = new Set(props.data.map((d) => d.key))
    next.delete(key)
    hiddenKeys.value = next
  }
}

const total = computed(() => props.data.reduce((acc, d) => acc + (d.value ?? 0), 0))

const colored = computed(() => props.data.map((d, i) => ({ ...d, _color: groupColor(i) })))

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

const option = computed<EChartsOption>(() => {
  void themeVersion.value
  const axis = getThemeAxisStyle()
  const visibleData = props.data
    .map((d, i) => ({ ...d, originalIndex: i }))
    .filter((d) => !hiddenKeys.value.has(d.key))

  const graphic: Record<string, unknown>[] = []
  if (props.centralLabel || props.centralSubLabel) {
    const inkColor = axis.tooltipText
    const mutedColor = axis.axisLabel
    if (props.centralLabel) {
      graphic.push({
        type: 'text',
        left: 'center',
        top: '43%',
        style: {
          text: props.centralLabel,
          fill: inkColor,
          fontSize: 16,
          fontWeight: 600,
          textAlign: 'center',
          fontFamily: 'Geist, Geist Fallback, ui-sans-serif, system-ui, sans-serif',
        },
      })
    }
    if (props.centralSubLabel) {
      graphic.push({
        type: 'text',
        left: 'center',
        top: '53%',
        style: {
          text: props.centralSubLabel,
          fill: mutedColor,
          fontSize: 10,
          textAlign: 'center',
          fontFamily: 'Geist, Geist Fallback, ui-sans-serif, system-ui, sans-serif',
        },
      })
    }
  }

  return {
    animation: false,
    graphic,
    tooltip: {
      trigger: 'item',
      backgroundColor: axis.tooltipBg,
      borderColor: axis.tooltipBorder,
      textStyle: { color: axis.tooltipText, fontSize: 10 },
      formatter: (params: CallbackDataParams | CallbackDataParams[]) => {
        const p = Array.isArray(params) ? params[0]! : params
        const d = p.data as Record<string, unknown> | undefined
        if (!d) return ''
        const value = (d.value as number) ?? 0
        const datum = d._datum as DonutDatum
        const formatted = props.valueFormat ? props.valueFormat(value, datum) : String(value)
        const pct = total.value === 0 ? '0' : ((value / total.value) * 100).toFixed(1)
        return `<div class="text-xs"><div class="font-medium">${escape(d.name as string)}</div><div class="mono tabular text-ink-muted">${formatted} · ${pct}%</div></div>`
      },
    },
    series: [
      {
        type: 'pie',
        radius: ['60%', '75%'],
        label: { show: true },
        itemStyle: { borderRadius: 2 },
        padAngle: 0.6,
        data: visibleData.map((d) => ({
          name: d.label,
          value: d.value,
          _datum: d,
          itemStyle: { color: groupColor(d.originalIndex) },
        })),
      },
    ],
  }
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <VChart :option="option" style="height: 180px" autoresize />
    <ul class="flex flex-wrap gap-1">
      <li
        v-for="d in colored"
        :key="d.key"
        class="flex items-center gap-1 cursor-pointer select-none"
        :class="{ 'opacity-30': hiddenKeys.has(d.key) }"
        @click="toggleSeries(d.key)"
        @contextmenu.prevent="isolateSeries(d.key)"
      >
        <span class="h-2 w-2 shrink-0 rounded-xs" :style="{ background: d._color }" />
        <Tag variant="default">{{ d.label }}</Tag>
        <span class="mono tabular text-ink-muted text-2xs">
          {{ valueFormat ? valueFormat(d.value, d) : d.value }}
        </span>
      </li>
    </ul>
  </div>
</template>
