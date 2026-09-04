<template>
  <div class="wf-node" :class="[`wf-node--${kind}`, { 'wf-node--selected': selected }]">
    <Handle v-if="hasTargetHandle" type="target" :position="Position.Left" />
    <div class="wf-node-inner">
      <span class="wf-node-badge">{{ kind.charAt(0) }}</span>
      <div class="wf-node-text">
        <span class="wf-node-kind">{{ kind }}</span>
        <span v-if="subtitle" class="wf-node-subtitle">{{ subtitle }}</span>
      </div>
    </div>
    <Handle v-if="hasSourceHandle" type="source" :position="Position.Right" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Handle, Position } from '@vue-flow/core'
import type { WorkflowNodeType } from '@/api/workflow'

const props = defineProps<{
  kind: WorkflowNodeType
  selected?: boolean
  subtitle?: string
}>()

const hasTargetHandle = computed(() => props.kind !== 'Start')
const hasSourceHandle = computed(() => props.kind !== 'Answer')
</script>

<style scoped>
.wf-node {
  border: 2px solid transparent;
  border-radius: 8px;
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-1);
  min-width: 132px;
  transition: border-color 0.15s ease;
}

.wf-node--selected {
  border-color: var(--td-brand-color);
}

.wf-node-inner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
}

.wf-node-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 6px;
  color: #fff;
  font-size: 12px;
  font-weight: 700;
}

.wf-node--Start .wf-node-badge {
  background: #34c77b;
}

.wf-node--LLM .wf-node-badge {
  background: #8e7cf0;
}

.wf-node--Retrieval .wf-node-badge {
  background: #4d9fff;
}

.wf-node--Switch .wf-node-badge {
  background: #f0a24a;
}

.wf-node--Answer .wf-node-badge {
  background: #9aa4b2;
}

.wf-node--Start {
  border-top: 3px solid #34c77b;
}

.wf-node--LLM {
  border-top: 3px solid #8e7cf0;
}

.wf-node--Retrieval {
  border-top: 3px solid #4d9fff;
}

.wf-node--Switch {
  border-top: 3px solid #f0a24a;
}

.wf-node--Answer {
  border-top: 3px solid #9aa4b2;
}

.wf-node-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.wf-node-kind {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.wf-node-subtitle {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
