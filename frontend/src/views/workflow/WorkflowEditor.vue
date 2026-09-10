<template>
  <div class="wf-editor">
    <div class="wf-editor-toolbar">
      <div class="wf-editor-toolbar-left">
        <t-button variant="text" theme="default" @click="goBack">
          <template #icon><t-icon name="arrow-left" /></template>
        </t-button>
        <t-input v-model="name" class="wf-editor-name" :placeholder="$t('workflow.name')" :maxlength="255" />
        <t-tag v-if="workflow" size="small" theme="warning">{{ $t(`workflow.status.${workflow.status}`) }}</t-tag>
      </div>
      <div class="wf-editor-toolbar-right">
        <t-button variant="outline" :disabled="!ready" @click="variablesDrawerVisible = true">
          <template #icon><t-icon name="braces" /></template>
          {{ $t('workflow.editor.variablesBtn') }}
        </t-button>
        <t-button variant="outline" @click="applyAutoLayout" :disabled="!ready">
          <template #icon><t-icon name="layout" /></template>
          {{ $t('workflow.editor.autoLayout') }}
        </t-button>
        <t-button variant="outline" @click="importDslFile?.click()">
          <template #icon><t-icon name="upload" /></template>
          {{ $t('workflow.editor.importDsl') }}
        </t-button>
        <t-button variant="outline" :disabled="!ready" @click="exportDsl">
          <template #icon><t-icon name="download" /></template>
          {{ $t('workflow.editor.exportDsl') }}
        </t-button>
        <span v-if="lastSavedAt" class="wf-editor-autosaved">{{ t('workflow.editor.autoSavedAt', { time: autoSavedTime }) }}</span>
        <t-button theme="primary" :loading="saving" :disabled="!ready" @click="doSave">
          {{ saveLabel }}
        </t-button>
        <t-button variant="outline" :loading="publishing" :disabled="!ready" @click="publishFromEditor">
          <template #icon><t-icon name="cloud-upload" /></template>
          {{ workflow?.status === 'published' ? $t('workflow.republish') : $t('workflow.publish') }}
        </t-button>
        <t-button variant="outline" :disabled="!ready" @click="runDrawerVisible = true">
          <template #icon><t-icon name="play-circle" /></template>
          {{ $t('workflow.run.open') }}
        </t-button>
      </div>
      <input ref="importDslFile" type="file" accept="application/json,.json" class="wf-editor-file-input" @change="onImportFile" />
    </div>

    <div v-if="loading" class="wf-editor-state">
      <t-loading />
    </div>
    <div v-else-if="loadError" class="wf-editor-state">
      <p>{{ loadErrorDetail || $t('workflow.editor.loadFailed') }}</p>
      <t-button variant="outline" @click="goBack">{{ $t('workflow.editor.back') }}</t-button>
    </div>

    <div v-show="ready" class="wf-editor-canvas">
      <NodePalette :has-start="hasStartNode" @add="addNodeFromPalette" />
      <div
        ref="flowEl"
        class="wf-editor-flow"
        :class="{ 'wf-editor-flow--comment': canvasMode === 'comment' }"
        @dragover.prevent
        @drop.prevent="onDropNode"
        @mousedown.middle.prevent
      >
        <!-- Dify-style canvas toolbar: pointer (V, box multi-select), hand
             (H, or hold Space temporarily), comment (C, click to place). -->
        <div class="wf-canvas-toolbar">
          <button
            type="button"
            class="wf-canvas-tool"
            :class="{ 'wf-canvas-tool--active': canvasMode === 'pointer' }"
            :title="`${t('workflow.editor.modePointer')} (V)`"
            @click="setMode('pointer')"
          >
            <t-icon name="cursor" />
          </button>
          <button
            type="button"
            class="wf-canvas-tool"
            :class="{ 'wf-canvas-tool--active': canvasMode === 'hand' }"
            :title="`${t('workflow.editor.modeHand')} (H · Space)`"
            @click="setMode('hand')"
          >
            <t-icon name="drag-move" />
          </button>
          <button
            type="button"
            class="wf-canvas-tool"
            :class="{ 'wf-canvas-tool--active': canvasMode === 'comment' }"
            :title="`${t('workflow.editor.modeComment')} (C)`"
            @click="setMode('comment')"
          >
            <t-icon name="chat" />
          </button>
        </div>
        <VueFlow
          ref="flowRef"
          v-model:nodes="canvasNodes"
          v-model:edges="canvasEdges"
          fit-view-on-init
          :min-zoom="0.25"
          :max-zoom="2"
          :default-edge-options="defaultEdgeOptions"
          :connection-radius="36"
          :connect-on-click="false"
          :pan-on-drag="effectiveMode === 'hand' || [1]"
          :nodes-draggable="effectiveMode !== 'comment'"
          :selection-key-code="effectiveMode === 'pointer'"
          :selection-mode="SelectionMode.Partial"
          :delete-key-code="null"
          @pane-click="onPaneClick"
          @move-end="onMoveEnd"
          @connect-end="onConnectEnd"
          @connect="onConnect"
          @node-click="onNodeClick"
          @edge-click="onEdgeClick"
          @edge-double-click="onEdgeDoubleClick"

        >
          <!-- Custom default edge: Dify-style midpoint + inserts a node
               between source and target. -->
          <template #edge-default="edgeProps">
            <WfEdge
              v-bind="edgeProps"
              @insert="(kind) => onEdgeInsert(String(edgeProps.id), kind, edgeProps)"
            />
          </template>
          <Background :gap="20" />
          <Controls position="bottom-left" />
          <MiniMap position="bottom-right" pannable zoomable />
          <template #node-wf-note="nodeProps">
            <div class="wf-note" :class="{ 'wf-note--selected': nodeProps.selected }">
              <textarea
                class="wf-note-textarea"
                :value="(nodeProps.data?.text as string) ?? ''"
                :placeholder="t('workflow.editor.notePlaceholder')"
                @mousedown.stop
                @input="updateNoteText(String(nodeProps.id), ($event.target as HTMLTextAreaElement).value)"
              />
            </div>
          </template>
          <template #node-wf="nodeProps">
            <WfNodeCard
              :kind="(nodeProps.data?.kind as WorkflowNodeType) ?? 'Answer'"
              :selected="nodeProps.selected"
              :subtitle="nodeSubtitle(nodeProps.data)"
              :run-phase="runNodePhases[nodeProps.id]"
              :outputs="runNodeOutputs[nodeProps.id]"
              :node-id="nodeProps.id"
              :branches="branchHandlesOf(nodeProps)"
              :connected-handles="connectedHandlesOf(nodeProps.id)"
              @quick-add="(kind, handleId) => onQuickAdd(String(nodeProps.id), kind, handleId)"
            />
          </template>
        </VueFlow>
        <p class="wf-editor-hint">{{ $t('workflow.editor.deleteHint') }}</p>
      </div>
    </div>

    <t-drawer
      v-model:visible="drawerVisible"
      :header="$t('workflow.editor.properties')"
      size="360px"
      :footer="false"
      :close-btn="true"
      :show-overlay="false"
    >
      <NodePropertyForm
        v-if="selectedParams && selectedKind"
        :kind="selectedKind"
        :current-node-id="selectedNodeId ?? ''"
        :params="selectedParams"
        :nodes="pickerNodes"
        :edges="canvasEdges"
        :chat-models="chatModels"
        :rerank-models="rerankModels"
        :kbs="kbs"
        :env-names="envNames"
        :web-search-providers="webSearchProviders"
        :parent="selectedParent"
        @set-parent="setSelectedParent"
      />
      <div v-else class="wf-editor-form-empty">
        {{ $t('workflow.editor.selectNode') }}
      </div>
    </t-drawer>
    <t-drawer
      v-model:visible="runDrawerVisible"
      :header="$t('workflow.run.title')"
      size="420px"
      :footer="false"
      :close-btn="true"
      :show-overlay="false"
      @closed="onRunDrawerClosed"
    >
      <WorkflowRunPanel
        :workflow-id="workflowId"
        :nodes="pickerNodes"
        :start-fields="startFields"
        :published-stale="publishedStale"
        @node-phases="runNodePhases = $event"
        @node-outputs="runNodeOutputs = $event"
      />
    </t-drawer>
    <t-drawer
      v-model:visible="variablesDrawerVisible"
      :header="$t('workflow.editor.variablesDrawer')"
      size="360px"
      :footer="false"
      :close-btn="true"
      :show-overlay="false"
    >
      <div class="wf-editor-vars">
        <p class="wf-editor-vars-hint">{{ $t('workflow.editor.variablesHint') }}</p>
        <div v-for="key in envNames" :key="key" class="wf-editor-vars-row">
          <t-input
            :value="key"
            class="wf-editor-vars-name"
            :placeholder="t('workflow.editor.variablesName')"
            @change="renameVariable(key, String($event))"
          />
          <t-input
            :value="String(wfVariables[key] ?? '')"
            :placeholder="t('workflow.editor.variablesValue')"
            @change="wfVariables = { ...wfVariables, [key]: $event }"
          />
          <t-button variant="text" theme="danger" size="small" @click="removeVariable(key)">
            <template #icon><t-icon name="delete" /></template>
          </t-button>
        </div>
        <t-button variant="dashed" size="small" block @click="addVariable">
          {{ $t('workflow.editor.addVariable') }}
        </t-button>
      </div>
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch, type Ref } from 'vue'
import { useRoute, onBeforeRouteLeave, useRouter } from 'vue-router'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { VueFlow, MarkerType, SelectionMode, type Connection, type Edge, type EdgeMouseEvent, type Node, type NodeMouseEvent } from '@vue-flow/core'
import { select } from 'd3-selection'
import { Background } from '@vue-flow/background'
import { Controls } from '@vue-flow/controls'
import { MiniMap } from '@vue-flow/minimap'
// vue-flow base styles must be global (not scoped): its internal DOM
// (handles/edges/pane) never receives this component's scope attribute.
import '@vue-flow/core/dist/style.css'
import '@vue-flow/core/dist/theme-default.css'
import '@vue-flow/controls/dist/style.css'
import '@vue-flow/minimap/dist/style.css'
import WfNodeCard from './components/WfNodeCard.vue'
import WfEdge from './components/WfEdge.vue'
import NodePalette from './components/NodePalette.vue'
import NodePropertyForm from './components/NodePropertyForm.vue'
import WorkflowRunPanel from './components/WorkflowRunPanel.vue'
import { WORKFLOW_NODE_TYPES, getWorkflow, updateWorkflow, publishWorkflow, type Workflow, type WorkflowDSL, type WorkflowNodeType } from '@/api/workflow'
import { buildDsl, defaultParams, makeNodeId, migrateNodeParams, normalizeDsl, autoLayout, validateGraph, type GraphIssue } from './dsl'
import { paramSummary } from './nodeMeta'
import { listModels, type ModelConfig } from '@/api/model'
import { listKnowledgeBases } from '@/api/knowledge-base'
import { listWebSearchProviders } from '@/api/web-search-provider'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const workflowId = computed(() => String(route.params.id ?? ''))

const loading = ref(true)
const loadError = ref(false)
const loadErrorDetail = ref('')
const name = ref('')
const saving = ref(false)

// Workflow-level variables (DSL.variables → runtime env.*). Edited in the
// variables drawer; saved as part of the DSL document.
const wfVariables = ref<Record<string, unknown>>({})
const variablesDrawerVisible = ref(false)
const envNames = computed(() => Object.keys(wfVariables.value).filter(Boolean))

// Start-node form fields, rendered as run inputs by the run panel.
const startFields = computed<Array<{ name: string; label?: string; type: string; required?: boolean; default?: string; options?: string[] }>>(() => {
  const start = pickerNodes.value.find((node) => node.kind === 'Start')
  const fields = start?.params?.fields
  return Array.isArray(fields)
    ? (fields as Array<{ name: string; label?: string; type: string; required?: boolean; default?: string; options?: string[] }>).filter((f) => f?.name)
    : []
})

function addVariable() {
  const base = 'var'
  let n = 1
  while (wfVariables.value[`${base}${n}`] !== undefined) n += 1
  wfVariables.value = { ...wfVariables.value, [`${base}${n}`]: '' }
}

function removeVariable(key: string) {
  const next = { ...wfVariables.value }
  delete next[key]
  wfVariables.value = next
}

function renameVariable(oldKey: string, rawName: string) {
  const name = rawName.trim().replace(/\s+/g, '_')
  if (!name || name === oldKey) return
  const next: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(wfVariables.value)) {
    next[key === oldKey ? name : key] = value
  }
  wfVariables.value = next
}

const workflow = ref<Workflow | null>(null)

// Cast to Ref<Node[]>: letting ref() infer UnwrapRef<Node> trips
// TS2589 (excessively deep) on vue-flow's heavily generic node type.
const canvasNodes = ref([]) as Ref<Node[]>
// VueFlow instance (template ref) for screenToFlowCoordinate in comment mode.
const flowRef = ref()
const flowInstance = computed(() => flowRef.value)
// The flow wrapper element: its rect gives the viewport centre for
// click-to-add placement (nodes must land where the user is looking).
const flowEl = ref<HTMLDivElement | null>(null)
const canvasEdges = ref([]) as Ref<Edge[]>

// ---- typed property-form data sources ------------------------------------
// Model / KB pickers degrade to manual entry when the list APIs fail
// (e.g. cross-workspace share edge cases) — the form stays usable.
const chatModels = ref<ModelConfig[]>([])
const rerankModels = ref<ModelConfig[]>([])
const kbs = ref<Array<{ id: string; name: string }>>([])
const webSearchProviders = ref<Array<{ id: string; name: string }>>([])

const pickerNodes = computed(() =>
  canvasNodes.value.map((node) => ({
    id: node.id,
    kind: (node.data?.kind as WorkflowNodeType) ?? 'Answer',
    params: (node.data?.params as Record<string, unknown>) ?? {},
  }))
)

/**
 * True when runs execute the frozen published snapshot while the saved
 * draft has diverged (dslForRun: published status → PublishedDSL). Without
 * this flag, editor-side param changes silently never reach test runs
 * until the workflow is republished.
 */
const publishedStale = computed(() => {
  const wf = workflow.value
  if (!wf || wf.status !== 'published' || !wf.published_dsl) return false
  return JSON.stringify(wf.dsl) !== JSON.stringify(wf.published_dsl)
})

async function fetchPickerData() {
  try {
    const models = await listModels()
    const list = Array.isArray(models) ? models : []
    chatModels.value = list.filter((model) => model.type === 'KnowledgeQA')
    rerankModels.value = list.filter((model) => model.type === 'Rerank')
  } catch {
    // keep empty pickers; forms fall back to raw input
  }
  try {
    const response: any = await listKnowledgeBases()
    // Same shape as stores/chatResources: res.data IS the array.
    const items = response?.data && Array.isArray(response.data) ? response.data : []
    kbs.value = items.map((item: { id: string; name?: string }) => ({ id: String(item.id), name: String(item.name ?? item.id) }))
  } catch {
    // keep empty pickers
  }
  try {
    const response = await listWebSearchProviders()
    const items = ((response as unknown as { data?: unknown })?.data ?? response ?? []) as Array<{ id: string; name?: string }>
    webSearchProviders.value = Array.isArray(items)
      ? items.map((item) => ({ id: String(item.id), name: String(item.name ?? item.id) }))
      : []
  } catch {
    // keep empty pickers
  }
}

const selectedNodeId = ref<string | null>(null)
const selectedEdgeId = ref<string | null>(null)
const importDslFile = ref<HTMLInputElement | null>(null)

// The palette disables Start (and adds warn) once a Start node exists —
// a second one can never compile (single entry).
const hasStartNode = computed(() => canvasNodes.value.some((node) => node.data?.kind === 'Start'))

function onPaneClick(event: MouseEvent) {
  if (canvasMode.value === 'comment') {
    // Place the note at the flow coordinates under the click.
    const flow = flowInstance.value
    if (flow) {
      const pos = flow.screenToFlowCoordinate({ x: event.clientX, y: event.clientY })
      addNoteAt(pos)
      canvasMode.value = 'pointer'
      return
    }
  }
  clearSelection()
}

/** After any pan/zoom drag, d3 installs a one-shot window-capture "click"
 *  swallow (d3-drag yesdrag → noevent) to eat the click that ended the
 *  gesture. It is window-wide: the first click after a pan would be eaten
 *  anywhere — including the node palette — which reads as "adding nodes is
 *  broken". vue-flow emits moveEnd synchronously right after the swallow is
 *  installed; drop it immediately so post-pan clicks land normally. */
function onMoveEnd() {
  select(window).on('click.drag', null)
}

/** vue-flow highlights the handle candidate under the pointer mid-drag by
 *  adding `connecting`/`valid` classes directly to the DOM, but never clears
 *  the last candidate when the drop lands — the highlight would stick.
 *  Clear it when the connection gesture ends. */
function onConnectEnd() {
  document
    .querySelectorAll('.vue-flow__handle.connecting, .vue-flow__handle.valid')
    .forEach((el) => el.classList.remove('connecting', 'valid', 'vue-flow__handle-connecting', 'vue-flow__handle-valid'))
}

function clearSelection() {
  selectedNodeId.value = null
  selectedEdgeId.value = null
}

// ---- selection + deletion -----------------------------------------------
// vue-flow keeps its own selected flags; we mirror the ids here so the
// Delete key path and the property drawer share one source of truth.
function onNodeClick(event: NodeMouseEvent) {
  selectedNodeId.value = event.node.id
  selectedEdgeId.value = null
}

function onEdgeClick(event: EdgeMouseEvent) {
  selectedEdgeId.value = event.edge.id
  selectedNodeId.value = null
}

function onEdgeDoubleClick(event: EdgeMouseEvent) {
  removeEdge(event.edge.id)
}

function removeEdge(edgeId: string) {
  canvasEdges.value = canvasEdges.value.filter((edge) => edge.id !== edgeId)
  if (selectedEdgeId.value === edgeId) selectedEdgeId.value = null
}

function removeNode(nodeId: string) {
  const node = canvasNodes.value.find((item) => item.id === nodeId)
  if (!node) return
  if ((node.data?.kind as WorkflowNodeType) === 'Start') {
    MessagePlugin.warning(t('workflow.editor.startProtected'))
    return
  }
  canvasNodes.value = canvasNodes.value.filter((item) => item.id !== nodeId)
  canvasEdges.value = canvasEdges.value.filter((edge) => edge.source !== nodeId && edge.target !== nodeId)
  if (selectedNodeId.value === nodeId) selectedNodeId.value = null
}

// ---- canvas modes (Dify-style) ---------------------------------------------
// pointer: box multi-select by dragging, nodes draggable, pane drag selects.
// hand: everything pans (nodes not draggable); Space holds it temporarily.
// comment: next pane click drops a sticky note there, then returns to pointer.
type CanvasMode = 'pointer' | 'hand' | 'comment'
const canvasMode = ref<CanvasMode>('pointer')
const spaceHeld = ref(false)
const effectiveMode = computed<CanvasMode>(() => (spaceHeld.value ? 'hand' : canvasMode.value))

function setMode(mode: CanvasMode) {
  canvasMode.value = mode
}

function updateNoteText(nodeId: string, text: string) {
  const node = canvasNodes.value.find((item) => item.id === nodeId)
  if (node) node.data = { ...(node.data ?? {}), text }
}

function addNoteAt(position: { x: number; y: number }) {
  canvasNodes.value.push({
    id: `note-${Date.now().toString(36)}`,
    type: 'wf-note',
    position,
    data: { text: '' },
  })
}

// Typing targets: Delete/Backspace must not fire while the user edits a
// form field — only delete when the focus is on the canvas itself.
function isTypingTarget(): boolean {
  const el = document.activeElement
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (el as HTMLElement).isContentEditable
}

function onKeyDown(event: KeyboardEvent) {
  // Undo/redo: Ctrl/Cmd+Z (+Shift for redo), canvas focus only.
  if ((event.ctrlKey || event.metaKey) && (event.key === 'z' || event.key === 'Z')) {
    if (isTypingTarget()) return
    event.preventDefault()
    if (event.shiftKey) redo()
    else undo()
    return
  }
  if ((event.ctrlKey || event.metaKey) && (event.key === 'y' || event.key === 'Y')) {
    if (isTypingTarget()) return
    event.preventDefault()
    redo()
    return
  }
  // Copy/paste shortcuts work with a node selected (canvas focus only,
  // never while typing in a form field).
  if ((event.ctrlKey || event.metaKey) && (event.key === 'c' || event.key === 'C')) {
    if (isTypingTarget()) return
    if (selectedNode.value) {
      event.preventDefault()
      copySelectedNode()
    }
    return
  }
  if ((event.ctrlKey || event.metaKey) && (event.key === 'v' || event.key === 'V')) {
    if (isTypingTarget() || !clipboard.value) return
    event.preventDefault()
    pasteClipboardNode()
    return
  }
  // Space: temporary hand (Dify/Figma-style) — hold to pan, release to restore.
  if (event.code === 'Space') {
    if (isTypingTarget()) return
    event.preventDefault()
    spaceHeld.value = true
    return
  }
  // Mode keys: V pointer, H hand, C comment (single press, no modifiers).
  if (!event.ctrlKey && !event.metaKey && !event.altKey && !isTypingTarget()) {
    const key = event.key.toLowerCase()
    if (key === 'v') { setMode('pointer'); return }
    if (key === 'h') { setMode('hand'); return }
    if (key === 'c') { setMode('comment'); return }
  }
  if (event.key !== 'Delete' && event.key !== 'Backspace') return
  if (isTypingTarget()) return
  if (selectedEdgeId.value) {
    event.preventDefault()
    removeEdge(selectedEdgeId.value)
    return
  }
  if (selectedNodeId.value) {
    event.preventDefault()
    removeNode(selectedNodeId.value)
    return
  }
  // Box multi-select delete: remove every selected node (Start stays).
  const selected = canvasNodes.value.filter((node) => (node as { selected?: boolean }).selected)
  const removable = selected.filter((node) => (node.data?.kind as WorkflowNodeType) !== 'Start')
  if (removable.length > 0) {
    event.preventDefault()
    for (const node of removable) removeNode(node.id)
  }
}

if (typeof window !== 'undefined') {
  window.addEventListener('keydown', onKeyDown)
  window.addEventListener('keyup', onKeyUp)
  onUnmounted(() => {
    window.removeEventListener('keydown', onKeyDown)
    window.removeEventListener('keyup', onKeyUp)
  })
}

function onKeyUp(event: KeyboardEvent) {
  if (event.code === 'Space') spaceHeld.value = false
}

// Run panel state: live node phases (SSE) keyed by canvas node id; cleared
// when the drawer closes so stale highlights never survive a panel session.
const runDrawerVisible = ref(false)
const runNodePhases = ref<Record<string, 'running' | 'done' | 'failed'>>({})
// Per-node debug payload (live frames or selected history run's trace),
// rendered as the inspect badge on canvas cards. Shares the phase lifecycle.
const runNodeOutputs = ref<Record<string, Record<string, unknown>>>({})

// ---- undo / redo ---------------------------------------------------------
// Snapshot history of the canvas structure (nodes+edges JSON). Structural
// changes push a snapshot (debounced); restore swaps the canvas back.
// ponytail: JSON snapshots, not command objects — O(canvas) per undo but a
// workflow canvas is tens of nodes; revisit if canvases grow past hundreds.
const undoStack = ref<string[]>([])
const undoIndex = ref(-1)
let restoring = false
const HISTORY_LIMIT = 50

const canUndo = computed(() => undoIndex.value > 0)
const canRedo = computed(() => undoIndex.value < undoStack.value.length - 1)

function canvasSnapshot(): string {
  return JSON.stringify({
    nodes: canvasNodes.value.map((node) => ({
      id: node.id,
      type: node.type,
      position: { x: node.position.x, y: node.position.y },
      data: JSON.parse(JSON.stringify(node.data ?? {})),
    })),
    edges: plainEdges(),
  })
}

function pushHistoryDebounced(): void {
  if (restoring) return
  if (pushHistoryTimer !== null) window.clearTimeout(pushHistoryTimer)
  pushHistoryTimer = window.setTimeout(() => {
    pushHistoryTimer = null
    const snap = canvasSnapshot()
    if (undoStack.value[undoIndex.value] === snap) return
    const next = undoStack.value.slice(0, undoIndex.value + 1)
    next.push(snap)
    if (next.length > HISTORY_LIMIT) next.shift()
    undoStack.value = next
    undoIndex.value = next.length - 1
  }, 400)
}
let pushHistoryTimer: number | null = null

// withRestoreGuard suppresses history pushes while the canvas is being
// replaced wholesale (undo restore / graph load); the flag re-arms on the
// next macrotask so positional watchers settling asynchronously stay
// suppressed too.
function withRestoreGuard(restore: () => void): void {
  restoring = true
  try {
    restore()
  } finally {
    window.setTimeout(() => {
      restoring = false
    }, 0)
  }
}

function restoreSnapshot(snap: string): void {
  withRestoreGuard(() => {
    const parsed = JSON.parse(snap) as {
      nodes: Array<{ id: string; type: string; position: { x: number; y: number }; data?: Record<string, unknown> }>
      edges: Array<{ id: string; source: string; target: string; sourceHandle?: string }>
    }
    canvasNodes.value = parsed.nodes.map((node) => ({
      id: node.id,
      type: node.type,
      position: { x: node.position.x, y: node.position.y },
      data: node.data,
    }))
    canvasEdges.value = parsed.edges.map((edge) => ({
      id: edge.id,
      source: edge.source,
      target: edge.target,
      ...(edge.sourceHandle ? { sourceHandle: edge.sourceHandle } : {}),
    }))
    refreshEdgeLabels()
  })
}

function undo() {
  if (!canUndo.value) return
  undoIndex.value -= 1
  restoreSnapshot(undoStack.value[undoIndex.value]!)
}

function redo() {
  if (!canRedo.value) return
  undoIndex.value += 1
  restoreSnapshot(undoStack.value[undoIndex.value]!)
}

watch([canvasNodes, canvasEdges], pushHistoryDebounced, { deep: true })

function onRunDrawerClosed() {
  runNodePhases.value = {}
  runNodeOutputs.value = {}
}

// ---- dirty guard ---------------------------------------------------------
// Snapshot the persisted DSL; any structural drift from it is unsaved work.
// Leaving the route (or reloading) then requires explicit confirmation.
// (Placed after `ready`, which refreshDirty reads.)
const ready = computed(() => !loading.value && !loadError.value)
let savedSignature = ''
const dirty = ref(false)

function currentSignature(): string {
  return JSON.stringify({ name: name.value.trim(), dsl: currentDsl() })
}

function refreshDirty() {
  dirty.value = ready.value && currentSignature() !== savedSignature
}

watch([canvasNodes, canvasEdges, name, ready], refreshDirty, { deep: true })

onBeforeRouteLeave(() => {
  if (!dirty.value) return true
  return new Promise<boolean>((resolve) => {
    const dialog = DialogPlugin.confirm({
      header: t('workflow.editor.unsavedTitle'),
      body: t('workflow.editor.unsavedBody'),
      confirmBtn: { content: t('workflow.editor.unsavedLeave'), theme: 'danger' },
      cancelBtn: t('workflow.editor.unsavedStay'),
      onConfirm: () => { dialog.destroy(); resolve(true) },
      onClose: () => { dialog.destroy(); resolve(false) },
    })
  })
})

// Reload/close with unsaved work: the browser's own guard (no custom UI).
function onBeforeUnload(event: BeforeUnloadEvent) {
  if (!dirty.value) return
  event.preventDefault()
  event.returnValue = ''
}
if (typeof window !== 'undefined') {
  window.addEventListener('beforeunload', onBeforeUnload)
  onUnmounted(() => window.removeEventListener('beforeunload', onBeforeUnload))
}

const saveLabel = computed(() =>
  dirty.value ? `${t('workflow.editor.save')} *` : t('workflow.editor.save'),
)

const selectedNode = computed(() => canvasNodes.value.find((node) => node.id === selectedNodeId.value) ?? null)
const selectedKind = computed<WorkflowNodeType>(() => (selectedNode.value?.data?.kind as WorkflowNodeType) ?? 'Answer')
const drawerVisible = computed({
  get: () => selectedNode.value !== null,
  set: (value: boolean) => {
    if (!value) selectedNodeId.value = null
  },
})

// Node params live on node.data.params; edit in place so the canvas and the
// DSL stay in sync without a separate copy step.
const selectedParams = computed<Record<string, unknown> | null>(() => {
  const data = selectedNode.value?.data as { params?: Record<string, unknown> } | undefined
  return data?.params ?? null
})

// Iteration body membership lives on node.data.parent (DSL contract), not in
// params — the form edits it through the set-parent event.
const selectedParent = computed(() => {
  const parent = (selectedNode.value?.data as Record<string, unknown> | undefined)?.parent
  return typeof parent === 'string' ? parent : ''
})

function setSelectedParent(parentId: string) {
  const node = selectedNode.value
  if (!node) return
  const data = (node.data ?? {}) as Record<string, unknown>
  if (parentId) data.parent = parentId
  else delete data.parent
  node.data = data
}

function nodeSubtitle(data: unknown): string {
  const holder = data as { kind?: WorkflowNodeType; params?: Record<string, unknown> } | undefined
  if (!holder?.kind) return ''
  return paramSummary(holder.kind, holder.params)
}

// Keep Switch case labels visible on the edges they route to.
watch(selectedParams, () => refreshEdgeLabels(), { deep: true })

function refreshEdgeLabels() {
  for (const edge of canvasEdges.value) {
    const source = canvasNodes.value.find((node) => node.id === edge.source)
    if (!source || (source.data?.kind !== 'Switch' && source.data?.kind !== 'QuestionClassifier')) continue
    const params = (source.data as { params?: Record<string, unknown> }).params ?? {}
    const cases = (Array.isArray(params.cases) ? (params.cases as Array<Record<string, unknown>>) : Array.isArray(params.classes) ? (params.classes as Array<Record<string, unknown>>) : []) as Array<{ value?: string; name?: string; to: string }>
    // Branch identity lives on the edge (sourceHandle) — re-deriving it
    // from cases[].to is ambiguous once two branches converge on one node
    // (both edges would jump to the first matching handle). Only legacy
    // edges saved without a handle derive it from the target, then keep it.
    if (!edge.sourceHandle) {
      const matched = cases.find((item) => item.to === edge.target)
      const isDefault = typeof params.default === 'string' && params.default === edge.target
      edge.sourceHandle = matched ? `case-${cases.indexOf(matched)}` : isDefault ? 'default' : edge.sourceHandle
    }
    const handle = edge.sourceHandle
    if (handle === 'default') {
      edge.label = t('workflow.editor.defaultBranch')
    } else if (typeof handle === 'string' && handle.startsWith('case-')) {
      const entry = cases[Number(handle.slice(5))]
      const label = entry ? String(entry.value ?? entry.name ?? '') : ''
      if (label) edge.label = label
      else delete edge.label
    } else {
      delete edge.label
    }
  }
}

// New and re-created edges get an arrowhead; selected/hover styling lives
// in the non-scoped block below (vue-flow internals carry no scope attr).
const defaultEdgeOptions = {
  markerEnd: MarkerType.ArrowClosed,
  selectable: true,
}

// Animate outgoing edges of the node currently executing (SSE run phase).
watch(runNodePhases, (phases) => {
  for (const edge of canvasEdges.value) {
    edge.animated = phases[edge.source] === 'running'
  }
}, { deep: true })

function onConnect(connection: Connection) {
  if (!connection.source || !connection.target) return
  const sourceHandle = connection.sourceHandle ?? undefined
  if (connection.source === connection.target) {
    MessagePlugin.warning(t('workflow.editor.selfLoopBlocked'))
    return
  }
  const source = canvasNodes.value.find((node) => node.id === connection.source)
  const isRoutingBranch =
    !!source &&
    (source.data?.kind === 'Switch' || source.data?.kind === 'QuestionClassifier') &&
    !!sourceHandle
  if (isRoutingBranch) {
    // A routing branch has exactly one target in the DSL (cases[].to /
    // default). Reconnecting the same branch outlet MOVES it: drop the old
    // edge instead of leaving a dead one behind.
    canvasEdges.value = canvasEdges.value.filter(
      (edge) => !(edge.source === connection.source && (edge.sourceHandle ?? '') === (sourceHandle ?? '')),
    )
  }
  // Duplicate only when the SAME branch handle already reaches the target:
  // converging branches (two cases → one node) are legitimate.
  const exists = canvasEdges.value.some(
    (edge) =>
      edge.source === connection.source &&
      edge.target === connection.target &&
      (edge.sourceHandle ?? '') === (sourceHandle ?? ''),
  )
  if (exists) return
  let id = sourceHandle
    ? `e-${connection.source}-${sourceHandle}-${connection.target}`
    : `e-${connection.source}-${connection.target}`
  while (canvasEdges.value.some((edge) => edge.id === id)) id = `${id}-x`
  canvasEdges.value.push({
    id,
    source: connection.source,
    target: connection.target,
    ...(sourceHandle ? { sourceHandle } : {}),
  })
  bindBranchTarget(connection.source, sourceHandle, connection.target)
  refreshEdgeLabels()
}

// ---- copy / paste --------------------------------------------------------
// One-node clipboard (mirrors the single-selection model). Copy stores the
// node's kind + a deep clone of params; paste drops a fresh id nearby.
interface NodeClipboard {
  kind: WorkflowNodeType
  params: Record<string, unknown>
}
const clipboard = ref<NodeClipboard | null>(null)

function copySelectedNode() {
  const node = selectedNode.value
  if (!node) return
  clipboard.value = {
    kind: (node.data?.kind as WorkflowNodeType) ?? 'Answer',
    params: JSON.parse(JSON.stringify((node.data?.params as Record<string, unknown>) ?? {})),
  }
  MessagePlugin.success(t('workflow.editor.nodeCopied'))
}

function pasteClipboardNode() {
  if (!clipboard.value) return
  addNodeFromPalette(clipboard.value.kind, clipboard.value.params)
}

// Quick-add from a node's + button: drop the new node to the right of the
// source (stepping down when the lane is occupied) and connect immediately.
function isOccupied(x: number, y: number): boolean {
  return canvasNodes.value.some(
    (item) => Math.abs(item.position.x - x) < 200 && Math.abs(item.position.y - y) < 90,
  )
}

function onQuickAdd(sourceId: string, kind: WorkflowNodeType, sourceHandle?: string) {
  const source = canvasNodes.value.find((item) => item.id === sourceId)
  if (!source) return
  // Multi-branch sources stack their children vertically per branch lane.
  let x = source.position.x + 260
  let y = source.position.y
  if (sourceHandle?.startsWith('case-')) {
    y = source.position.y + (Number(sourceHandle.slice(5)) - 0.5) * 110
  }
  let guard = 0
  while (isOccupied(x, y) && guard++ < 12) y += 110
  addNodeAt(kind, { x, y })
  const target = canvasNodes.value[canvasNodes.value.length - 1].id
  onConnect({ source: sourceId, target, sourceHandle } as Connection)
  bindBranchTarget(sourceId, sourceHandle, target)
  refreshEdgeLabels()
}

/** Branch handles for routing nodes: one per case plus the default branch.
 *  Plain nodes return undefined → single unnamed source handle. */
function branchHandlesOf(node: { id: string; data?: Record<string, unknown> }): Array<{ id: string; label: string }> | undefined {
  const kind = node.data?.kind
  if (kind !== 'Switch' && kind !== 'QuestionClassifier') return undefined
  const params = (node.data?.params as Record<string, unknown> | undefined) ?? {}
  const entries = Array.isArray(params.cases)
    ? (params.cases as Array<Record<string, unknown>>)
    : Array.isArray(params.classes)
      ? (params.classes as Array<Record<string, unknown>>)
      : []
  const branches = entries.map((entry, index) => ({
    id: `case-${index}`,
    label: String(entry.value ?? entry.name ?? index),
  }))
  if (typeof params.default === 'string' && params.default) {
    branches.push({ id: 'default', label: t('workflow.editor.defaultBranch') })
  }
  return branches.length > 0 ? branches : undefined
}

/** Which source handles of a node already carry an edge ('' = the single
 *  unnamed handle of plain nodes). */
function connectedHandlesOf(nodeId: string): string[] {
  return canvasEdges.value.filter((edge) => edge.source === nodeId).map((edge) => edge.sourceHandle ?? '')
}

/** Routing-node semantics: connecting from a case handle binds that case's
 *  `to` (and `default` for the default handle) so the canvas and the DSL
 *  params never disagree. */
function bindBranchTarget(sourceId: string, sourceHandle: string | undefined, targetId: string) {
  if (!sourceHandle) return
  const source = canvasNodes.value.find((node) => node.id === sourceId)
  if (!source || (source.data?.kind !== 'Switch' && source.data?.kind !== 'QuestionClassifier')) return
  const params = { ...((source.data?.params as Record<string, unknown>) ?? {}) }
  if (sourceHandle === 'default') {
    params.default = targetId
  } else if (sourceHandle.startsWith('case-')) {
    const index = Number(sourceHandle.slice(5))
    const listKey = Array.isArray(params.cases) ? 'cases' : 'classes'
    const list = Array.isArray(params[listKey]) ? [...(params[listKey] as Array<Record<string, unknown>>)] : []
    if (index >= 0 && index < list.length) {
      list[index] = { ...list[index], to: targetId }
      params[listKey] = list
    }
  }
  source.data = { ...source.data, params }
}

/** Edge-midpoint insert (Dify-style): new node lands on the midpoint and
 *  the old edge becomes source→new + new→target. */
function onEdgeInsert(
  edgeId: string,
  kind: WorkflowNodeType,
  geometry: { sourceX: number; sourceY: number; targetX: number; targetY: number },
) {
  const edge = canvasEdges.value.find((item) => item.id === edgeId)
  if (!edge) return
  const sourceHandle = edge.sourceHandle
  const sourceId = edge.source
  const targetId = edge.target
  const midX = Number(geometry.sourceX ?? 0)
  const midY = Number(geometry.sourceY ?? 0)
  const dx = Number(geometry.targetX ?? midX) - midX
  const dy = Number(geometry.targetY ?? midY) - midY
  const position = {
    x: midX + dx / 2 - 104,
    y: midY + dy / 2 - 30,
  }
  removeEdge(edge.id)
  addNodeAt(kind, position)
  const inserted = canvasNodes.value[canvasNodes.value.length - 1]
  onConnect({ source: sourceId, target: inserted.id, sourceHandle } as Connection)
  onConnect({ source: inserted.id, target: targetId } as Connection)
  refreshEdgeLabels()
}

function addNodeAt(kind: WorkflowNodeType, position: { x: number; y: number }) {
  if (!WORKFLOW_NODE_TYPES.includes(kind)) return
  const node: Node = {
    id: makeNodeId(kind),
    type: 'wf',
    position,
    data: { kind, params: defaultParams(kind) },
  }
  canvasNodes.value.push(node)
  selectedNodeId.value = node.id
  selectedEdgeId.value = null
}

/** Current viewport centre in flow coordinates (null when not ready). */
function viewportCenter(): { x: number; y: number } | null {
  const flow = flowInstance.value
  const el = flowEl.value
  if (!flow?.screenToFlowCoordinate || !el) return null
  const rect = el.getBoundingClientRect()
  return flow.screenToFlowCoordinate({ x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 })
}

function addNodeFromPalette(kind: WorkflowNodeType, presetParams?: Record<string, unknown>, position?: { x: number; y: number }) {
  if (!WORKFLOW_NODE_TYPES.includes(kind)) return
  // The graph allows a single Start node — Dify disables the palette entry
  // once one exists; the guard also covers paste and drag-and-drop paths.
  if (kind === 'Start' && canvasNodes.value.some((node) => node.data?.kind === 'Start')) {
    MessagePlugin.warning(t('workflow.editor.startExists'))
    return
  }
  // Land in view: click-add targets the current viewport centre (fixed
  // far-away coordinates looked like “nothing happened” after panning/zooming);
  // a drag-drop centres the card on the release point.
  const centre = position ?? viewportCenter()
  const jitter = () => Math.random() * 48 - 24
  let x = centre ? centre.x - 104 : 140
  let y = centre ? centre.y - 30 : 100
  if (!position) {
    x += jitter()
    y += jitter()
  }
  let guard = 0
  while (isOccupied(x, y) && guard++ < 12) y += 110
  const node: Node = {
    id: makeNodeId(kind),
    type: 'wf',
    position: { x, y },
    data: { kind, params: presetParams ? JSON.parse(JSON.stringify(presetParams)) : defaultParams(kind) },
  }
  canvasNodes.value.push(node)
  selectedNodeId.value = node.id
  selectedEdgeId.value = null
}

// Dify-style add-node: drag a palette entry onto the canvas; it lands
// under the release point.
function onDropNode(event: DragEvent) {
  const kind = event.dataTransfer?.getData('application/x-wf-node') as WorkflowNodeType | ''
  if (!kind) return
  const flow = flowInstance.value
  const position = flow?.screenToFlowCoordinate({ x: event.clientX, y: event.clientY })
  addNodeFromPalette(kind, undefined, position)
}

// ---- auto layout ----------------------------------------------------------
function applyAutoLayout() {
  const positions = autoLayout(currentGraphNodes(), plainEdges())
  for (const node of canvasNodes.value) {
    const position = positions[node.id]
    if (position) node.position = { ...position }
  }
}

function plainEdges() {
  // sourceHandle rides along: it is the branch identity for routing nodes
  // and is persisted in the DSL graph view (Go GraphEdge.SourceHandle).
  return canvasEdges.value.map((edge) => ({
    id: edge.id,
    source: edge.source,
    target: edge.target,
    ...(edge.sourceHandle ? { sourceHandle: edge.sourceHandle } : {}),
  }))
}

function currentGraphNodes() {
  return canvasNodes.value.map((node) => ({
    id: node.id,
    type: (node.data?.kind as WorkflowNodeType) ?? 'Answer',
    position: { x: node.position.x, y: node.position.y },
    data: { params: (node.data?.params as Record<string, unknown>) ?? {} },
  }))
}

// ---- pre-save validation --------------------------------------------------
function formatIssues(issues: GraphIssue[]): string {
  return issues.map((issue) => t(`workflow.editor.issues.${issue.key}`, issue.values ?? {})).join('\n')
}

function validateBeforeSave(): boolean {
  const issues = validateGraph(currentGraphNodes(), plainEdges())
  const errors = issues.filter((issue) => issue.level === 'error')
  const warnings = issues.filter((issue) => issue.level === 'warning')
  if (errors.length > 0) {
    MessagePlugin.error(`${t('workflow.editor.issues.title')}\n${formatIssues(errors)}`)
    return false
  }
  if (warnings.length > 0) {
    MessagePlugin.warning(`${t('workflow.editor.issues.title')}\n${formatIssues(warnings)}`)
    // Warnings do not block the save: the engine ignores unreachable
    // nodes and stale refs fail at run time with a clear node-scoped error.
  }
  return true
}

function currentDsl(): WorkflowDSL {
  return buildDsl(currentGraphNodes(), plainEdges(), { ...wfVariables.value })
}

function setCanvas(dsl: WorkflowDSL) {
  canvasNodes.value = dsl.graph.nodes.map((node) => ({
    id: node.id,
    type: 'wf',
    position: { x: node.position.x, y: node.position.y },
    data: { kind: node.type, params: migrateNodeParams(node.type, (node.data?.params as Record<string, unknown> | undefined) ?? defaultParams(node.type)) },
  }))
  canvasEdges.value = dsl.graph.edges.map((edge) => ({
    id: edge.id,
    source: edge.source,
    target: edge.target,
    ...(edge.sourceHandle ? { sourceHandle: edge.sourceHandle } : {}),
  }))
  wfVariables.value = { ...(dsl.variables ?? {}) }
  refreshEdgeLabels()
  // Reset the undo history for the freshly loaded graph.
  withRestoreGuard(() => {
    undoStack.value = [canvasSnapshot()]
    undoIndex.value = 0
  })
}

async function load() {
  loading.value = true
  loadError.value = false
  loadErrorDetail.value = ''
  try {
    const response = await getWorkflow(workflowId.value)
    const data = response?.data
    if (!response?.success || !data) {
      loadError.value = true
      loadErrorDetail.value = response?.message || t('workflow.editor.notFound')
      return
    }
    name.value = data.name
    workflow.value = data
    setCanvas(normalizeDsl(data.dsl))
    savedSignature = JSON.stringify({ name: data.name, dsl: currentDsl() })
    dirty.value = false
    void fetchPickerData()
  } catch (error) {
    loadError.value = true
    loadErrorDetail.value = error instanceof Error ? error.message : String(error)
  } finally {
    loading.value = false
  }
}

async function doSave(options: { silent?: boolean } = {}): Promise<boolean> {
  const silent = options.silent === true
  const trimmed = name.value.trim()
  if (!trimmed) {
    if (!silent) MessagePlugin.warning(t('workflow.nameRequired'))
    return false
  }
  // Silent (auto-)save skips validation toasts: drafts persist mid-edit, and
  // publish/run do their own gating. Manual save keeps the loud checks.
  if (!silent && !validateBeforeSave()) return false
  saving.value = true
  try {
    const response = await updateWorkflow(workflowId.value, { name: trimmed, dsl: currentDsl() })
    if (response?.success) {
      savedSignature = currentSignature()
      dirty.value = false
      if (silent) lastSavedAt.value = new Date()
      else MessagePlugin.success(t('workflow.saved'))
      return true
    }
    if (!silent) MessagePlugin.error(response?.message || t('workflow.editor.saveFailed'))
    else console.warn('[workflow] auto-save rejected:', response?.message)
    return false
  } catch (error) {
    if (!silent) MessagePlugin.error(error instanceof Error ? error.message : t('workflow.editor.saveFailed'))
    else console.warn('[workflow] auto-save failed:', error)
    return false
  } finally {
    saving.value = false
  }
}

// ---- auto-save ------------------------------------------------------------
// 3s after the last change that made the canvas dirty, persist silently.
// Failures stay quiet and retry on the next dirty tick; the leave guards
// naturally stop firing once auto-saved.
const AUTO_SAVE_DELAY = 3000
let autoSaveTimer: number | null = null
const lastSavedAt = ref<Date | null>(null)
const autoSavedTime = computed(() =>
  lastSavedAt.value ? lastSavedAt.value.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '',
)

function scheduleAutoSave() {
  if (autoSaveTimer !== null) window.clearTimeout(autoSaveTimer)
  if (!dirty.value || !ready.value) return
  autoSaveTimer = window.setTimeout(() => {
    autoSaveTimer = null
    if (!dirty.value || !ready.value || saving.value) return
    void doSave({ silent: true }).then((ok) => {
      if (!ok) scheduleAutoSave()
    })
  }, AUTO_SAVE_DELAY)
}

watch(dirty, scheduleAutoSave)
onUnmounted(() => {
  if (autoSaveTimer !== null) window.clearTimeout(autoSaveTimer)
})

// Publish from the editor: unsaved changes are saved first (publish always
// freezes what is on the canvas), then the snapshot endpoint runs.
const publishing = ref(false)

async function publishFromEditor() {
  if (publishing.value) return
  publishing.value = true
  try {
    if (dirty.value && !(await doSave())) return
    const response = await publishWorkflow(workflowId.value)
    if (response?.success && response.data) {
      workflow.value = response.data
      MessagePlugin.success(t('workflow.published'))
    } else {
      MessagePlugin.error(response?.message || t('workflow.publishFailed'))
    }
  } catch (error) {
    MessagePlugin.error(error instanceof Error ? error.message : t('workflow.publishFailed'))
  } finally {
    publishing.value = false
  }
}

function exportDsl() {
  const blob = new Blob([JSON.stringify(currentDsl(), null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = `workflow-${workflowId.value}.json`
  anchor.click()
  URL.revokeObjectURL(url)
}

async function onImportFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  try {
    const text = await file.text()
    const parsed: unknown = JSON.parse(text)
    const dsl = normalizeDsl(parsed)
    if (dsl.graph.nodes.length === 0) {
      MessagePlugin.warning(t('workflow.editor.importFailed'))
      return
    }
    setCanvas(dsl)
    MessagePlugin.success(t('workflow.editor.imported'))
  } catch {
    MessagePlugin.warning(t('workflow.editor.importFailed'))
  }
}

function goBack() {
  router.push('/platform/workflow')
}

load()
</script>

<style>
/* Global (non-scoped): vue-flow's edge/pane DOM never receives this
   component's scope attribute, so selection styling must live here. */
.wf-editor .vue-flow__edge {
  cursor: pointer;
}

.wf-editor .vue-flow__edge:hover path.wf-edge-path,
.wf-editor .vue-flow__edge:hover path.vue-flow__edge-path {
  stroke: var(--td-brand-color);
}

.wf-editor .vue-flow__edge.selected path.wf-edge-path,
.wf-editor .vue-flow__edge.selected path.vue-flow__edge-path {
  stroke: var(--td-brand-color);
  stroke-width: 2.5px;
}

.wf-editor .vue-flow__edge-textbg {
  fill: var(--td-bg-color-container);
}

.wf-editor .vue-flow__edge-text {
  fill: var(--td-text-color-primary);
  font-size: 11px;
}

/* Live connection line while dragging: brand-coloured like Dify's. */
.wf-editor .vue-flow__connection-path {
  stroke: var(--td-brand-color);
  stroke-width: 2;
}
</style>

<style scoped>
.wf-editor {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--td-bg-color-page);
}

.wf-editor-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px 12px;
  padding: 8px 16px;
  background: var(--td-bg-color-container);
  border-bottom: 1px solid var(--td-component-stroke);
}

.wf-editor-toolbar-left,
.wf-editor-toolbar-right {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 6px 8px;
}

.wf-editor-toolbar-left {
  min-width: 0;
}

.wf-editor-name {
  min-width: 120px;
  width: auto;
  max-width: 220px;
}

.wf-editor-name {
  width: 260px;
}

.wf-editor-autosaved {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
}

.wf-editor-file-input {
  display: none;
}

.wf-editor-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
}

.wf-editor-vars {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.wf-editor-vars-hint {
  margin: 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.wf-editor-vars-row {
  display: flex;
  align-items: center;
  gap: 4px;
}

.wf-editor-vars-row .t-input {
  min-width: 0;
  flex: 1;
}

.wf-editor-vars-name {
  flex: 0 0 110px !important;
}

.wf-editor-canvas {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: stretch;
}

.wf-editor-flow {
  flex: 1;
  min-width: 0;
  position: relative;
}

.wf-editor-flow--comment :deep(.vue-flow__pane) {
  cursor: crosshair;
}

.wf-canvas-toolbar {
  position: absolute;
  top: 12px;
  left: 12px;
  z-index: 20;
  display: flex;
  gap: 4px;
  padding: 4px;
  border-radius: 8px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  box-shadow: var(--td-shadow-1);
}

.wf-canvas-tool {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border: none;
  border-radius: 6px;
  background: none;
  cursor: pointer;
  color: var(--td-text-color-secondary);
  font-size: 15px;
}

.wf-canvas-tool:hover {
  background: var(--td-bg-color-container-hover);
}

.wf-canvas-tool--active {
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
}

/* Sticky note: draggable + editable, purely presentational (excluded from
   the DSL components; rides in the graph view). */
.wf-note {
  width: 220px;
  min-height: 120px;
  padding: 8px;
  border-radius: 8px;
  background: #fff7d6;
  border: 1px solid #e8d98a;
  box-shadow: var(--td-shadow-1);
}

.wf-note--selected {
  border-color: var(--td-brand-color);
  box-shadow: var(--td-shadow-3);
}

.wf-note-textarea {
  width: 100%;
  height: 104px;
  border: none;
  outline: none;
  background: transparent;
  resize: vertical;
  font-size: 12px;
  line-height: 1.6;
  color: #5b4a12;
}

.wf-editor-hint {
  position: absolute;
  left: 12px;
  bottom: 12px;
  margin: 0;
  padding: 4px 10px;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  pointer-events: none;
  z-index: 5;
}

.wf-editor-form-empty {
  padding: 32px 0;
  text-align: center;
  color: var(--td-text-color-placeholder);
}
</style>
