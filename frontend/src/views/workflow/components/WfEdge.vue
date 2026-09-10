<template>
  <!-- Wide invisible hit path under the visible one: hovering edges is easy. -->
  <path :d="d" class="wf-edge-hit" @mouseenter="hovered = true" @mouseleave="hovered = false" />
  <path :d="d" class="wf-edge-path" :marker-end="markerEnd" />
  <EdgeLabelRenderer>
    <!-- Dify-style midpoint +: appears on edge hover, opens the quick-add
         list; picking a kind inserts a node BETWEEN source and target. -->
    <div class="wf-edge-insert" :style="{ left: `${midX}px`, top: `${midY}px` }">
      <button
        v-if="hovered || open"
        type="button"
        class="wf-edge-insert-btn"
        :title="t('workflow.editor.quickAdd')"
        @click.stop="open = !open"
      >
        <t-icon name="add" />
      </button>
      <template v-if="open">
        <div class="wf-quickadd-backdrop" @click.stop="open = false" />
        <div class="wf-quickadd-pop wf-edge-insert-pop">
          <button
            v-for="entry in quickAddKinds"
            :key="entry"
            type="button"
            class="wf-quickadd-item"
            @click.stop="pick(entry)"
          >
            <span class="wf-quickadd-item-icon" :style="{ background: NODE_COLORS[entry] }">
              <t-icon :name="NODE_ICONS[entry]" />
            </span>
            <span>{{ t(`workflow.nodes.${entry}`) }}</span>
          </button>
        </div>
      </template>
    </div>
  </EdgeLabelRenderer>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { EdgeLabelRenderer, getBezierPath } from '@vue-flow/core'
import type { WorkflowNodeType } from '@/api/workflow'
import { NODE_COLORS, NODE_ICONS, NODE_PALETTE } from '../nodeMeta'

const props = defineProps<{
  id: string
  sourceX: number
  sourceY: number
  targetX: number
  targetY: number
  sourcePosition?: unknown
  targetPosition?: unknown
  markerEnd?: string
}>()

const emit = defineEmits<{
  /** Emitted with the picked kind; the editor rewires source→new→target. */
  insert: [kind: WorkflowNodeType]
}>()

const { t } = useI18n()
const open = ref(false)
const hovered = ref(false)

// The bezier path plus its midpoint (labelX/labelY) — the + sits there.
const [d, midX, midY] = computed(() =>
  getBezierPath({
    sourceX: props.sourceX,
    sourceY: props.sourceY,
    sourcePosition: props.sourcePosition as never,
    targetX: props.targetX,
    targetY: props.targetY,
    targetPosition: props.targetPosition as never,
  }),
).value

const quickAddKinds = computed(() =>
  NODE_PALETTE.filter((entry) => entry.kind !== 'Start').map((entry) => entry.kind),
)

function pick(kind: WorkflowNodeType) {
  open.value = false
  emit('insert', kind)
}

defineExpose({ midX, midY })
</script>

<style scoped>
.wf-edge-path {
  stroke: rgb(var(--td-gray-color-5, 148, 158, 176));
  stroke-width: 2;
  fill: none;
}

.wf-edge-hit {
  stroke: transparent;
  stroke-width: 16;
  fill: none;
  pointer-events: stroke;
}

/* Midpoint insert +: circle button at the bezier midpoint, visible on
   edge hover (the hit path widens the hover zone to the whole edge). */
.wf-edge-insert {
  position: absolute;
  transform: translate(-50%, -50%);
  z-index: 4;
  pointer-events: none;
}

.wf-edge-insert-btn {
  pointer-events: auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  border: 1.5px solid var(--td-brand-color);
  background: var(--td-bg-color-container);
  color: var(--td-brand-color);
  font-size: 15px;
  cursor: pointer;
  box-shadow: var(--td-shadow-1);
}

.wf-edge-insert-btn:focus-visible {
  outline: 2px solid var(--td-brand-color);
}
</style>

<style>
/* EdgeLabelRenderer content is teleported into the edge label layer, so
   these reach it globally. Shared with the node quick-add menu look. */
.wf-edge-insert-pop {
  /* anchored below the + */
  position: absolute;
  left: 16px;
  top: -8px;
}
</style>
