<template>
  <div class="wf-json-tree">
      <div
        v-for="row in rows"
        :key="row.path"
        class="wf-json-row"
        :class="{ 'wf-json-row--leaf': row.isLeaf }"
        :style="{ paddingLeft: `${8 + row.depth * 14}px` }"
        :title="row.isLeaf ? refHint(row) : ''"
        :draggable="row.isLeaf"
        @dragstart="onDragStart($event, row)"
        @click="row.isLeaf && copyRef(row)"
      >
        <span
          v-if="!row.isLeaf"
          class="wf-json-toggle"
          @click.stop="toggle(row.path)"
        >
          {{ collapsed.has(row.path) ? '▸' : '▾' }}
        </span>
        <span v-else class="wf-json-toggle wf-json-toggle--blank" />
        <span class="wf-json-key">{{ row.key }}</span>
        <span class="wf-json-colon">:</span>
        <span v-if="row.isLeaf" class="wf-json-value" :class="`wf-json-${typeof row.value}`">{{ display(row.value) }}</span>
        <span v-else class="wf-json-meta">{{ row.summary }}</span>
      </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'

/**
 * Collapsible JSON tree for the node-detail OUTPUT pane (n8n RunDataJson
 * semantics, minus drag-to-parameter): object/array nodes fold, leaf rows
 * copy a `{node@param.path}` workflow reference on click — the engine
 * resolves dotted params against nested map outputs.
 */
const props = defineProps<{
  /** Root value to render. */
  value: unknown
  /** The workflow node id used when composing the copied reference. */
  nodeId: string
}>()

const emit = defineEmits<{
  'copied': [ref: string]
}>()

const { t } = useI18n()
const collapsed = reactive(new Set<string>())

interface TreeRow {
  key: string
  path: string // dotted path relative to the node output param
  depth: number
  isLeaf: boolean
  value?: unknown
  summary?: string
}

const MAX_PRIMITIVE = 120
const MAX_DEPTH = 32

function display(value: unknown): string {
  if (typeof value === 'string') return JSON.stringify(value.slice(0, MAX_PRIMITIVE))
  return String(value)
}

function summarize(value: unknown): string {
  if (Array.isArray(value)) return t('workflow.editor.treeCountItems', { n: value.length })
  if (value !== null && typeof value === 'object') return t('workflow.editor.treeCountFields', { n: Object.keys(value).length })
  return ''
}

function walk(value: unknown, key: string, prefix: string, depth: number, out: TreeRow[]) {
  const path = prefix ? `${prefix}.${key}` : key
  const branch = value !== null && typeof value === 'object'
  if (!branch || depth >= MAX_DEPTH) {
    out.push({ key, path, depth, isLeaf: true, value })
    return
  }
  out.push({ key, path, depth, isLeaf: false, summary: summarize(value) })
  if (collapsed.has(path)) return
  const entries = Array.isArray(value)
    ? value.map((v, i) => [String(i), v] as const)
    : Object.entries(value as Record<string, unknown>)
  for (const [k, v] of entries) {
    walk(v, k, path, depth + 1, out)
  }
}

const rows = computed<TreeRow[]>(() => {
  const out: TreeRow[] = []
  if (props.value !== null && typeof props.value === 'object') {
    const entries = Array.isArray(props.value)
      ? props.value.map((v, i) => [String(i), v] as const)
      : Object.entries(props.value as Record<string, unknown>)
    for (const [k, v] of entries) walk(v, k, '', 0, out)
  } else {
    out.push({ key: 'value', path: '', depth: 0, isLeaf: true, value: props.value })
  }
  return out
})

function toggle(path: string) {
  if (collapsed.has(path)) collapsed.delete(path)
  else collapsed.add(path)
}

function refOf(row: TreeRow): string {
  return `{${props.nodeId}@${row.path}}`
}

function refHint(row: TreeRow): string {
  return `${refOf(row)}  ${t('workflow.editor.refCopyHint')}`
}

async function copyRef(row: TreeRow) {
  const ref = refOf(row)
  try {
    await navigator.clipboard.writeText(ref)
  } catch {
    // Clipboard API unavailable (http origin) — selection fallback.
    const ta = document.createElement('textarea')
    ta.value = ref
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    ta.remove()
  }
  emit('copied', ref)
}

// n8n drag-to-expression: the leaf carries its reference as drag payload;
// RefTextarea inputs accept the drop and insert it at the caret.
function onDragStart(e: DragEvent, row: TreeRow) {
  if (!row.isLeaf) return
  e.dataTransfer?.setData('text/plain', refOf(row))
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'copy'
}
</script>

<style scoped>
.wf-json-tree {
  font-family: var(--td-font-family-code);
  font-size: 12px;
  line-height: 1.7;
}

.wf-json-row {
  display: flex;
  align-items: baseline;
  gap: 2px;
  white-space: nowrap;
  border-radius: 3px;
}

.wf-json-row--leaf {
  cursor: copy;
}

.wf-json-row--leaf:hover {
  background: var(--td-brand-color-light);
  outline: 1px dashed var(--td-brand-color);
}


.wf-json-toggle {
  flex: none;
  width: 14px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  user-select: none;
}

.wf-json-toggle--blank {
  cursor: default;
}

.wf-json-key {
  color: var(--td-brand-color);
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
}

.wf-json-colon {
  color: var(--td-text-color-placeholder);
}

.wf-json-value {
  overflow: hidden;
  text-overflow: ellipsis;
}

.wf-json-string {
  color: var(--td-success-color);
}

.wf-json-number,
.wf-json-boolean {
  color: var(--td-warning-color);
}

.wf-json-meta {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}
</style>
