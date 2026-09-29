<script setup lang="ts">
import { computed, watch, ref } from 'vue'
import VChart from 'vue-echarts'
import { usePreferencesStore } from '@/stores/preferences'
import { groupColor, groupBorderColor, getThemeAxisStyle } from './colors'
import type { CallbackDataParams } from 'echarts/types/dist/shared'
import type { EChartsOption } from './echarts'
import './echarts'

interface BoxplotItem {
  key: string
  label: string
  min: number
  p25: number
  median: number
  p95: number
  max: number
  count: number
}

const props = defineProps<{
  items: BoxplotItem[]
  height?: number
  valueFormat?: (value: number) => string
}>()

interface GroupStats {
  label: string
  min: number
  p25: number
  median: number
  p95: number
  max: number
  count: number
  hasData: boolean
  colorIndex: number
}

const groupStats = computed<GroupStats[]>(() => {
  return props.items.map((item, i) => {
    const values = [item.min, item.p25, item.median, item.p95, item.max]
    const hasData = item.count > 0 && values.every(Number.isFinite)
    if (!hasData) {
      return {
        label: item.label || '-',
        min: 0,
        p25: 0,
        median: 0,
        p95: 0,
        max: 0,
        count: item.count,
        hasData: false,
        colorIndex: i,
      }
    }
    return {
      label: item.label || '-',
      min: item.min,
      p25: item.p25,
      median: item.median,
      p95: item.p95,
      max: item.max,
      count: item.count,
      hasData: true,
      colorIndex: i,
    }
  })
})

const noData = computed(() => groupStats.value.every((s) => !s.hasData))

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
  const fmtValue =
    props.valueFormat ??
    ((v: number, skipUnit = false) => `${v.toFixed(0)}${skipUnit ? '' : ' tok/s'}`)
  const reversed = [...groupStats.value].reverse()
  const labels = reversed.map((s) => s.label)

  return {
    animation: false,
    grid: { left: 8, right: 16, top: 8, bottom: 24, containLabel: true },
    yAxis: {
      type: 'category',
      data: labels,
      axisLine: { show: false },
      axisTick: { show: false },
      axisLabel: { color: axis.axisLabel, fontSize: 10 },
    },
    xAxis: {
      type: 'value',
      axisLabel: { color: axis.axisLabel, fontSize: 10, formatter: (v: number) => fmtValue(v) },
      splitLine: { lineStyle: { color: axis.splitLine } },
      axisLine: { show: false },
      axisTick: { show: false },
    },
    tooltip: {
      trigger: 'item',
      backgroundColor: axis.tooltipBg,
      borderColor: axis.tooltipBorder,
      textStyle: { color: axis.tooltipText, fontSize: 10 },
      formatter: (params: CallbackDataParams | CallbackDataParams[]) => {
        const p = Array.isArray(params) ? params[0]! : params
        const d = p.data as Record<string, unknown> | undefined
        if (!d) return ''
        const stat = d._stat as GroupStats
        const color = groupColor(stat.colorIndex)
        return `<div class="min-w-32">
          <div class="text-2xs text-ink-muted mb-1">${escape(stat.label)}</div>
          <div class="flex items-center gap-1 text-2xs">
            <span style="background:${color};display:inline-block;width:8px;height:8px;border-radius:2px"></span>
            <span class="mono tabular">min ${escape(fmtValue(stat.min, true))} · med ${escape(fmtValue(stat.median, true))} · p99 ${escape(fmtValue(stat.max))}</span>
          </div>
          <div class="text-2xs text-ink-muted mt-1">n=${stat.count}</div>
        </div>`
      },
    },
    series: [
      {
        type: 'boxplot',
        emphasis: { focus: 'self' },
        blur: {
          itemStyle: { opacity: 0.4 },
        },
        data: reversed.map((s) => ({
          value: [s.min, s.p25, s.median, s.p95, s.max],
          _stat: s,
          itemStyle: {
            color: groupColor(s.colorIndex),
            borderColor: groupBorderColor(s.colorIndex),
          },
        })),
      },
    ],
  }
})
</script>

<template>
  <div class="flex flex-col gap-2">
    <div v-if="noData" class="text-2xs text-ink-muted">暂无数据</div>
    <VChart
      v-else
      :option="option"
      :style="{ height: `${height ?? groupStats.length * 15 + 50}px` }"
      :autoresize="true"
    />
  </div>
</template>
