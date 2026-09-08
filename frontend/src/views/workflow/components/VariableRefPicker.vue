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
          <div v-if="envVarNames.length > 0" class="wf-refpicker-group">
            <p class="wf-refpicker-node">env</p>
            <t-button
              v-for="name in envVarNames"
              :key="name"
              variant="text"
              size="small"
              class="wf-refpicker-item"
              @click="pick(`env.${name}`)"
            >
              env · {{ name }}
            </t-button>
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
import { outputParamsOf, upstreamNodeIds } from '../nodeMeta'

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
  /** Workflow variable names offered as env.<name> entries. */
  envNames?: string[]
}>()

const emit = defineEmits<{ insert: [ref: string] }>()

const { t } = useI18n()
const visible = ref(false)
const envVarNames = computed(() => props.envNames ?? [])

interface RefGroup {
  nodeId: string
  kind: WorkflowNodeType
  params: string[]
}

const entries = computed<RefGroup[]>(() => {
  // Ancestor walk shared with the inline autocomplete (nodeMeta).
  const ancestors = upstreamNodeIds(props.currentNodeId, props.edges)
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
