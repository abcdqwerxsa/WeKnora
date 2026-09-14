<template>
  <!-- Wide invisible hit path under the visible one: hovering edges is easy. -->
  <path :d="d" class="wf-edge-hit" @mouseenter="hovered = true" @mouseleave="hovered = false" />
  <path :d="d" class="wf-edge-path" :marker-end="markerEnd" />
  <EdgeLabelRenderer>
    <!-- Branch label (Switch / QuestionClassifier): a chip on the line at
         the bezier midpoint so parallel branches are distinguishable. -->
    <div v-if="labelText" class="wf-edge-label" :style="{ left: `${midX}px`, top: `${midY}px` }">
      {{ labelText }}
    </div>
    <!-- Dify-style midpoint +: appears on edge hover, opens the quick-add
         list; picking a kind inserts a node BETWEEN source and target. -->
    <!-- Dify-style midpoint +: appears on edge hover, opens the quick-add
         list; picking a kind inserts a node BETWEEN source and target.
         pointerdown must not reach the pane: it would start a box-select
         drag that intercepts the menu item click. -->
    <div class="wf-edge-insert" :style="{ left: `${midX}px`, top: `${midY}px` }" @pointerdown.stop @mousedown.stop>
      <button
        v-if="hovered || open"
        type="button"
        class="wf-edge-insert-btn"
        :title="t('workflow.editor.quickAdd')"
        @click.stop="open = !open"
        @mouseenter="hovered = true"
        @mouseleave="hovered = false"
      >
        <t-icon name="add" />
      </button>
      <template v-if="open">
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
import { computed, ref, watch } from 'vue'
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
  /** Branch label set by the editor (refreshEdgeLabels). Loosely typed:
   *  vue-flow's EdgeProps.label is string | VNode | Component and the
   *  whole edgeProps object is v-bind'ed onto this component. */
  label?: unknown
}>()

const emit = defineEmits<{
  /** Emitted with the picked kind; the editor rewires source→new→target. */
  insert: [kind: WorkflowNodeType]
}>()

const { t } = useI18n()
const open = ref(false)
const hovered = ref(false)

// Reactive: the path (and the + anchor) must follow node drags. Destructuring
// a computed once leaves the geometry frozen at setup time.
const edgePath = computed(() =>
  getBezierPath({
    sourceX: props.sourceX,
    sourceY: props.sourceY,
    sourcePosition: props.sourcePosition as never,
    targetX: props.targetX,
    targetY: props.targetY,
    targetPosition: props.targetPosition as never,
  }),
)
const d = computed(() => edgePath.value[0])
const midX = computed(() => edgePath.value[1])
const midY = computed(() => edgePath.value[2])
const labelText = computed(() => (typeof props.label === 'string' ? props.label : ''))

// Close the insert menu on any click outside it. A fixed backdrop would
// not work here: EdgeLabelRenderer content sits inside the transformed
// viewport, which traps position:fixed to that box.
watch(open, (value, _prev, onCleanup) => {
  if (!value) return
  const close = (event: PointerEvent) => {
    const target = event.target as Element | null
    if (target?.closest('.wf-edge-insert')) return
    open.value = false
  }
  window.addEventListener('pointerdown', close, true)
  onCleanup(() => window.removeEventListener('pointerdown', close, true))
})

const quickAddKinds = computed(() =>
  NODE_PALETTE.filter((entry) => entry.kind !== 'Start').map((entry) => entry.kind),
)

function pick(kind: WorkflowNodeType) {
  open.value = false
  emit('insert', kind)
}
</script>

<style scoped>
/* Plain hex on purpose: `rgb(var(--td-gray-color-5, …))` is invalid at
   computed-value time wherever the theme defines the token as a hex value,
   which leaves stroke at its initial `none` — invisible edges. */
.wf-edge-path {
  stroke: #a6b1bd;
  stroke-width: 2;
  fill: none;
}

.wf-edge-hit {
  stroke: transparent;
  stroke-width: 16;
  fill: none;
  pointer-events: stroke;
}

/* Branch label chip on the line (Switch / QuestionClassifier edges). */
.wf-edge-label {
  position: absolute;
  transform: translate(-50%, -50%);
  padding: 1px 8px;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-secondary);
  font-size: 11px;
  line-height: 16px;
  white-space: nowrap;
  pointer-events: none;
  z-index: 3;
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
