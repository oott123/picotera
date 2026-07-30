<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import VChart from 'vue-echarts'
import { Tag } from '@/ui'
import { usePreferencesStore } from '@/stores/preferences'
import { groupColor, getChartColors, getThemeAxisStyle } from './colors'
import type { CallbackDataParams } from 'echarts/types/dist/shared'
import type { EChartsOption } from './echarts'
import './echarts'

interface SeriesGroup {
  key: string
  label: string
}

interface SeriesPoint {
  groupKey: string
  bucketAt: string
  value: number
}

const props = defineProps<{
  groups: SeriesGroup[]
  buckets: string[]
  points: SeriesPoint[]
  height?: number
  valueFormat?: (value: number) => string
  bucketFormat?: (iso: string) => string
  /** Pins the y axis top (e.g. 1 for a 100%-share stack). Auto-scaled when omitted. */
  yMax?: number
}>()

const hiddenKeys = ref<Set<string>>(new Set())

function toggleSeries(key: string) {
  if (hiddenKeys.value.size === props.groups.length - 1 && !hiddenKeys.value.has(key)) {
    hiddenKeys.value = new Set()
    return
  }
  const next = new Set(hiddenKeys.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  hiddenKeys.value = next
}

function isolateSeries(key: string) {
  if (hiddenKeys.value.size === props.groups.length - 1 && !hiddenKeys.value.has(key)) {
    hiddenKeys.value = new Set()
  } else {
    const next = new Set(props.groups.map((g) => g.key))
    next.delete(key)
    hiddenKeys.value = next
  }
}

const visibleGroups = computed(() => props.groups.filter((g) => !hiddenKeys.value.has(g.key)))

interface Datum {
  bucket: string
  values: Record<string, number>
}

const dataset = computed<Datum[]>(() => {
  const indexByBucket = new Map<string, number>()
  props.buckets.forEach((b, i) => indexByBucket.set(b, i))
  const rows: Datum[] = props.buckets.map((b) => ({
    bucket: b,
    values: Object.fromEntries(props.groups.map((g) => [g.key, 0])),
  }))
  for (const point of props.points) {
    const idx = indexByBucket.get(point.bucketAt)
    if (idx === undefined) continue
    const row = rows[idx]
    if (!row) continue
    row.values[point.groupKey] = (row.values[point.groupKey] ?? 0) + (point.value ?? 0)
  }
  return rows
})

const colors = computed(() => props.groups.map((_, i) => groupColor(i)))

function defaultBucketFormat(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const total = props.buckets.length
  if (total <= 24) {
    return `${d.getHours().toString().padStart(2, '0')}:00`
  }
  return `${d.getMonth() + 1}/${d.getDate()}`
}

function compactNumber(v: number) {
  if (!Number.isFinite(v)) return ''
  if (Math.abs(v) >= 1e9) return `${(v / 1e9).toFixed(1)}B`
  if (Math.abs(v) >= 1e6) return `${(v / 1e6).toFixed(1)}M`
  if (Math.abs(v) >= 1e3) return `${(v / 1e3).toFixed(1)}k`
  if (Math.abs(v) >= 1) return v.toFixed(0)
  if (v === 0) return '0'
  return v.toFixed(2)
}

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
  const chartColors = getChartColors()
  const idxMap = new Map(props.groups.map((g, i) => [g.key, i]))
  const bucketLabels = props.buckets.map((b) =>
    props.bucketFormat ? props.bucketFormat(b) : defaultBucketFormat(b),
  )
  const fmtValue = (v: number) => (props.valueFormat ? props.valueFormat(v) : compactNumber(v))

  return {
    animation: false,
    grid: { left: 32, right: 8, top: 8, bottom: 24, containLabel: false },
    xAxis: {
      type: 'category',
      data: bucketLabels,
      axisLine: { lineStyle: { color: axis.axisLine } },
      axisTick: { lineStyle: { color: axis.axisTick } },
      axisLabel: { color: axis.axisLabel, fontSize: 10 },
      splitLine: { show: false },
    },
    yAxis: {
      type: 'value',
      ...(props.yMax === undefined ? {} : { max: props.yMax }),
      axisLabel: { color: axis.axisLabel, fontSize: 10, formatter: (v: number) => fmtValue(v) },
      splitLine: { lineStyle: { color: axis.splitLine } },
      axisLine: { show: false },
      axisTick: { show: false },
    },
    tooltip: {
      trigger: 'axis',
      backgroundColor: axis.tooltipBg,
      borderColor: axis.tooltipBorder,
      textStyle: { color: axis.tooltipText, fontSize: 10 },
      formatter: (params: CallbackDataParams | CallbackDataParams[]) => {
        const arr = Array.isArray(params) ? params : [params]
        if (arr.length === 0) return ''
        const bucketIdx = arr[0]!.dataIndex
        const bucket = props.buckets[bucketIdx]
        if (!bucket) return ''
        const bucketLabel = props.bucketFormat
          ? props.bucketFormat(bucket)
          : defaultBucketFormat(bucket)
        const head = `<div class="text-2xs text-ink-muted mb-1">${escape(bucketLabel)}</div>`
        const lines = arr
          .filter((p) => p.value != null && p.value !== 0)
          .map((p) => {
            const formatted = fmtValue(p.value as number)
            return `<div class="flex items-center gap-1 text-2xs"><span style="background:${p.color};display:inline-block;width:8px;height:8px;border-radius:2px"></span><span class="text-ink-muted">${escape(p.seriesName || '-')}</span><span class="ml-auto mono tabular">${formatted}</span></div>`
          })
          .join('')
        return `<div class="min-w-32">${head}${lines}</div>`
      },
    },
    color: chartColors,
    series: visibleGroups.value.map((g) => {
      const originalIdx = idxMap.get(g.key) ?? 0
      return {
        type: 'line' as const,
        name: g.label || '-',
        data: dataset.value.map((d) => d.values[g.key] ?? 0),
        stack: 'total',
        areaStyle: { opacity: 0.85 },
        smooth: true,
        symbol: 'none',
        lineStyle: { width: 1 },
        itemStyle: { color: chartColors[originalIdx] },
        emphasis: { focus: 'series' },
        blur: {
          areaStyle: { opacity: 0.25 },
        },
      }
    }),
  }
})
</script>

<template>
  <div class="flex flex-col gap-2">
    <VChart :option="option" :style="{ height: (height ?? 180) + 'px' }" autoresize />
    <ul class="flex flex-wrap gap-1">
      <li
        v-for="(g, i) in groups"
        :key="g.key || `__${i}`"
        class="flex items-center gap-1 cursor-pointer select-none"
        :class="{ 'opacity-30': hiddenKeys.has(g.key) }"
        @click="toggleSeries(g.key)"
        @contextmenu.prevent="isolateSeries(g.key)"
      >
        <span class="h-2 w-2 shrink-0 rounded-xs" :style="{ background: colors[i] }" />
        <Tag variant="default">{{ g.label || '—' }}</Tag>
      </li>
    </ul>
  </div>
</template>
