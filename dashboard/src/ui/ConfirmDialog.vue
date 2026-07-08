<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { confirmState } from '@/composables/useConfirm'
import Button from './Button.vue'

const dialogRef = ref<HTMLElement | null>(null)
const cancelRef = ref<InstanceType<typeof Button> | null>(null)
const acceptRef = ref<InstanceType<typeof Button> | null>(null)
let previousFocus: HTMLElement | null = null

function buttonEl(refValue: InstanceType<typeof Button> | null): HTMLElement | null {
  const el = refValue?.$el
  return el instanceof HTMLElement ? el : null
}

function focusableButtons() {
  return [buttonEl(cancelRef.value), buttonEl(acceptRef.value)].filter(
    (el): el is HTMLElement => !!el && !el.hasAttribute('disabled'),
  )
}

function focusAccept() {
  const target = buttonEl(acceptRef.value) ?? buttonEl(cancelRef.value)
  target?.focus()
}

async function accept() {
  if (confirmState.accepting) return
  confirmState.accepting = true
  try {
    await confirmState.onAccept?.()
  } finally {
    confirmState.visible = false
    confirmState.accepting = false
  }
}

function reject() {
  confirmState.visible = false
}

function handleKeydown(event: KeyboardEvent) {
  if (!confirmState.visible) return
  if (event.key === 'Escape') {
    event.preventDefault()
    reject()
    return
  }
  if (event.key !== 'Tab') return

  const buttons = focusableButtons()
  if (buttons.length === 0) {
    event.preventDefault()
    return
  }
  const first = buttons[0]!
  const last = buttons[buttons.length - 1]!
  const active = document.activeElement

  if (event.shiftKey) {
    if (active === first || !dialogRef.value?.contains(active)) {
      event.preventDefault()
      last.focus()
    }
  } else if (active === last || !dialogRef.value?.contains(active)) {
    event.preventDefault()
    first.focus()
  }
}

function removeKeydown() {
  window.removeEventListener('keydown', handleKeydown)
}

watch(
  () => confirmState.visible,
  async (visible) => {
    if (visible) {
      previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
      window.addEventListener('keydown', handleKeydown)
      await nextTick()
      focusAccept()
    } else {
      removeKeydown()
      previousFocus?.focus()
      previousFocus = null
    }
  },
)

onBeforeUnmount(removeKeydown)
</script>

<template>
  <Teleport to="body">
    <div
      v-if="confirmState.visible"
      class="fixed inset-0 z-[9999] flex items-center justify-center bg-overlay-bg"
      @click.self="reject"
    >
      <div
        ref="dialogRef"
        role="dialog"
        aria-modal="true"
        aria-describedby="confirm-dialog-message"
        class="min-w-80 max-w-[420px] px-6 py-5 bg-surface-0 border border-line rounded-xl shadow-lg"
      >
        <p id="confirm-dialog-message" class="m-0 mb-4 text-sm leading-[1.5] text-ink">
          {{ confirmState.message }}
        </p>
        <div class="flex justify-end gap-2">
          <Button ref="cancelRef" variant="ghost" @click="reject">取消</Button>
          <Button ref="acceptRef" variant="danger" :disabled="confirmState.accepting" @click="accept">
            删除
          </Button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
