<template>
  <div class="wf-node" :class="[`wf-node--${kind}`, { 'wf-node--selected': selected }, runPhase ? `wf-node--run-${runPhase}` : '']">
    <Handle v-if="hasTargetHandle" type="target" :position="Position.Left" />
    <div class="wf-node-inner">
      <span class="wf-node-icon" :style="{ background: badgeColor }">
        <t-icon :name="iconName" />
      </span>
      <div class="wf-node-text">
        <span class="wf-node-kind">{{ title }}</span>
        <span class="wf-node-subtitle">{{ subtitle || desc }}</span>
      </div>
    </div>
    <Handle v-if="hasSourceHandle" type="source" :position="Position.Right" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Handle, Position } from '@vue-flow/core'
import type { WorkflowNodeType } from '@/api/workflow'
import { NODE_COLORS, NODE_ICONS } from '../nodeMeta'

const props = defineProps<{
  kind: WorkflowNodeType
  selected?: boolean
  subtitle?: string
  /** Live run progress from the SSE stream; undefined when idle. */
  runPhase?: 'running' | 'done' | 'failed'
}>()

const { t } = useI18n()

const hasTargetHandle = computed(() => props.kind !== 'Start')
const hasSourceHandle = computed(() => props.kind !== 'Answer')
const badgeColor = computed(() => NODE_COLORS[props.kind] ?? '#9aa4b2')
const iconName = computed(() => NODE_ICONS[props.kind] ?? 'app')
const title = computed(() => t(`workflow.nodes.${props.kind}`))
const desc = computed(() => t(`workflow.nodeDesc.${props.kind}`))
</script>

<style scoped>
.wf-node {
  border: 1.5px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-1);
  width: 208px;
  transition: box-shadow 0.15s ease, border-color 0.15s ease, transform 0.15s ease;
}

.wf-node:hover {
  box-shadow: var(--td-shadow-3);
  transform: translateY(-1px);
}

.wf-node--selected {
  border-color: v-bind(badgeColor);
  box-shadow: 0 0 0 3px rgba(0, 0, 0, 0.04), 0 0 0 6px rgba(0, 82, 217, 0.08);
}

/* Live run phases (set from the SSE node events by the editor). */
.wf-node--run-running {
  border-color: var(--td-brand-color);
  animation: wf-node-pulse 1.1s ease-in-out infinite;
}

.wf-node--run-done {
  border-color: var(--td-success-color);
}

.wf-node--run-failed {
  border-color: var(--td-error-color);
}

@keyframes wf-node-pulse {
  50% {
    box-shadow: 0 0 0 6px rgba(0, 82, 217, 0.18);
  }
}

.wf-node-inner {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
}

.wf-node-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
  border-radius: 9px;
  color: #fff;
  font-size: 20px;
  flex-shrink: 0;
}

.wf-node-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.wf-node-kind {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.wf-node-subtitle {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
