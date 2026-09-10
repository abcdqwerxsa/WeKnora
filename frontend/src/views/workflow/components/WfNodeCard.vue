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
    <!-- Source handle, Dify-style: the Handle itself is the interaction
         surface — a click toggles the quick-add menu, a drag starts a
         connection (vue-flow native). The + is a purely VISUAL affordance
         (pointer-events: none) so it never blocks connection drags. -->
    <Handle
      v-if="hasSourceHandle"
      type="source"
      :position="Position.Right"
      @mousedown="onHandleMouseDown"
      @click.stop="onHandleClick"
    >
      <span v-if="!hasOutgoing" class="wf-node-quickadd" :title="t('workflow.editor.quickAdd')">
        <t-icon name="add" />
      </span>
      <template v-if="menuOpen">
        <div class="wf-quickadd-backdrop" @click.stop="menuOpen = false" />
        <div class="wf-quickadd-pop">
          <button
            v-for="entry in quickAddKinds"
            :key="entry"
            type="button"
            class="wf-quickadd-item"
            @click.stop="pickKind(entry)"
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
         once an edge leaves this node; the + is gone. -->
    <span v-if="hasSourceHandle && hasOutgoing" class="wf-handle-connected" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Handle, Position } from '@vue-flow/core'
import type { WorkflowNodeType } from '@/api/workflow'
import { NODE_COLORS, NODE_ICONS, NODE_PALETTE } from '../nodeMeta'

const emit = defineEmits<{ 'quick-add': [kind: WorkflowNodeType] }>()

const props = defineProps<{
  /** True when an edge already leaves this node: the + yields its spot to
   *  the connected edge (Dify behaviour — connected handles show no +). */
  hasOutgoing?: boolean
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

// Quick-add menu: opened by a plain CLICK on the handle. A click that
// follows a drag (vue-flow connection) must not open it — track the mousedown
// position and ignore displaced clicks (Dify relies on react-flow's own
// click/drag separation; we approximate with a 5px threshold).
const menuOpen = ref(false)
let handleDownAt = { x: 0, y: 0 }

function onHandleMouseDown(event: MouseEvent) {
  handleDownAt = { x: event.clientX, y: event.clientY }
}

function onHandleClick(event: MouseEvent) {
  if (Math.hypot(event.clientX - handleDownAt.x, event.clientY - handleDownAt.y) > 5) return
  if (props.hasOutgoing) return
  menuOpen.value = !menuOpen.value
}

function pickKind(kind: WorkflowNodeType) {
  menuOpen.value = false
  emit('quick-add', kind)
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

/* The + lives INSIDE the handle box, centered on the connection anchor —
   edges and the affordance can never drift apart. Purely visual: pointer
   events pass through to the Handle so connection drags always work. */
.wf-node-quickadd {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  color: var(--td-brand-color);
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 50%;
  width: 20px;
  height: 20px;
  box-shadow: var(--td-shadow-1);
  pointer-events: none;
  opacity: 0;
  transform: scale(0.8);
  transition: opacity 0.15s ease, transform 0.15s ease;
}

.wf-node:hover .wf-node-quickadd,
.wf-node--selected .wf-node-quickadd {
  opacity: 1;
  transform: scale(1);
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
