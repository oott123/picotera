<script setup lang="ts">
import { computed, nextTick, ref, useTemplateRef, watch } from 'vue'
import VChart from 'vue-echarts'
import { Tag } from '@/ui'
import { usePreferencesStore } from '@/stores/preferences'
import { groupColor, groupBorderColor, getThemeAxisStyle } from './colors'
import type { CallbackDataParams } from 'echarts/types/dist/shared'
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
    void nextTick(applyLegendHover)
    return
  }
  const next = new Set(hiddenKeys.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  hiddenKeys.value = next
  void nextTick(applyLegendHover)
}

function isolateSeries(key: string) {
  if (hiddenKeys.value.size === props.groups.length - 1 && !hiddenKeys.value.has(key)) {
    hiddenKeys.value = new Set()
  } else {
    const next = new Set(props.groups.map((g) => g.key))
    next.delete(key)
    hiddenKeys.value = next
  }
  void nextTick(applyLegendHover)
}

const visibleGroups = computed(() => props.groups.filter((g) => !hiddenKeys.value.has(g.key)))

// Hovering a legend item highlights its series the same way hovering the
// series itself does. Toggling visibility shifts series indices, so the
// highlight is re-applied once vue-echarts has set the new option.
const chartRef = useTemplateRef<InstanceType<typeof VChart>>('chart')
const hoveredKey = ref<string | null>(null)

function applyLegendHover() {
  const chart = chartRef.value
  if (!chart) return
  chart.dispatchAction({ type: 'downplay' })
  // A single visible group has nothing to stand out against, and focusing its
  // median line would blur the group's own boxes.
  if (boxMode.value) return
  if (hoveredKey.value === null) return
  // Series are generated from visibleGroups in order; indices rather than
  // names, since two groups may share a label.
  const seriesIndex = visibleGroups.value.findIndex((g) => g.key === hoveredKey.value)
  if (seriesIndex === -1) return
  chart.dispatchAction({ type: 'highlight', seriesIndex })
}

function onLegendEnter(key: string) {
  hoveredKey.value = key
  applyLegendHover()
}

function onLegendLeave() {
  hoveredKey.value = null
  applyLegendHover()
}

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

interface StatDatum<V> {
  value: V
  _point: BoxplotPoint
  _colorIndex: number
}

// With a single visible group the full distribution is drawn as boxes, its
// medians joined by a line; with several, boxes side by side get too crowded,
// so only the median lines remain.
const boxMode = computed(() => visibleGroups.value.length === 1)

const option = computed<EChartsOption>(() => {
  void themeVersion.value
  const axis = getThemeAxisStyle()
  const idxMap = new Map(props.groups.map((g, i) => [g.key, i]))
  const bucketLabels = props.buckets.map((b) =>
    props.bucketFormat ? props.bucketFormat(b) : defaultBucketFormat(b),
  )
  const fmtValue = (v: number, skipUnit = false) =>
    props.valueFormat ? props.valueFormat(v, skipUnit) : compactNumber(v)

  const boxSeries = boxMode.value
    ? visibleGroups.value.map((g) => {
        const originalIdx = idxMap.get(g.key) ?? 0
        const row = pointsByGroup.value.get(g.key) ?? []
        return {
          type: 'boxplot' as const,
          name: g.label || '-',
          boxWidth: [2, 24],
          data: row.map((p): StatDatum<number[]> | number[] =>
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
          },
        }
      })
    : []

  const medianSeries = visibleGroups.value.map((g) => {
    const originalIdx = idxMap.get(g.key) ?? 0
    const row = pointsByGroup.value.get(g.key) ?? []
    return {
      type: 'line' as const,
      name: g.label || '-',
      // A bucket without samples is ECharts' '-' placeholder, which breaks the
      // line.
      data: row.map((p): StatDatum<number> | '-' =>
        p ? { value: p.median, _point: p, _colorIndex: originalIdx } : '-',
      ),
      // Straight segments over the boxes, so the line doesn't suggest values
      // between buckets; smoothed on its own, like OverviewLineChart.
      smooth: !boxMode.value,
      // The boxes already mark each median; on its own the line draws every
      // point, like OverviewLineChart, so an isolated median stays visible.
      ...(boxMode.value
        ? { symbol: 'none' }
        : { symbol: 'circle', symbolSize: 3, showSymbol: true, showAllSymbol: true }),
      // Over the boxes the line takes the median stroke's color; on its own it
      // uses the fill color, like OverviewLineChart.
      lineStyle: { width: 1.5 },
      itemStyle: {
        color: boxMode.value ? groupBorderColor(originalIdx) : groupColor(originalIdx),
      },
      z: 3,
      // ECharts' default hover scale would take a 3px symbol to 6px; 4 / 3
      // keeps it at 4px.
      emphasis: { focus: 'series' as const, scale: 4 / 3 },
      blur: {
        lineStyle: { opacity: 0.3 },
        itemStyle: { opacity: 0.3 },
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
      axisPointer: { type: boxMode.value ? 'shadow' : 'line' },
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
        // In box mode a group shows up twice (box + median line); one row each.
        const seen = new Set<number>()
        const lines = arr
          .map((p) => p.data as StatDatum<unknown> | string | null | undefined)
          .filter((d): d is StatDatum<unknown> => typeof d === 'object' && d !== null)
          .filter((d) => !seen.has(d._colorIndex) && !!seen.add(d._colorIndex))
          .map((d) => {
            const s = d._point
            const name = props.groups[d._colorIndex]?.label || '-'
            return `<div class="flex items-center gap-1 text-2xs"><span style="background:${groupColor(d._colorIndex)};display:inline-block;width:8px;height:8px;border-radius:2px"></span><span class="text-ink-muted">${escape(name)}</span><span class="ml-auto pl-2 mono tabular">min ${escape(fmtValue(s.min, true))} · med ${escape(fmtValue(s.median, true))} · p99 ${escape(fmtValue(s.max))}</span><span class="pl-2 text-ink-muted mono tabular">n=${s.count}</span></div>`
          })
          .join('')
        return `<div class="min-w-32">${head}${lines}</div>`
      },
    },
    series: [...boxSeries, ...medianSeries],
  }
})
</script>

<template>
  <div class="flex flex-col gap-2">
    <div v-if="noData" class="text-2xs text-ink-muted">暂无数据</div>
    <template v-else>
      <VChart ref="chart" :option="option" :style="{ height: (height ?? 180) + 'px' }" autoresize />
      <ul class="flex flex-wrap gap-1">
        <li
          v-for="(g, i) in groups"
          :key="g.key || `__${i}`"
          class="flex items-center gap-1 cursor-pointer select-none"
          :class="{ 'opacity-30': hiddenKeys.has(g.key) }"
          @click="toggleSeries(g.key)"
          @contextmenu.prevent="isolateSeries(g.key)"
          @mouseenter="onLegendEnter(g.key)"
          @mouseleave="onLegendLeave"
        >
          <span class="h-2 w-2 shrink-0 rounded-xs" :style="{ background: colors[i] }" />
          <Tag variant="default">{{ g.label || '—' }}</Tag>
        </li>
      </ul>
    </template>
  </div>
</template>
