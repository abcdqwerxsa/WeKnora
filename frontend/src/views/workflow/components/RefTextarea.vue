<template>
  <div class="wf-ref-input">
    <t-textarea
      :value="modelValue"
      :autosize="autosize"
      :placeholder="placeholder"
      @focus="onFocus"
      @click="rememberCaret"
      @keyup="rememberCaret"
      @input="onInput"
      @change="emitChange"
      @keydown="onKeyDown"
      @dragover.prevent
      @drop.prevent="onDrop"
    />
    <div v-if="suggestions.length > 0" class="wf-ref-suggest">
      <button
        v-for="(item, index) in suggestions"
        :key="item.ref"
        type="button"
        class="wf-ref-suggest-item"
        :class="{ 'wf-ref-suggest-item--active': index === activeIndex }"
        @mousedown.prevent="applySuggestion(item)"
        @mouseenter="activeIndex = index"
      >
        <span class="wf-ref-suggest-ref">{{ item.ref }}</span>
        <span v-if="item.hint" class="wf-ref-suggest-hint">{{ item.hint }}</span>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import type { RefSuggestion } from '../nodeMeta'

/**
 * Textarea with inline {reference} autocomplete.
 *
 * Typing "{" opens a suggestion list of upstream outputs / sys.* / env.*
 * (the same option set VariableRefPicker offers). Arrow keys + Enter/Tab
 * complete `{nodeId@param}`; Escape or typing "}" dismisses. Plain wrapper:
 * v-model compatible, no canvas knowledge — options arrive via prop.
 */

const props = defineProps<{
  modelValue: string
  suggestions: RefSuggestion[]
  placeholder?: string
  autosize?: { minRows: number; maxRows: number }
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string]
  change: [value: string]
}>()

const caret = ref(0)
const activeIndex = ref(0)
const open = ref(false)

/** The partial ref between "{" and the caret, when autocomplete is armed. */
const partial = computed<string | null>(() => {
  if (!open.value) return null
  const text = props.modelValue
  const pos = caret.value
  const before = text.slice(0, pos)
  const brace = before.lastIndexOf('{')
  if (brace === -1) return null
  const snippet = before.slice(brace + 1)
  // One ref in the making: no spaces, braces or "@" typed yet beyond the
  // node id (node@partial is fine, keeps filtering while typing the param).
  if (/[\s{}]/.test(snippet)) return null
  if (before.slice(brace + snippet.length + 1, pos) !== '') return null
  return snippet.toLowerCase()
})

const suggestions = computed(() => {
  const p = partial.value
  if (p === null) return []
  return props.suggestions.filter((item) => item.ref.toLowerCase().includes(p)).slice(0, 8)
})

function rememberCaret(event: Event) {
  const target = event.target as HTMLTextAreaElement
  caret.value = target.selectionStart ?? target.value.length
}

// n8n drag-to-expression: a leaf row dropped from the output tree inserts
// its {node@param} reference at the caret.
function onDrop(event: DragEvent) {
  const ref = event.dataTransfer?.getData('text/plain') ?? ''
  if (!ref.startsWith('{') || !ref.endsWith('}')) return
  const target = event.target as HTMLTextAreaElement
  const pos = target.selectionStart ?? props.modelValue.length
  const next = `${props.modelValue.slice(0, pos)}${ref}${props.modelValue.slice(pos)}`
  caret.value = pos + ref.length
  open.value = false
  emit('update:modelValue', next)
  emit('change', next)
  void nextTick(() => {
    target.focus()
    target.setSelectionRange(caret.value, caret.value)
  })
}

function onFocus() {
  open.value = true
}

function onInput(event: Event) {
  const target = event.target as HTMLTextAreaElement
  caret.value = target.selectionStart ?? target.value.length
  open.value = !target.value.slice(0, caret.value).endsWith('}')
  activeIndex.value = 0
  emit('update:modelValue', target.value)
  emit('change', target.value)
}

function emitChange(value: string) {
  emit('update:modelValue', value)
  emit('change', value)
}

function applySuggestion(item: RefSuggestion) {
  const text = props.modelValue
  const pos = caret.value
  const brace = text.slice(0, pos).lastIndexOf('{')
  if (brace === -1) return
  const inserted = `{${item.ref}}`
  const next = text.slice(0, brace) + inserted + text.slice(pos)
  caret.value = brace + inserted.length
  open.value = false
  emitChange(next)
}

function onKeyDown(event: KeyboardEvent) {
  if (suggestions.value.length === 0) return
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    activeIndex.value = (activeIndex.value + 1) % suggestions.value.length
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    activeIndex.value = (activeIndex.value - 1 + suggestions.value.length) % suggestions.value.length
  } else if (event.key === 'Enter' || event.key === 'Tab') {
    event.preventDefault()
    applySuggestion(suggestions.value[activeIndex.value]!)
  } else if (event.key === 'Escape') {
    open.value = false
  }
}
</script>

<style scoped>
.wf-ref-input {
  position: relative;
  width: 100%;
}

.wf-ref-suggest {
  position: absolute;
  z-index: 20;
  left: 0;
  right: 0;
  top: 100%;
  margin-top: 2px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  box-shadow: var(--td-shadow-2);
  max-height: 200px;
  overflow-y: auto;
}

.wf-ref-suggest-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  padding: 4px 8px;
  border: none;
  background: none;
  cursor: pointer;
  text-align: left;
  font-size: 12px;
}

.wf-ref-suggest-item--active,
.wf-ref-suggest-item:hover {
  background: var(--td-bg-color-container-hover);
}

.wf-ref-suggest-ref {
  font-family: var(--td-font-family-code, monospace);
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.wf-ref-suggest-hint {
  color: var(--td-text-color-placeholder);
  flex: none;
  font-size: 11px;
}
</style>
