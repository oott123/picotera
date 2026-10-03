<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import VChart from 'vue-echarts'
import { Tag } from '@/ui'
import { usePreferencesStore } from '@/stores/preferences'
import { groupColor, groupBorderColor, getThemeAxisStyle } from './colors'
import type { CallbackDataParams, CustomSeriesRenderItemAPI } from 'echarts/types/dist/shared'
import type { EChartsOption } from './echarts'
import './echarts'

interface SeriesGroup {
  key: string
  label: string
}

interface BoxplotPoint {
  groupKey: string
  bucketAt: string
  min: number
  p25: number
  median: number
  p95: number
  max: number
  count: number
}

const props = defineProps<{
  groups: SeriesGroup[]
  buckets: string[]
  points: BoxplotPoint[]
  height?: number
  valueFormat?: (value: number, skipUnit?: boolean) => string
  bucketFormat?: (iso: string) => string
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

// Points indexed per group, aligned to `buckets`; a bucket without samples
// stays undefined and becomes ECharts' '-' placeholder (no box drawn).
const pointsByGroup = computed(() => {
  const indexByBucket = new Map<string, number>()
  props.buckets.forEach((b, i) => indexByBucket.set(b, i))
  const out = new Map<string, (BoxplotPoint | undefined)[]>(
    props.groups.map((g) => [g.key, props.buckets.map(() => undefined)]),
  )
  for (const point of props.points) {
    const idx = indexByBucket.get(point.bucketAt)
    if (idx === undefined) continue
    const row = out.get(point.groupKey)
    if (!row) continue
    row[idx] = point
  }
  return out
})

const noData = computed(() => props.points.length === 0)

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

// ECharts' empty-data placeholder: a bucket without samples draws no box. The
// boxplot typings only admit number arrays, hence the cast.
const EMPTY_BOX = '-' as unknown as number[]

interface BoxDatum {
  value: number[]
  _point: BoxplotPoint
  _colorIndex: number
}

// Hover focus is tracked here instead of via ECharts' emphasis.focus: a box and
// its median line are separate series, and focus: 'series' would fade the
// line of the very group being hovered.
const focusedKey = ref<string | null>(null)

function onChartMouseover(params: { seriesType?: string; seriesIndex?: number }) {
  if (params.seriesType !== 'boxplot' || params.seriesIndex === undefined) return
  focusedKey.value = visibleGroups.value[params.seriesIndex]?.key ?? null
}

function clearFocus() {
  focusedKey.value = null
}

// Horizontal offset of the k-th of n boxplot series from its category center,
// mirroring ECharts' boxplotLayout (calculateBase) so a median line runs
// through the centers of its own boxes.
function boxOffset(bandWidth: number, k: number, n: number) {
  const available = bandWidth * 0.8 - 2
  const gap = (available / n) * 0.3
  const width = (available - gap * (n - 1)) / n
  return width / 2 - available / 2 + k * (gap + width)
}

const option = computed<EChartsOption>(() => {
  void themeVersion.value
  const axis = getThemeAxisStyle()
  const idxMap = new Map(props.groups.map((g, i) => [g.key, i]))
  const bucketLabels = props.buckets.map((b) =>
    props.bucketFormat ? props.bucketFormat(b) : defaultBucketFormat(b),
  )
  const fmtValue = (v: number, skipUnit = false) =>
    props.valueFormat ? props.valueFormat(v, skipUnit) : compactNumber(v)

  const n = visibleGroups.value.length
  const opacityOf = (key: string) =>
    focusedKey.value !== null && focusedKey.value !== key ? 0.3 : 1

  const boxSeries = visibleGroups.value.map((g) => {
    const originalIdx = idxMap.get(g.key) ?? 0
    const row = pointsByGroup.value.get(g.key) ?? []
    return {
      type: 'boxplot' as const,
      name: g.label || '-',
      boxWidth: [2, 24],
      data: row.map((p): BoxDatum | number[] =>
        p
          ? {
              value: [p.min, p.p25, p.median, p.p95, p.max],
              _point: p,
              _colorIndex: originalIdx,
            }
          : EMPTY_BOX,
      ),
      itemStyle: {
        color: groupColor(originalIdx),
        borderColor: groupBorderColor(originalIdx),
        opacity: opacityOf(g.key),
      },
    }
  })

  // One custom series per group draws the whole median polyline from a single
  // placeholder datum; buckets without samples are skipped, so the line joins
  // the neighbouring boxes across the gap.
  const medianLineSeries = visibleGroups.value.map((g, k) => {
    const originalIdx = idxMap.get(g.key) ?? 0
    const row = pointsByGroup.value.get(g.key) ?? []
    return {
      type: 'custom' as const,
      name: g.label || '-',
      silent: true,
      tooltip: { show: false },
      z: 3,
      clip: true,
      data: [[0, 0]],
      renderItem: (_params: unknown, api: CustomSeriesRenderItemAPI) => {
        const offset = boxOffset((api.size!([1, 0]) as number[])[0]!, k, n)
        const points: number[][] = []
        row.forEach((p, i) => {
          if (!p) return
          const [x, y] = api.coord([i, p.median])
          points.push([x! + offset, y!])
        })
        if (points.length < 2) return null
        return {
          type: 'polyline' as const,
          shape: { points },
          style: {
            stroke: groupBorderColor(originalIdx),
            lineWidth: 1.5,
            fill: 'none',
            opacity: opacityOf(g.key),
          },
        }
      },
    }
  })

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
      axisLabel: { color: axis.axisLabel, fontSize: 10, formatter: (v: number) => fmtValue(v) },
      splitLine: { lineStyle: { color: axis.splitLine } },
      axisLine: { show: false },
      axisTick: { show: false },
    },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      backgroundColor: axis.tooltipBg,
      borderColor: axis.tooltipBorder,
      textStyle: { color: axis.tooltipText, fontSize: 10 },
      formatter: (params: CallbackDataParams | CallbackDataParams[]) => {
        const arr = Array.isArray(params) ? params : [params]
        if (arr.length === 0) return ''
        const bucket = props.buckets[arr[0]!.dataIndex]
        if (!bucket) return ''
        const bucketLabel = props.bucketFormat
          ? props.bucketFormat(bucket)
          : defaultBucketFormat(bucket)
        const head = `<div class="text-2xs text-ink-muted mb-1">${escape(bucketLabel)}</div>`
        const lines = arr
          .map((p) => p.data as BoxDatum | string | undefined)
          .filter((d): d is BoxDatum => typeof d === 'object' && d !== null)
          .map((d) => {
            const s = d._point
            const name = props.groups[d._colorIndex]?.label || '-'
            return `<div class="flex items-center gap-1 text-2xs"><span style="background:${groupColor(d._colorIndex)};display:inline-block;width:8px;height:8px;border-radius:2px"></span><span class="text-ink-muted">${escape(name)}</span><span class="ml-auto pl-2 mono tabular">min ${escape(fmtValue(s.min, true))} · med ${escape(fmtValue(s.median, true))} · p99 ${escape(fmtValue(s.max))}</span><span class="pl-2 text-ink-muted mono tabular">n=${s.count}</span></div>`
          })
          .join('')
        return `<div class="min-w-32">${head}${lines}</div>`
      },
    },
    // Boxplot series come first so their seriesIndex equals the position in
    // visibleGroups, which both boxOffset and onChartMouseover rely on.
    series: [...boxSeries, ...medianLineSeries],
  }
})
</script>

<template>
  <div class="flex flex-col gap-2">
    <div v-if="noData" class="text-2xs text-ink-muted">暂无数据</div>
    <template v-else>
      <VChart
        :option="option"
        :style="{ height: (height ?? 180) + 'px' }"
        autoresize
        @mouseover="onChartMouseover"
        @mouseout="clearFocus"
        @globalout="clearFocus"
      />
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
    </template>
  </div>
</template>
