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
      <t-popup
        v-if="outputs"
        trigger="click"
        placement="right-top"
        :overlay-style="{ maxWidth: '420px' }"
      >
        <span
          class="wf-node-output-badge"
          :class="outputFailed ? 'wf-node-output-badge--failed' : ''"
          :title="$t('workflow.editor.nodeOutputs')"
          @click.stop
        >
          <t-icon :name="outputFailed ? 'error-circle' : 'browse'" />
        </span>
        <template #content>
          <div class="wf-node-output-pop">
            <p class="wf-node-output-title">{{ title }} · {{ nodeId }}</p>
            <pre class="wf-node-output-json">{{ outputsJSON }}</pre>
          </div>
        </template>
      </t-popup>
    </div>
    <!-- Source handles, Dify-style: the Handle itself is the interaction
         surface — a click toggles the quick-add menu, a drag starts a
         connection (vue-flow native). The + is a purely VISUAL affordance
         (pointer-events: none) so it never blocks connection drags.
         Routing nodes (Switch / QuestionClassifier) carry one handle per
         branch, vertically distributed on the right edge. -->
    <Handle
      v-for="branch in sourceBranches"
      :key="branch.id"
      type="source"
      :id="branch.id"
      :position="Position.Right"
      :style="branch.style"
      class="wf-handle"
      @mousedown="onHandleMouseDown"
      @click.stop="onHandleClick(branch.id)"
    >
      <span v-if="!connectedHandles.includes(branch.id)" class="wf-node-quickadd" :title="t('workflow.editor.quickAdd')">
        <t-icon name="add" />
      </span>
      <template v-if="menuOpen === branch.id">
        <div class="wf-quickadd-backdrop" @click.stop="menuOpen = null" />
        <div class="wf-quickadd-pop">
          <button
            v-for="entry in quickAddKinds"
            :key="entry"
            type="button"
            class="wf-quickadd-item"
            @click.stop="pickKind(entry, branch.id)"
          >
            <span class="wf-quickadd-item-icon" :style="{ background: NODE_COLORS[entry] }">
              <t-icon :name="NODE_ICONS[entry]" />
            </span>
            <span>{{ t(`workflow.nodes.${entry}`) }}</span>
          </button>
        </div>
      </template>
    </Handle>
    <!-- Connected marker (Dify-style): a 2×8px vertical line owns the spot
         once an edge leaves the handle; the + is gone. -->
    <span
      v-for="branch in sourceBranches"
      v-show="connectedHandles.includes(branch.id)"
      :key="`mark-${branch.id}`"
      class="wf-handle-connected"
      :style="branch.style"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Handle, Position } from '@vue-flow/core'
import type { WorkflowNodeType } from '@/api/workflow'
import { NODE_COLORS, NODE_ICONS, NODE_PALETTE } from '../nodeMeta'

const emit = defineEmits<{ 'quick-add': [kind: WorkflowNodeType, sourceHandle?: string] }>()

const props = defineProps<{
  /** Handle ids that already have an outgoing edge: their + yields the spot
   *  to the connected edge (Dify behaviour — connected handles show a line). */
  connectedHandles?: string[]
  /** Branch handles for routing nodes (Switch / QuestionClassifier):
   *  one labelled handle per case plus the default branch. Plain nodes
   *  get a single unnamed handle. */
  branches?: Array<{ id: string; label: string }>
  kind: WorkflowNodeType
  selected?: boolean
  subtitle?: string
  /** Live run progress from the SSE stream; undefined when idle. */
  runPhase?: 'running' | 'done' | 'failed'
  /** Last-run outputs (debug payload); presence renders the inspect badge. */
  outputs?: Record<string, unknown>
  /** Node id shown in the outputs popover header. */
  nodeId?: string
}>()

const { t } = useI18n()

const hasTargetHandle = computed(() => props.kind !== 'Start')
const hasSourceHandle = computed(() => props.kind !== 'Answer')
const badgeColor = computed(() => NODE_COLORS[props.kind] ?? '#9aa4b2')
const iconName = computed(() => NODE_ICONS[props.kind] ?? 'app')
const title = computed(() => t(`workflow.nodes.${props.kind}`))
const desc = computed(() => t(`workflow.nodeDesc.${props.kind}`))
const outputsJSON = computed(() => JSON.stringify(props.outputs ?? {}, null, 2))
const outputFailed = computed(() => props.runPhase === 'failed')

// Quick-add menu: everything the palette offers except Start (the graph
// allows a single Start node, which already exists when any node is on the
// canvas).
const quickAddKinds = computed(() =>
  NODE_PALETTE.filter((entry) => entry.kind !== 'Start').map((entry) => entry.kind),
)

// Source handles: one unnamed handle for plain nodes; one per branch for
// routing nodes (the parent passes `branches`), vertically distributed.
const sourceBranches = computed(() => {
  if (!props.branches || props.branches.length === 0) {
    return props.hasSourceHandle ? [{ id: undefined as string | undefined, label: '', style: {} }] : []
  }
  const n = props.branches.length
  return props.branches.map((branch, i) => ({
    ...branch,
    style: { top: `${((i + 1) / (n + 1)) * 100}%` },
  }))
})

const connectedHandles = computed(() => props.connectedHandles ?? [])

// Quick-add menu: opened by a plain CLICK on the handle. A click that
// follows a drag (vue-flow connection) must not open it — track the mousedown
// position and ignore displaced clicks (Dify relies on react-flow's own
// click/drag separation; we approximate with a 5px threshold).
const menuOpen = ref<string | null>(null)
let handleDownAt = { x: 0, y: 0 }

function onHandleMouseDown(event: MouseEvent) {
  handleDownAt = { x: event.clientX, y: event.clientY }
}

function onHandleClick(handleId: string | undefined) {
  if (Math.hypot(event.clientX - handleDownAt.x, event.clientY - handleDownAt.y) > 5) return
  const id = handleId ?? ''
  if (connectedHandles.value.includes(id)) return
  menuOpen.value = menuOpen.value === id ? null : id
}

function pickKind(kind: WorkflowNodeType, handleId: string | undefined) {
  menuOpen.value = null
  emit('quick-add', kind, handleId ?? undefined)
}
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
    box-shadow: 0 0 0 6px rgba(var(--td-brand-rgb), 0.15);
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

/* Last-run outputs inspect badge (debug payload from the run panel). */
.wf-node-output-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 6px;
  color: var(--td-success-color);
  background: var(--td-success-color-1);
  cursor: pointer;
  flex: none;
  font-size: 14px;
}

.wf-node-output-badge--failed {
  color: var(--td-error-color);
  background: var(--td-error-color-1);
}

.wf-node-output-pop {
  max-width: 400px;
}

.wf-node-output-title {
  margin: 0 0 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

/* Dify-style handles: invisible element, 20px hit target; visuals are the
   big hover + (24px, plus INSIDE the circle) or, once connected, the line
   marker. The invisible halo keeps manual edge-drag forgiving. */
:deep(.vue-flow__handle) {
  width: 20px;
  height: 20px;
  border: none !important;
  background: transparent !important;
  position: relative;
}

:deep(.vue-flow__handle)::after {
  content: '';
  position: absolute;
  inset: -8px;
  border-radius: 50%;
}

/* While a connection drag is live, every valid drop target lights up so
   users can SEE where the line can land (Dify highlights targets too). */
:deep(.vue-flow__handle.connectionindicator) {
  background: var(--td-brand-color) !important;
  border-radius: 50%;
  box-shadow: 0 0 0 4px color-mix(in srgb, var(--td-brand-color) 22%, transparent);
}

/* The + lives INSIDE the handle box, centered on the connection anchor —
   edges and the affordance can never drift apart. Purely visual: pointer
   events pass through to the Handle so connection drags always work. */
.wf-node-quickadd {
  position: absolute;
  left: 50%;
  top: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  color: var(--td-brand-color);
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 50%;
  width: 24px;
  height: 24px;
  box-shadow: var(--td-shadow-1);
  pointer-events: none;
  opacity: 0;
  transform: translate(-50%, -50%) scale(0.8);
  transition: opacity 0.15s ease, transform 0.15s ease;
}

.wf-node:hover .wf-node-quickadd,
.wf-node--selected .wf-node-quickadd {
  opacity: 1;
  transform: translate(-50%, -50%) scale(1);
}

/* Quick-add popover anchored to the handle (right of the node). The
   fixed backdrop closes it on any outside click. */
.wf-quickadd-backdrop {
  position: fixed;
  inset: 0;
  z-index: 30;
}

.wf-quickadd-pop {
  position: absolute;
  left: calc(100% + 10px);
  top: 50%;
  transform: translateY(-50%);
  z-index: 31;
  display: grid;
  grid-template-columns: repeat(2, minmax(120px, 1fr));
  gap: 2px;
  max-height: 320px;
  overflow-y: auto;
  min-width: 260px;
  padding: 6px;
  border-radius: 8px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  box-shadow: var(--td-shadow-3);
}

.wf-quickadd-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border: none;
  background: none;
  border-radius: 6px;
  cursor: pointer;
  font-size: 12px;
  color: var(--td-text-color-primary);
  text-align: left;
}

.wf-quickadd-item:hover {
  background: var(--td-bg-color-container-hover);
}

.wf-quickadd-item-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 6px;
  color: #fff;
  flex: none;
  font-size: 12px;
}

.wf-node-output-json {
  margin: 0;
  max-height: 260px;
  overflow: auto;
  font-size: 11px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
