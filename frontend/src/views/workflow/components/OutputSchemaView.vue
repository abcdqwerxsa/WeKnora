<template>
  <div class="wf-schema">
    <template v-if="rows.length > 0">
      <div v-for="row in rows" :key="row.path" class="wf-schema-row" :style="{ paddingLeft: `${4 + row.depth * 12}px` }">
        <span class="wf-schema-toggle">{{ row.leaf ? '·' : (collapsed.has(row.path) ? '▸' : '▾') }}</span>
        <span
          class="wf-schema-toggle-btn"
          @click="!row.leaf && toggle(row.path)"
        >{{ row.key }}</span>
        <span class="wf-schema-type" :class="`wf-schema-type--${row.type}`">{{ row.type }}</span>
        <span v-if="row.sample !== null" class="wf-schema-sample" :title="row.sample">{{ row.sample }}</span>
      </div>
    </template>
    <p v-if="rows.length === 0" class="wf-schema-empty">{{ t('workflow.editor.schemaEmpty') }}</p>
    <p v-else-if="declarationMode" class="wf-schema-note">{{ t('workflow.editor.schemaNoOutputYet') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'

/**
 * n8n RunData Schema semantics: a compact field inventory — name, inferred
 * type, sample value. Same JSON underneath; a reading aid for "what does
 * this node emit" before wiring references. In declaration mode (no run
 * output yet) the declared outputs render with their static types.
 */
const props = defineProps<{
  value?: unknown
  /** Declared outputs (outputDeclsOf): rendered when there is no value. */
  decls?: Array<{ name: string; type: string; desc?: string }>
}>()

const { t } = useI18n()
const collapsed = reactive(new Set<string>())

const declarationMode = computed(
  () => (props.value === null || props.value === undefined) && (props.decls?.length ?? 0) > 0,
)

interface SchemaRow {
  key: string
  path: string
  depth: number
  leaf: boolean
  type: string
  sample: string | null
}

const SAMPLE_MAX = 60
const MAX_DEPTH = 32

function typeOf(v: unknown): string {
  if (v === null) return 'null'
  if (Array.isArray(v)) return 'array'
  return typeof v
}

function sampleOf(v: unknown): string | null {
  if (v === null || v === undefined) return null
  if (typeof v === 'object') return null
  const s = String(v)
  return s.length > SAMPLE_MAX ? `${s.slice(0, SAMPLE_MAX)}…` : s
}

function walk(value: unknown, key: string, prefix: string, depth: number, out: SchemaRow[]) {
  const path = prefix ? `${prefix}.${key}` : key
  const type = typeOf(value)
  if ((type !== 'object' && type !== 'array') || depth >= MAX_DEPTH) {
    out.push({ key, path, depth, leaf: true, type, sample: sampleOf(value) })
    return
  }
  out.push({ key, path, depth, leaf: false, type, sample: null })
  if (collapsed.has(path)) return
  const entries = Array.isArray(value)
    ? value.slice(0, 1).map((v, i) => [String(i), v] as const) // schema of [0] stands for all
    : Object.entries(value as Record<string, unknown>)
  for (const [k, v] of entries) {
    walk(v, k === '' ? '""' : k, path, depth + 1, out)
  }
}

const rows = computed<SchemaRow[]>(() => {
  // Declaration mode: no run output — the declared structure stands in.
  if (declarationMode.value) {
    return (props.decls ?? []).map((d) => ({
      key: d.name,
      path: d.name,
      depth: 0,
      leaf: d.type !== 'object',
      type: d.type,
      sample: d.desc ?? null,
    }))
  }
  const out: SchemaRow[] = []
  if (props.value !== null && typeof props.value === 'object') {
    const entries = Array.isArray(props.value)
      ? props.value.slice(0, 1).map((v, i) => [String(i), v] as const)
      : Object.entries(props.value as Record<string, unknown>)
    for (const [k, v] of entries) walk(v, k === '' ? '""' : k, '', 0, out)
  } else if (props.value !== undefined) {
    out.push({ key: 'value', path: '', depth: 0, leaf: true, type: typeOf(props.value), sample: sampleOf(props.value) })
  }
  return out
})

function toggle(path: string) {
  if (collapsed.has(path)) collapsed.delete(path)
  else collapsed.add(path)
}
</script>

<style scoped>
.wf-schema {
  font-size: 12px;
  line-height: 1.8;
}

.wf-schema-row {
  display: flex;
  align-items: baseline;
  gap: 6px;
}

.wf-schema-toggle {
  color: var(--td-text-color-placeholder);
  flex: none;
  width: 12px;
}

.wf-schema-toggle-btn {
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.wf-schema-toggle-btn:hover {
  text-decoration: underline dotted;
}

.wf-schema-type {
  flex: none;
  font-size: 10px;
  font-family: var(--td-font-family-code);
  padding: 0 5px;
  border-radius: 3px;
  background: var(--td-bg-color-graycontainer);
  color: var(--td-text-color-secondary);
}

.wf-schema-type--string {
  color: var(--td-success-color);
}

.wf-schema-type--number,
.wf-schema-type--boolean {
  color: var(--td-warning-color);
}

.wf-schema-type--array {
  color: var(--td-brand-color);
}

.wf-schema-sample {
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--td-font-family-code);
}

.wf-schema-empty {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.wf-schema-note {
  margin-top: 6px;
  padding-top: 6px;
  border-top: 1px dashed var(--td-component-stroke);
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}
</style>
