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
          <template #icon><t-icon name="variable" /></template>
          {{ $t('workflow.editor.variablesBtn') }}
        </t-button>
        <t-button variant="outline" @click="applyAutoLayout" :disabled="!ready">
          <template #icon><t-icon name="layout" /></template>
          {{ $t('workflow.editor.autoLayout') }}
        </t-button>
        <t-button variant="outline" @click="copySelectedNode" :disabled="!ready || !selectedNode">
          <template #icon><t-icon name="copy" /></template>
          {{ $t('workflow.editor.copyNode') }}
        </t-button>
        <t-button variant="outline" :disabled="!ready || !clipboard" @click="pasteClipboardNode">
          <template #icon><t-icon name="clipboard" /></template>
          {{ $t('workflow.editor.pasteNode') }}
        </t-button>
        <t-button variant="outline" @click="importDslFile?.click()">
          <template #icon><t-icon name="upload" /></template>
          {{ $t('workflow.editor.importDsl') }}
        </t-button>
        <t-button variant="outline" :disabled="!ready" @click="exportDsl">
          <template #icon><t-icon name="download" /></template>
          {{ $t('workflow.editor.exportDsl') }}
        </t-button>
        <t-button theme="primary" :loading="saving" :disabled="!ready" @click="save">
          {{ saveLabel }}
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
      <NodePalette @add="addNodeFromPalette" />
      <div class="wf-editor-flow">
        <VueFlow
          v-model:nodes="canvasNodes"
          v-model:edges="canvasEdges"
          fit-view-on-init
          :min-zoom="0.2"
          :max-zoom="2"
          :default-edge-options="defaultEdgeOptions"
          @connect="onConnect"
          @node-click="onNodeClick"
          @edge-click="onEdgeClick"
          @edge-double-click="onEdgeDoubleClick"
          @pane-click="clearSelection"
        >
          <Background :gap="20" />
          <Controls position="bottom-left" />
          <MiniMap position="bottom-right" pannable zoomable />
          <template #node-wf="nodeProps">
            <WfNodeCard
              :kind="(nodeProps.data?.kind as WorkflowNodeType) ?? 'Answer'"
              :selected="nodeProps.selected"
              :subtitle="nodeSubtitle(nodeProps.data)"
              :run-phase="runNodePhases[nodeProps.id]"
              :outputs="runNodeOutputs[nodeProps.id]"
              :node-id="nodeProps.id"
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
      @closed="onRunDrawerClosed"
    >
      <WorkflowRunPanel
        :workflow-id="workflowId"
        :nodes="pickerNodes"
        :start-fields="startFields"
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
import { VueFlow, MarkerType, type Connection, type Edge, type EdgeMouseEvent, type Node, type NodeMouseEvent } from '@vue-flow/core'
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
import NodePalette from './components/NodePalette.vue'
import NodePropertyForm from './components/NodePropertyForm.vue'
import WorkflowRunPanel from './components/WorkflowRunPanel.vue'
import { WORKFLOW_NODE_TYPES, getWorkflow, updateWorkflow, type Workflow, type WorkflowDSL, type WorkflowNodeType } from '@/api/workflow'
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
    const response = await listKnowledgeBases()
    const items = ((response as unknown as { data?: { list?: unknown[] } })?.data?.list ?? (response as unknown as { list?: unknown[] })?.list ?? []) as Array<{ id: string; name: string }>
    kbs.value = Array.isArray(items) ? items.map((item) => ({ id: String(item.id), name: String(item.name ?? item.id) })) : []
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

// Typing targets: Delete/Backspace must not fire while the user edits a
// form field — only delete when the focus is on the canvas itself.
function isTypingTarget(): boolean {
  const el = document.activeElement
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (el as HTMLElement).isContentEditable
}

function onKeyDown(event: KeyboardEvent) {
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
  }
}

if (typeof window !== 'undefined') {
  window.addEventListener('keydown', onKeyDown)
  onUnmounted(() => window.removeEventListener('keydown', onKeyDown))
}

// Run panel state: live node phases (SSE) keyed by canvas node id; cleared
// when the drawer closes so stale highlights never survive a panel session.
const runDrawerVisible = ref(false)
const runNodePhases = ref<Record<string, 'running' | 'done' | 'failed'>>({})
// Per-node debug payload (live frames or selected history run's trace),
// rendered as the inspect badge on canvas cards. Shares the phase lifecycle.
const runNodeOutputs = ref<Record<string, Record<string, unknown>>>({})

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
    if (!source || source.data?.kind !== 'Switch') continue
    const params = (source.data as { params?: Record<string, unknown> }).params ?? {}
    const cases = Array.isArray(params.cases) ? (params.cases as Array<{ value: string; to: string }>) : []
    const matched = cases.find((item) => item.to === edge.target)
    const isDefault = typeof params.default === 'string' && params.default === edge.target
    const label = matched?.value ?? (isDefault ? t('workflow.editor.defaultBranch') : undefined)
    if (label) edge.label = label
    else delete edge.label
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
  if (connection.source === connection.target) {
    MessagePlugin.warning(t('workflow.editor.selfLoopBlocked'))
    return
  }
  if (canvasEdges.value.some((edge) => edge.source === connection.source && edge.target === connection.target)) {
    return
  }
  canvasEdges.value.push({
    id: `e-${connection.source}-${connection.target}`,
    source: connection.source,
    target: connection.target,
  })
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

function addNodeFromPalette(kind: WorkflowNodeType, presetParams?: Record<string, unknown>) {
  if (!WORKFLOW_NODE_TYPES.includes(kind)) return
  // Drop near the canvas centre with a little jitter so repeated adds
  // don't stack exactly on top of each other.
  const n = canvasNodes.value.length
  const jitter = () => Math.random() * 48 - 24
  const node: Node = {
    id: makeNodeId(kind),
    type: 'wf',
    position: { x: 140 + (n % 4) * 240 + jitter(), y: 100 + Math.floor(n / 4) * 170 + jitter() },
    data: { kind, params: presetParams ? JSON.parse(JSON.stringify(presetParams)) : defaultParams(kind) },
  }
  canvasNodes.value.push(node)
  selectedNodeId.value = node.id
  selectedEdgeId.value = null
}

// ---- auto layout ----------------------------------------------------------
function applyAutoLayout() {
  const positions = autoLayout(currentGraphNodes(), canvasEdges.value.map((e) => ({ id: e.id, source: e.source, target: e.target })))
  for (const node of canvasNodes.value) {
    const position = positions[node.id]
    if (position) node.position = { ...position }
  }
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
  const issues = validateGraph(currentGraphNodes(), canvasEdges.value.map((e) => ({ id: e.id, source: e.source, target: e.target })))
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
  const plainNodes = canvasNodes.value.map((node) => {
    const kind = (node.data?.kind as WorkflowNodeType) ?? 'Answer'
    return {
      id: node.id,
      type: kind,
      position: { x: node.position.x, y: node.position.y },
      data: { params: (node.data?.params as Record<string, unknown>) ?? defaultParams(kind) },
    }
  })
  const plainEdges = canvasEdges.value.map((edge) => ({ id: edge.id, source: edge.source, target: edge.target }))
  return buildDsl(plainNodes, plainEdges, { ...wfVariables.value })
}

function setCanvas(dsl: WorkflowDSL) {
  canvasNodes.value = dsl.graph.nodes.map((node) => ({
    id: node.id,
    type: 'wf',
    position: { x: node.position.x, y: node.position.y },
    data: { kind: node.type, params: migrateNodeParams(node.type, (node.data?.params as Record<string, unknown> | undefined) ?? defaultParams(node.type)) },
  }))
  canvasEdges.value = dsl.graph.edges.map((edge) => ({ id: edge.id, source: edge.source, target: edge.target }))
  wfVariables.value = { ...(dsl.variables ?? {}) }
  refreshEdgeLabels()
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

async function save() {
  const trimmed = name.value.trim()
  if (!trimmed) {
    MessagePlugin.warning(t('workflow.nameRequired'))
    return
  }
  if (!validateBeforeSave()) return
  saving.value = true
  try {
    const response = await updateWorkflow(workflowId.value, { name: trimmed, dsl: currentDsl() })
    if (response?.success) {
      savedSignature = currentSignature()
      dirty.value = false
      MessagePlugin.success(t('workflow.saved'))
    } else {
      MessagePlugin.error(response?.message || t('workflow.editor.saveFailed'))
    }
  } catch (error) {
    MessagePlugin.error(error instanceof Error ? error.message : t('workflow.editor.saveFailed'))
  } finally {
    saving.value = false
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

.wf-editor .vue-flow__edge:hover path.vue-flow__edge-path {
  stroke: var(--td-brand-color);
}

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
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 16px;
  background: var(--td-bg-color-container);
  border-bottom: 1px solid var(--td-component-stroke);
}

.wf-editor-toolbar-left,
.wf-editor-toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.wf-editor-name {
  width: 260px;
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

.wf-editor-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.wf-editor-form-kind {
  margin: 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  word-break: break-all;
}

.wf-editor-form-empty {
  padding: 32px 0;
  text-align: center;
  color: var(--td-text-color-placeholder);
}

.wf-editor-cases {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.wf-editor-case-row {
  display: flex;
  align-items: center;
  gap: 4px;
}
</style>
