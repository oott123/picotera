import { intervalToDuration, formatDuration as fnsFormatDuration } from 'date-fns'
import { zhCN } from 'date-fns/locale'

const UNITS = ['years', 'months', 'days', 'hours', 'minutes', 'seconds'] as const

// 返回首次 → 最近的人类可读持续时间；无效或负区间返回 '—'
export function formatDuration(from?: string, to?: string): string {
  if (!from || !to) return '—'
  const start = new Date(from)
  const end = new Date(to)
  if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) return '—'
  if (end.getTime() < start.getTime()) return '—'
  if (end.getTime() === start.getTime()) return '0 秒'

  const duration = intervalToDuration({ start, end })
  // 从最大的非零单位起，最多取两个单位，避免过长
  const nonZero = UNITS.filter((u) => (duration[u] ?? 0) > 0)
  const format = nonZero.slice(0, 2)
  return fnsFormatDuration(duration, { locale: zhCN, format, delimiter: ' ' })
}
