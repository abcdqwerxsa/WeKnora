<template>
  <t-popup v-model:visible="visible" placement="bottom-left" trigger="click">
    <template #content>
      <div class="wf-refpicker">
        <p class="wf-refpicker-title">{{ t('workflow.editor.refPickerTitle') }}</p>
        <div v-if="entries.length === 0" class="wf-refpicker-empty">{{ t('workflow.editor.refPickerEmpty') }}</div>
        <template v-else>
          <div v-for="group in entries" :key="group.nodeId" class="wf-refpicker-group">
            <p class="wf-refpicker-node" :title="group.nodeId">
              {{ t(`workflow.nodes.${group.kind}`) }} · {{ group.nodeId }}
            </p>
            <t-button
              v-for="param in group.params"
              :key="param"
              variant="text"
              size="small"
              class="wf-refpicker-item"
              @click="pick(`${group.nodeId}@${param}`)"
            >
              <template #icon><t-icon name="at" /></template>
              {{ param }}
            </t-button>
          </div>
          <div class="wf-refpicker-group">
            <p class="wf-refpicker-node">sys</p>
            <t-button variant="text" size="small" class="wf-refpicker-item" @click="pick('sys.query')">sys · query</t-button>
            <t-button variant="text" size="small" class="wf-refpicker-item" @click="pick('sys.files')">sys · files</t-button>
          </div>
        </template>
      </div>
    </template>
    <t-button variant="outline" size="small">
      <template #icon><t-icon name="at" /></template>
      {{ t('workflow.editor.insertRef') }}
    </t-button>
  </t-popup>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Edge } from '@vue-flow/core'
import type { WorkflowNodeType } from '@/api/workflow'
import { outputParamsOf } from '../nodeMeta'

/**
 * Reference picker for `{nodeId@param}` template insertion. Lists only the
 * ancestors of the current node (anything reachable by walking upstream
 * over the canvas edges) — those are the values the engine can actually
 * resolve at run time.
 */
const props = defineProps<{
  currentNodeId: string
  nodes: Array<{ id: string; kind: WorkflowNodeType; params?: Record<string, unknown> }>
  edges: Edge[]
}>()

const emit = defineEmits<{ insert: [ref: string] }>()

const { t } = useI18n()
const visible = ref(false)

interface RefGroup {
  nodeId: string
  kind: WorkflowNodeType
  params: string[]
}

const entries = computed<RefGroup[]>(() => {
  // Ancestors of the current node: walk upstream from it over the edges.
  const upstream = new Map<string, string[]>()
  for (const edge of props.edges) {
    const list = upstream.get(edge.target) ?? []
    list.push(edge.source)
    upstream.set(edge.target, list)
  }
  const ancestors = new Set<string>()
  const queue = [...(upstream.get(props.currentNodeId) ?? [])]
  while (queue.length > 0) {
    const id = queue.shift()!
    if (ancestors.has(id)) continue
    ancestors.add(id)
    queue.push(...(upstream.get(id) ?? []))
  }

  const groups: RefGroup[] = []
  for (const node of props.nodes) {
    if (node.id === props.currentNodeId || !ancestors.has(node.id)) continue
    const params = outputParamsOf(node.kind, node.params)
    if (params.length === 0) continue
    groups.push({ nodeId: node.id, kind: node.kind, params })
  }
  return groups
})

function pick(ref: string) {
  emit('insert', `{${ref}}`)
  visible.value = false
}
</script>

<style scoped>
.wf-refpicker {
  padding: 8px;
  max-height: 320px;
  overflow-y: auto;
  min-width: 200px;
}

.wf-refpicker-title {
  margin: 0 0 6px;
  font-size: 12px;
  font-weight: 600;
}

.wf-refpicker-empty {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.wf-refpicker-group {
  margin-bottom: 6px;
}

.wf-refpicker-node {
  margin: 2px 0;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 220px;
}

.wf-refpicker-item {
  font-size: 12px;
  font-family: var(--td-font-family-code, monospace);
}
</style>
