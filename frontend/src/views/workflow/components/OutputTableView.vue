<template>
  <div class="wf-out-table">
    <div
      v-for="(block, bi) in blocks"
      :key="bi"
      class="wf-out-table-block"
    >
      <p class="wf-out-table-caption">{{ block.caption }}</p>
      <table class="wf-out-table-table">
        <thead>
          <tr>
            <th v-for="col in block.columns" :key="col">{{ col }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, ri) in block.rows" :key="ri">
            <td
              v-for="col in block.columns"
              :key="col"
              :title="cellText(row[col])"
            >{{ cellText(row[col]) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

/**
 * n8n RunData Table semantics: root arrays of objects become tables; an
 * object output whose first array-of-objects field is the payload (search
 * results, LLM lists) renders that field as the table instead. Everything
 * else falls back to nothing (caller shows the JSON view).
 */
const props = defineProps<{
  value: unknown
}>()

const { t } = useI18n()

const CELL_MAX = 200

function cellText(v: unknown): string {
  if (v === null || v === undefined) return ''
  const s = typeof v === 'object' ? JSON.stringify(v) : String(v)
  return s.length > CELL_MAX ? `${s.slice(0, CELL_MAX)}…` : s
}

function isObj(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v)
}

function columnsOf(items: unknown[]): string[] {
  const cols: string[] = []
  for (const item of items) {
    if (!isObj(item)) return []
    for (const k of Object.keys(item)) {
      if (!cols.includes(k)) cols.push(k)
    }
  }
  return cols
}

interface TableBlock {
  caption: string
  columns: string[]
  rows: Array<Record<string, unknown>>
}

const blocks = computed<TableBlock[]>(() => {
  const out: TableBlock[] = []
  const root = props.value
  if (Array.isArray(root)) {
    const cols = columnsOf(root)
    if (cols.length > 0) out.push({ caption: t('workflow.editor.outputTableRows', { count: root.length }), columns: cols, rows: root.filter(isObj) as Array<Record<string, unknown>> })
    return out
  }
  if (isObj(root)) {
    for (const [key, v] of Object.entries(root)) {
      if (Array.isArray(v)) {
        const cols = columnsOf(v)
        if (cols.length > 0) {
          out.push({ caption: key, columns: cols, rows: v.filter(isObj) as Array<Record<string, unknown>> })
        }
      }
    }
  }
  return out
})
</script>

<style scoped>
.wf-out-table {
  display: flex;
  flex-direction: column;
  gap: 12px;
  overflow-x: auto;
}

.wf-out-table-caption {
  margin: 0 0 4px;
  font-size: 11px;
  font-weight: 600;
  color: var(--td-text-color-placeholder);
}

.wf-out-table-table {
  border-collapse: collapse;
  font-size: 12px;
  width: 100%;
}

.wf-out-table-table th {
  text-align: left;
  padding: 4px 8px;
  background: var(--td-bg-color-graycontainer);
  color: var(--td-text-color-secondary);
  font-weight: 600;
  border: 1px solid var(--td-component-stroke);
  white-space: nowrap;
}

.wf-out-table-table td {
  padding: 4px 8px;
  border: 1px solid var(--td-component-stroke);
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
