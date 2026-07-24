<script setup lang="ts">
import { computed } from 'vue'

export interface TimeRangeValue {
  startAt: string
  endAt: string
}

const props = defineProps<{
  modelValue: TimeRangeValue
}>()

const emit = defineEmits<{
  'update:modelValue': [TimeRangeValue]
}>()

const pad = (n: number) => String(n).padStart(2, '0')

// RFC3339 UTC string → local datetime-local string (seconds precision).
// Returns '' for empty input. An unparseable external value yields '' and is
// flagged via startInvalid/endInvalid so the field surfaces an error rather
// than silently masking it.
function rfcToLocal(rfc: string): { local: string; invalid: boolean } {
  if (!rfc) return { local: '', invalid: false }
  const d = new Date(rfc)
  if (Number.isNaN(d.getTime())) return { local: '', invalid: true }
  return {
    local:
      `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` +
      `T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`,
    invalid: false,
  }
}

function localToRfc(local: string): string {
  if (!local) return ''
  const d = new Date(local)
  if (Number.isNaN(d.getTime())) return ''
  return d.toISOString()
}

const start = computed(() => rfcToLocal(props.modelValue.startAt))
const end = computed(() => rfcToLocal(props.modelValue.endAt))

const startLocal = computed({
  get: () => start.value.local,
  set: (v: string) =>
    emit('update:modelValue', { ...props.modelValue, startAt: localToRfc(v) }),
})

const endLocal = computed({
  get: () => end.value.local,
  set: (v: string) =>
    emit('update:modelValue', { ...props.modelValue, endAt: localToRfc(v) }),
})

const rangeError = computed(() => {
  if (!startLocal.value || !endLocal.value) return ''
  if (new Date(startLocal.value).getTime() > new Date(endLocal.value).getTime()) {
    return '开始时间不能晚于结束时间'
  }
  return ''
})
</script>

<template>
  <div class="flex items-start gap-2">
      <input
        v-model="startLocal"
        type="datetime-local"
        step="1"
        class="rounded-md border border-line bg-surface-0 px-2 py-1.5 text-sm text-ink outline-none focus:border-accent focus-visible:ring-1 focus-visible:ring-accent"
        :class="start.invalid || rangeError ? 'border-err focus:border-err focus-visible:ring-err' : ''"
      />
      <input
        v-model="endLocal"
        type="datetime-local"
        step="1"
        class="rounded-md border border-line bg-surface-0 px-2 py-1.5 text-sm text-ink outline-none focus:border-accent focus-visible:ring-1 focus-visible:ring-accent"
        :class="end.invalid || rangeError ? 'border-err focus:border-err focus-visible:ring-err' : ''"
      />
  </div>
</template>
