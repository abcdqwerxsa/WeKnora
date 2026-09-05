<template>
  <div class="wf-palette" :class="{ 'wf-palette--collapsed': collapsed }">
    <div class="wf-palette-header">
      <span v-if="!collapsed" class="wf-palette-title">{{ t('workflow.palette.title') }}</span>
      <t-button variant="text" size="small" @click="collapsed = !collapsed">
        <template #icon>
          <t-icon :name="collapsed ? 'chevron-right' : 'chevron-left'" />
        </template>
      </t-button>
    </div>
    <div v-if="!collapsed" class="wf-palette-body">
      <div v-for="group in PALETTE_GROUPS" :key="group" class="wf-palette-group">
        <p class="wf-palette-group-title">{{ t(`workflow.palette.groups.${group}`) }}</p>
        <button
          v-for="entry in kindsOf(group)"
          :key="entry"
          type="button"
          class="wf-palette-item"
          @click="emit('add', entry)"
        >
          <span class="wf-palette-item-icon" :style="{ background: NODE_COLORS[entry] }">
            <t-icon :name="NODE_ICONS[entry]" />
          </span>
          <span class="wf-palette-item-label">{{ t(`workflow.nodes.${entry}`) }}</span>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { WorkflowNodeType } from '@/api/workflow'
import { NODE_PALETTE, PALETTE_GROUPS, NODE_COLORS, NODE_ICONS, type NodePaletteEntry } from '../nodeMeta'

const emit = defineEmits<{ add: [kind: WorkflowNodeType] }>()
const { t } = useI18n()
const collapsed = ref(false)

function kindsOf(group: NodePaletteEntry['group']): WorkflowNodeType[] {
  return NODE_PALETTE.filter((entry) => entry.group === group).map((entry) => entry.kind)
}
</script>

<style scoped>
.wf-palette {
  width: 196px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
  border-right: 1px solid var(--td-component-stroke);
  overflow: hidden;
  transition: width 0.15s ease;
}

.wf-palette--collapsed {
  width: 40px;
}

.wf-palette-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 4px 6px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.wf-palette--collapsed .wf-palette-header {
  justify-content: center;
  padding: 6px 0;
}

.wf-palette-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.wf-palette-body {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
}

.wf-palette-group {
  margin-bottom: 10px;
}

.wf-palette-group-title {
  margin: 6px 4px;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  text-transform: uppercase;
  letter-spacing: 0.4px;
}

.wf-palette-item {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 6px;
  border: none;
  border-radius: 8px;
  background: transparent;
  cursor: pointer;
  text-align: left;
  transition: background 0.12s ease;
}

.wf-palette-item:hover {
  background: var(--td-bg-color-container-hover);
}

.wf-palette-item-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 7px;
  color: #fff;
  font-size: 15px;
  flex-shrink: 0;
}

.wf-palette-item-label {
  font-size: 13px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
