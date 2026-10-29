<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { EditorState, Compartment } from '@codemirror/state'
import {
  EditorView,
  keymap,
  lineNumbers,
  highlightActiveLine,
  highlightActiveLineGutter,
  drawSelection,
} from '@codemirror/view'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import {
  bracketMatching,
  defaultHighlightStyle,
  foldGutter,
  foldKeymap,
  indentOnInput,
  syntaxHighlighting,
} from '@codemirror/language'
import { highlightSelectionMatches, searchKeymap } from '@codemirror/search'
import {
  autocompletion,
  closeBrackets,
  closeBracketsKeymap,
  completionKeymap,
} from '@codemirror/autocomplete'
import { javascript } from '@codemirror/lang-javascript'
import { oneDark } from '@codemirror/theme-one-dark'

const props = withDefaults(
  defineProps<{
    modelValue?: string
    language?: 'javascript'
    minHeight?: string
    maxHeight?: string
  }>(),
  { modelValue: '', language: 'javascript', minHeight: '200px', maxHeight: '70vh' },
)
const emit = defineEmits<{ 'update:modelValue': [string] }>()

const host = ref<HTMLElement | null>(null)
let view: EditorView | null = null
const themeComp = new Compartment()

function isDark() {
  return document.documentElement.dataset.dark === 'true'
}

const baseTheme = EditorView.theme({
  '&': {
    fontSize: '13px',
    backgroundColor: 'var(--color-surface-0)',
    color: 'var(--color-ink)',
    border: '1px solid var(--color-line)',
    borderRadius: '6px',
    minHeight: props.minHeight,
    maxHeight: props.maxHeight,
  },
  '&.cm-focused': {
    outline: 'none',
    borderColor: 'var(--color-accent)',
    boxShadow: '0 0 0 3px color-mix(in oklch, var(--color-accent) 20%, transparent)',
  },
  '.cm-scroller': {
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    lineHeight: '1.55',
  },
  '.cm-gutters': {
    backgroundColor: 'var(--color-surface-50)',
    color: 'var(--color-ink-faint)',
    border: 'none',
    borderRight: '1px solid var(--color-line)',
  },
  '.cm-activeLine': { backgroundColor: 'color-mix(in oklch, var(--color-accent) 6%, transparent)' },
  '.cm-activeLineGutter': {
    backgroundColor: 'color-mix(in oklch, var(--color-accent) 8%, transparent)',
  },
  '.cm-selectionBackground, ::selection': {
    backgroundColor: 'color-mix(in oklch, var(--color-accent) 25%, transparent) !important',
  },
  '.cm-cursor': { borderLeftColor: 'var(--color-ink)' },
})

function themeExt() {
  return isDark()
    ? [oneDark, baseTheme]
    : [baseTheme, syntaxHighlighting(defaultHighlightStyle, { fallback: true })]
}

function createState(value: string) {
  return EditorState.create({
    doc: value,
    extensions: [
      lineNumbers(),
      highlightActiveLineGutter(),
      foldGutter(),
      drawSelection(),
      history(),
      indentOnInput(),
      bracketMatching(),
      closeBrackets(),
      autocompletion(),
      highlightActiveLine(),
      highlightSelectionMatches(),
      javascript(),
      keymap.of([
        ...closeBracketsKeymap,
        ...defaultKeymap,
        ...searchKeymap,
        ...historyKeymap,
        ...foldKeymap,
        ...completionKeymap,
        indentWithTab,
      ]),
      EditorView.lineWrapping,
      EditorView.updateListener.of((u) => {
        if (u.docChanged) {
          const v = u.state.doc.toString()
          if (v !== props.modelValue) emit('update:modelValue', v)
        }
      }),
      themeComp.of(themeExt()),
    ],
  })
}

let themeObserver: MutationObserver | null = null

onMounted(() => {
  if (!host.value) return
  view = new EditorView({ state: createState(props.modelValue), parent: host.value })
  themeObserver = new MutationObserver(() => {
    view?.dispatch({ effects: themeComp.reconfigure(themeExt()) })
  })
  themeObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['data-dark'],
  })
})

onBeforeUnmount(() => {
  themeObserver?.disconnect()
  view?.destroy()
  view = null
})

watch(
  () => props.modelValue,
  (v) => {
    if (!view) return
    const current = view.state.doc.toString()
    if (v !== current) {
      view.dispatch({ changes: { from: 0, to: current.length, insert: v ?? '' } })
    }
  },
)
</script>

<template>
  <div ref="host" class="code-editor w-full" />
</template>

<style scoped>
.code-editor :deep(.cm-editor) {
  border-radius: 6px;
}
</style>
