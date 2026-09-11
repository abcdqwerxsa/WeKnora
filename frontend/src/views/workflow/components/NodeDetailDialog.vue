<template>
  <t-dialog
    :visible="visible"
    :header="headerTitle"
    width="1180px"
    :footer="false"
    :close-btn="true"
    :close-on-overlay-click="true"
    dialog-class-name="wf-node-detail-dialog"
    @update:visible="emit('update:visible', $event)"
  >
    <div class="wf-detail">
      <div class="wf-detail-toolbar">
        <span class="wf-detail-node-id">{{ nodeId }}</span>
        <div class="wf-detail-actions">
          <t-button
            v-if="pinnable"
            size="small"
            :theme="pinned ? 'warning' : 'default'"
            variant="dashed"
            :disabled="!pinned && outputValue === null"
            :title="t('workflow.editor.pinHint')"
            @click="togglePin"
          >
            {{ pinned ? t('workflow.editor.unpin') : t('workflow.editor.pin') }}
          </t-button>
          <t-button
            v-if="runnable"
            size="small"
            theme="primary"
            variant="outline"
            :loading="running"
            @click="run"
          >
            {{ t('workflow.editor.runNodeGo') }}
          </t-button>
        </div>
      </div>

      <div class="wf-detail-cols" :style="colsStyle">
        <!-- Left: INPUT — direct upstream outputs, editable JSON (n8n pinned-data style).
             With multiple upstreams an InputNodeSelect-style dropdown picks
             the one under edit instead of stacking them all. The Start node
             (no upstreams) renders its own form here instead: filling it
             materialises the entry fixture when the node is test-run. -->
        <aside class="wf-detail-pane">
          <div class="wf-detail-pane-head">
            <p class="wf-detail-pane-title">{{ t('workflow.editor.nodeInputs') }}</p>
            <t-select
              v-if="upstreamList.length > 1"
              :value="activeUpstreamId ?? upstreamList[0]?.id"
              size="small"
              style="width: 150px"
              @change="activeUpstreamId = String($event)"
            >
              <t-option
                v-for="up in upstreamList"
                :key="up.id"
                :value="up.id"
                :label="`${up.label} · ${up.id}`"
              />
            </t-select>
          </div>
          <template v-if="isStart">
            <p class="wf-detail-form-hint">{{ t('workflow.editor.startFormHint') }}</p>
            <t-textarea
              v-model="startForm.query"
              :autosize="{ minRows: 2, maxRows: 4 }"
              :placeholder="t('workflow.run.queryPlaceholder')"
            />
            <RunAttachmentsField v-model="startAttachments" :workflow-id="workflowId" />
            <t-form label-align="top" size="small">
              <t-form-item
                v-for="field in startFields"
                :key="field.name"
                :label="field.label || field.name"
                :mark="field.required"
              >
                <t-select v-if="field.type === 'select'" v-model="startForm.values[field.name]" clearable :placeholder="field.name">
                  <t-option v-for="option in field.options ?? []" :key="option" :value="option" :label="option" />
                </t-select>
                <t-textarea v-else-if="field.type === 'paragraph'" v-model="startForm.values[field.name]" :autosize="{ minRows: 1, maxRows: 4 }" :placeholder="field.name" />
                <t-input-number v-else-if="field.type === 'number'" v-model="startForm.values[field.name]" theme="column" :placeholder="field.name" />
                <t-input v-else v-model="startForm.values[field.name]" :placeholder="field.name" />
              </t-form-item>
            </t-form>
          </template>
          <div v-else-if="upstreams.length === 0" class="wf-detail-empty">
            {{ t('workflow.editor.runNodeNoUpstream') }}
          </div>
          <div v-for="up in visibleUpstreams" :key="up.id" class="wf-detail-up">
            <p class="wf-detail-up-title">
              <span class="wf-detail-dot" :style="{ background: up.color }" />
              {{ up.label }}
              <span class="wf-detail-up-id">{{ up.id }}</span>
            </p>
            <textarea
              class="wf-detail-json"
              rows="10"
              spellcheck="false"
              :value="draftOf(up.id).draft"
              :placeholder="t('workflow.editor.runNodeJsonPlaceholder')"
              @input="setDraft(up.id, ($event.target as HTMLTextAreaElement).value)"
            />
            <p v-if="draftOf(up.id).parseError" class="wf-detail-err">{{ draftOf(up.id).parseError }}</p>
          </div>
        </aside>

        <span
          class="wf-detail-resize"
          role="separator"
          aria-orientation="vertical"
          @mousedown="startResize('left', $event)"
        />

        <!-- Middle: the node's property form (the old right-side drawer body). -->
        <section class="wf-detail-main">
          <slot />
        </section>

        <span
          class="wf-detail-resize"
          role="separator"
          aria-orientation="vertical"
          @mousedown="startResize('right', $event)"
        />

        <!-- Right: OUTPUT — this node's execution result. -->
        <aside class="wf-detail-pane">
          <div class="wf-detail-pane-head">
            <p class="wf-detail-pane-title">{{ t('workflow.editor.nodeOutputs') }}</p>
            <span class="wf-detail-view-toggle">
              <button
                type="button"
                :class="['wf-detail-view-btn', { 'is-active': viewMode === 'schema' }]"
                @click="viewMode = 'schema'"
              >
                {{ t('workflow.editor.outputViewSchema') }}
              </button>
              <button
                type="button"
                :class="['wf-detail-view-btn', { 'is-active': viewMode === 'table' }]"
                :disabled="!tableable"
                @click="viewMode = 'table'"
              >
                {{ t('workflow.editor.outputViewTable') }}
              </button>
              <button
                type="button"
                :class="['wf-detail-view-btn', { 'is-active': viewMode === 'tree' }]"
                @click="viewMode = 'tree'"
              >
                {{ t('workflow.editor.outputViewTree') }}
              </button>
              <button
                type="button"
                :class="['wf-detail-view-btn', { 'is-active': viewMode === 'json' }]"
                @click="viewMode = 'json'"
              >
                {{ t('workflow.editor.outputViewJson') }}
              </button>
            </span>
          </div>
          <p v-if="copiedRef" class="wf-detail-copied">{{ t('workflow.editor.refCopied', { ref: copiedRef }) }}</p>
          <template v-if="viewMode === 'schema'">
            <OutputSchemaView v-if="outputValue !== null && typeof outputValue === 'object'" :value="outputValue" />
            <div v-else class="wf-detail-empty">{{ t('workflow.editor.runNodeNoOutput') }}</div>
          </template>
          <template v-else-if="viewMode === 'table'">
            <OutputTableView :value="outputValue" />
          </template>
          <template v-else-if="viewMode === 'tree'">
            <JsonTreeView
              v-if="outputValue !== null && typeof outputValue === 'object'"
              :value="outputValue"
              :node-id="nodeId"
              @copied="flashCopied"
            />
            <pre v-else-if="outputShown !== null" class="wf-detail-json-out" :class="{ 'wf-detail-json-out--err': failed }">{{ outputShown }}</pre>
            <div v-else class="wf-detail-empty">{{ t('workflow.editor.runNodeNoOutput') }}</div>
          </template>
          <pre v-else-if="outputShown !== null" class="wf-detail-json-out" :class="{ 'wf-detail-json-out--err': failed }">{{ outputShown }}</pre>
          <div v-else class="wf-detail-empty">{{ t('workflow.editor.runNodeNoOutput') }}</div>
          <p v-if="runError" class="wf-detail-err">{{ runError }}</p>
        </aside>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { runWorkflowNode, type WorkflowRunTraceEntry, type WorkflowNodeType } from '@/api/workflow'
import { NODE_COLORS } from '../nodeMeta'
import JsonTreeView from './JsonTreeView.vue'
import OutputTableView from './OutputTableView.vue'
import OutputSchemaView from './OutputSchemaView.vue'
import RunAttachmentsField from './RunAttachmentsField.vue'
import type { WorkflowRunAttachment } from '@/api/workflow'

/** One upstream node for the INPUT pane. */
export interface DetailUpstream {
  id: string
  kind: WorkflowNodeType
  /** Last-known outputs (previous run) prefilled as the editable input. */
  outputs?: Record<string, unknown> | null
  /** Shape hint for Start upstreams (query key + field defaults). */
  seed?: Record<string, unknown> | null
}

const props = defineProps<{
  visible: boolean
  workflowId: string
  nodeId: string
  nodeLabel: string
  /** Kind of the node under edit (drives the Start form in the INPUT pane). */
  nodeKind?: WorkflowNodeType
  /** Show the test-step button (false for Iteration, which the backend
   * rejects as not runnable in isolation). Start IS runnable: its test
   * step materialises the filled form as the entry fixture. */
  runnable: boolean
  upstreams: DetailUpstream[]
  /** Start-node form fields (query + these materialise into outputs). */
  startFields?: Array<{ name: string; label?: string; type: string; required?: boolean; default?: string; options?: string[] }>
  /** Frozen outputs persisted in the DSL (n8n pinned data); non-null pins
   * the node: full runs skip it and replay these values. */
  pinned?: Record<string, unknown> | null
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  /** Fired after a successful single-node run so the editor's outputs
   * cache (canvas cards, future debug prefill) stays in sync. */
  'node-output': [nodeId: string, outputs: Record<string, unknown>]
  /** Pin/unpin the node's frozen outputs in the DSL (null un-pins). */
  'set-pinned': [nodeId: string, outputs: Record<string, unknown> | null]
}>()

const { t } = useI18n()
const running = ref(false)
const runError = ref('')
const outputShown = ref<string | null>(null)
const failed = ref(false)
const viewMode = ref<'schema' | 'table' | 'tree' | 'json'>('schema')
const copiedRef = ref('')
const activeUpstreamId = ref<string | null>(null)
let copiedTimer: number | null = null

// ---- pinned data ----
const isStart = computed(() => props.nodeKind === 'Start')
// The Start node's INPUT pane is a form (query + declared fields); running
// the node materialises it as the entry fixture for downstream debugging.
const startForm = ref<{ query: string; values: Record<string, string | number | undefined> }>({ query: '', values: {} })
const startAttachments = ref<WorkflowRunAttachment[]>([])
watch(
  () => [props.visible, props.nodeId] as const,
  ([visible]) => {
    if (!visible || !isStart.value) return
    const values: Record<string, string | number | undefined> = {}
    for (const field of props.startFields ?? []) values[field.name] = field.default ?? ''
    startForm.value = { query: '', values }
  },
  { immediate: true },
)

const pinned = computed(() => props.pinned != null && Object.keys(props.pinned).length > 0)
// Anything with outputs can be pinned (Start included — a pinned Start
// freezes the entry fixture the whole flow develops against).
const pinnable = computed(() => props.nodeId !== '')

function togglePin() {
  if (pinned.value) {
    emit('set-pinned', props.nodeId, null)
    return
  }
  const snapshot = (outputValue.value ?? null) as Record<string, unknown> | null
  // An empty snapshot pins nothing (engine treats len==0 as unpinned);
  // skipping keeps the badge and the run behaviour consistent.
  if (snapshot !== null && typeof snapshot === 'object' && Object.keys(snapshot).length > 0) {
    emit('set-pinned', props.nodeId, snapshot)
  }
}

// Upstream selector (n8n InputNodeSelect): one upstream under edit at a
// time when several feed this node; single upstream needs no selector.
const visibleUpstreams = computed(() => {
  if (upstreamList.value.length <= 1) return upstreamList.value
  const active = activeUpstreamId.value ?? upstreamList.value[0]?.id
  return upstreamList.value.filter((up) => up.id === active)
})

// Table view applies when the output is (or contains) an array of
// keyed objects (empty objects carry no columns — not tableable).
const tableable = computed(() => {
  const v = outputValue.value
  const isObjArr = (x: unknown) =>
    Array.isArray(x) && x.length > 0 && x.every((i) => i !== null && typeof i === 'object' && !Array.isArray(i) && Object.keys(i).length > 0)
  if (Array.isArray(v)) return isObjArr(v)
  if (v !== null && typeof v === 'object') {
    return Object.values(v as Record<string, unknown>).some(isObjArr)
  }
  return false
})

// Prefer table automatically for array-ish outputs (n8n default for item
// lists); schema for object payloads, tree otherwise. Re-evaluated when a
// new output arrives.
watch(outputShown, () => {
  viewMode.value = tableable.value ? 'table' : outputValue.value !== null && typeof outputValue.value === 'object' ? 'schema' : 'tree'
})

// ---- pane widths (n8n PanelDragButton semantics): drag the separators,
// remember per browser. ----
const PANE_MIN = 220
const PANE_MAX = 520
const leftWidth = ref(clampPane(Number(localStorage.getItem('wf.ndv.leftW')) || 290))
const rightWidth = ref(clampPane(Number(localStorage.getItem('wf.ndv.rightW')) || 290))
const colsStyle = computed(() => ({
  gridTemplateColumns: `${leftWidth.value}px 7px minmax(0, 1fr) 7px ${rightWidth.value}px`,
}))

function persistWidths() {
  localStorage.setItem('wf.ndv.leftW', String(leftWidth.value))
  localStorage.setItem('wf.ndv.rightW', String(rightWidth.value))
}

function clampPane(w: number): number {
  return Math.min(PANE_MAX, Math.max(PANE_MIN, w))
}

function onResizeMove(which: 'left' | 'right', startX: number, startW: number, e: MouseEvent) {
  const dx = which === 'left' ? e.clientX - startX : startX - e.clientX
  const w = clampPane(startW + dx)
  if (which === 'left') leftWidth.value = w
  else rightWidth.value = w
}

let stopResize: (() => void) | null = null

function startResize(which: 'left' | 'right', down: MouseEvent) {
  const startX = down.clientX
  const startW = which === 'left' ? leftWidth.value : rightWidth.value
  const move = (e: MouseEvent) => onResizeMove(which, startX, startW, e)
  const up = () => {
    window.removeEventListener('mousemove', move)
    window.removeEventListener('mouseup', up)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    persistWidths()
    stopResize = null
  }
  document.body.style.cursor = 'ew-resize'
  document.body.style.userSelect = 'none'
  window.addEventListener('mousemove', move)
  window.addEventListener('mouseup', up)
  stopResize = up
}
onBeforeUnmount(() => {
  if (copiedTimer !== null) window.clearTimeout(copiedTimer)
  stopResize?.()
})

function flashCopied(refStr: string) {
  copiedRef.value = refStr
  if (copiedTimer !== null) window.clearTimeout(copiedTimer)
  copiedTimer = window.setTimeout(() => (copiedRef.value = ''), 1500)
}

// Tree view renders the parsed output; raw pre falls back for primitives.
const outputValue = computed<unknown>(() => {
  if (outputShown.value === null) return null
  try {
    return JSON.parse(outputShown.value)
  } catch {
    return null
  }
})

// One editable JSON draft per upstream (nodeId -> {draft, parseError}).
const drafts = reactive(new Map<string, { draft: string; parseError: string }>())

const headerTitle = computed(() => t('workflow.editor.runNodeTitle', { name: props.nodeLabel }))

const upstreamList = computed(() =>
  props.upstreams.map((up) => ({
    ...up,
    label: t(`workflow.nodes.${up.kind}`),
    color: NODE_COLORS[up.kind] ?? '#9aa4b2',
  })),
)

function draftOf(id: string): { draft: string; parseError: string } {
  return drafts.get(id) ?? { draft: '{}', parseError: '' }
}

function setDraft(id: string, value: string) {
  const prev = drafts.get(id)
  drafts.set(id, { draft: value, parseError: prev?.parseError ?? '' })
}

// Re-seed the editable inputs every time the dialog opens for a node.
watch(
  () => [props.visible, props.nodeId] as const,
  ([visible]) => {
    if (!visible) return
    drafts.clear()
    activeUpstreamId.value = null
    for (const up of props.upstreams) {
      drafts.set(up.id, { draft: JSON.stringify(up.outputs ?? up.seed ?? {}, null, 2), parseError: '' })
    }
    runError.value = ''
    failed.value = false
    // Pinned nodes show their frozen fixture, not a stale run output.
    outputShown.value = props.pinned && Object.keys(props.pinned).length > 0 ? JSON.stringify(props.pinned, null, 2) : null
    viewMode.value = tableable.value ? 'table' : props.pinned && Object.keys(props.pinned).length > 0 ? 'schema' : 'tree'
  },
  { immediate: true },
)

function pretty(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

async function run() {
  const inputs: Record<string, Record<string, unknown>> = {}

  if (isStart.value) {
    // Start's test step: materialise the filled form as the node's outputs
    // (the backend promotes the fixture to the run request; the node's
    // echo produces the outputs) — downstream INPUT panes pick it up.
    const fixture: Record<string, unknown> = {}
    if (startForm.value.query.trim() !== '') fixture.query = startForm.value.query
    for (const [k, v] of Object.entries(startForm.value.values)) {
      if (v !== undefined && String(v) !== '') fixture[k] = v
    }
    const readyFiles = startAttachments.value.filter((a) => a.status === 'ready').map((a) => a.id)
    if (readyFiles.length > 0) fixture.files = readyFiles
    inputs[props.nodeId] = fixture
  } else {
    // Parse every upstream draft first — abort on the first syntax error.
    for (const up of props.upstreams) {
      const raw = draftOf(up.id).draft || '{}'
      try {
        const parsed = JSON.parse(raw)
        if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
          throw new Error('expected a JSON object')
        }
        inputs[up.id] = parsed as Record<string, unknown>
        drafts.set(up.id, { draft: raw, parseError: '' })
      } catch (e) {
        drafts.set(up.id, { draft: raw, parseError: e instanceof Error ? e.message : String(e) })
        return
      }
    }
  }

  running.value = true
  runError.value = ''
  outputShown.value = null
  failed.value = false
  try {
    const res = await runWorkflowNode(props.workflowId, props.nodeId, { inputs })
    const run = res?.run
    if (!run) {
      runError.value = res?.message || t('workflow.editor.runNodeFailed')
      return
    }
    const trace: WorkflowRunTraceEntry[] = Array.isArray(run.trace) ? run.trace : []
    const entry = trace.find((item) => item.node_id === props.nodeId)
    if (run.status === 'failed') {
      failed.value = true
      runError.value = entry?.error || run.error || t('workflow.editor.runNodeFailed')
    }
    const outputs = (entry?.outputs ?? run.output ?? {}) as Record<string, unknown>
    outputShown.value = pretty(outputs)
    if (run.status !== 'failed') emit('node-output', props.nodeId, outputs)
  } catch (e) {
    // Transport-level failure (400 node-not-runnable, 403 gate, network).
    runError.value = e instanceof Error ? e.message : String(e) || t('workflow.editor.runNodeFailed')
  } finally {
    running.value = false
  }
}
</script>

<style scoped>
.wf-detail {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.wf-detail-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.wf-detail-node-id {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  font-family: var(--td-font-family-code);
}

.wf-detail-cols {
  display: grid;
  grid-template-columns: 290px 7px minmax(0, 1fr) 7px 290px;
  gap: 0;
  height: min(66vh, 640px);
}

.wf-detail-resize {
  width: 7px;
  cursor: ew-resize;
  border-radius: 3px;
  background: transparent;
  transition: background 0.15s;
  align-self: stretch;
}

.wf-detail-resize:hover,
.wf-detail-resize:active {
  background: var(--td-component-stroke);
}

.wf-detail-pane-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex: none;
}

.wf-detail-view-toggle {
  display: inline-flex;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium);
  overflow: hidden;
}

.wf-detail-view-btn {
  border: none;
  background: transparent;
  padding: 2px 8px;
  font-size: 11px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
}

.wf-detail-view-btn:disabled {
  cursor: not-allowed;
  opacity: 0.4;
}

.wf-detail-view-btn.is-active {
  background: var(--td-brand-color);
  color: var(--td-text-color-anti);
}

.wf-detail-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.wf-detail-copied {
  margin: 0 0 4px;
  font-size: 11px;
  color: var(--td-success-color);
  font-family: var(--td-font-family-code);
  word-break: break-all;
}

.wf-detail-form-hint {
  margin: 0;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}

.wf-detail-pane {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-height: 0;
  overflow-y: auto;
}

.wf-detail-pane-title {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.5px;
  text-transform: uppercase;
  color: var(--td-text-color-placeholder);
  flex: none;
}

.wf-detail-main {
  min-height: 0;
  overflow-y: auto;
  padding: 0 4px;
}

.wf-detail-empty {
  padding: 24px 12px;
  text-align: center;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--td-radius-medium);
}

.wf-detail-up {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.wf-detail-up-title {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  font-size: 13px;
  font-weight: 500;
}

.wf-detail-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}

.wf-detail-up-id {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  font-family: var(--td-font-family-code);
}

.wf-detail-json {
  width: 100%;
  box-sizing: border-box;
  resize: vertical;
  font-family: var(--td-font-family-code);
  font-size: 12px;
  line-height: 1.6;
  padding: 8px 10px;
  border-radius: var(--td-radius-medium);
  border: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
}

.wf-detail-json:focus {
  outline: none;
  border-color: var(--td-brand-color);
}

.wf-detail-json-out {
  margin: 0;
  overflow: auto;
  font-family: var(--td-font-family-code);
  font-size: 12px;
  line-height: 1.6;
  padding: 10px 12px;
  border-radius: var(--td-radius-medium);
  border: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-graycontainer);
  white-space: pre-wrap;
  word-break: break-all;
}

.wf-detail-json-out--err {
  border-color: var(--td-error-color);
}

.wf-detail-err {
  margin: 0;
  font-size: 12px;
  color: var(--td-error-color);
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
