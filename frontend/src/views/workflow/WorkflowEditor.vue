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
        <t-dropdown @click="(item: { value?: string }) => addNode(item.value as WorkflowNodeType)">
          <t-button variant="outline">
            {{ $t('workflow.editor.addNode') }}
            <template #icon><t-icon name="add" /></template>
          </t-button>
          <t-dropdown-menu>
            <t-dropdown-item v-for="kind in WORKFLOW_NODE_TYPES" :key="kind" :value="kind">
              {{ $t(`workflow.nodes.${kind}`) }}
            </t-dropdown-item>
          </t-dropdown-menu>
        </t-dropdown>
        <t-button variant="outline" @click="importDslFile?.click()">
          <template #icon><t-icon name="upload" /></template>
          {{ $t('workflow.editor.importDsl') }}
        </t-button>
        <t-button variant="outline" :disabled="!ready" @click="exportDsl">
          <template #icon><t-icon name="download" /></template>
          {{ $t('workflow.editor.exportDsl') }}
        </t-button>
        <t-button theme="primary" :loading="saving" :disabled="!ready" @click="save">
          {{ $t('workflow.editor.save') }}
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
      <VueFlow
        v-model:nodes="canvasNodes"
        v-model:edges="canvasEdges"
        fit-view-on-init
        :min-zoom="0.2"
        :max-zoom="2"
        @connect="onConnect"
        @node-click="onNodeClick"
        @pane-click="selectedNodeId = null"
      >
        <Background :gap="20" />
        <template #node-wf="nodeProps">
          <WfNodeCard
            :kind="(nodeProps.data?.kind as WorkflowNodeType) ?? 'Answer'"
            :selected="nodeProps.selected"
            :subtitle="nodeSubtitle(nodeProps.data)"
            :run-phase="runNodePhases[nodeProps.id]"
          />
        </template>
      </VueFlow>
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
      <WorkflowRunPanel :workflow-id="workflowId" @node-phases="runNodePhases = $event" />
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { VueFlow, type Connection, type Edge, type Node, type NodeMouseEvent } from '@vue-flow/core'
import { Background } from '@vue-flow/background'
// vue-flow base styles must be global (not scoped): its internal DOM
// (handles/edges/pane) never receives this component's scope attribute.
import '@vue-flow/core/dist/style.css'
import '@vue-flow/core/dist/theme-default.css'
import WfNodeCard from './components/WfNodeCard.vue'
import NodePropertyForm from './components/NodePropertyForm.vue'
import WorkflowRunPanel from './components/WorkflowRunPanel.vue'
import { WORKFLOW_NODE_TYPES, getWorkflow, updateWorkflow, type Workflow, type WorkflowDSL, type WorkflowNodeType } from '@/api/workflow'
import { buildDsl, defaultParams, makeNodeId, migrateNodeParams, normalizeDsl } from './dsl'
import { paramSummary } from './nodeMeta'
import { listModels, type ModelConfig } from '@/api/model'
import { listKnowledgeBases } from '@/api/knowledge-base'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const workflowId = computed(() => String(route.params.id ?? ''))

const loading = ref(true)
const loadError = ref(false)
const loadErrorDetail = ref('')
const name = ref('')
const saving = ref(false)

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
}

const selectedNodeId = ref<string | null>(null)
const importDslFile = ref<HTMLInputElement | null>(null)

// Run panel state: live node phases (SSE) keyed by canvas node id; cleared
// when the drawer closes so stale highlights never survive a panel session.
const runDrawerVisible = ref(false)
const runNodePhases = ref<Record<string, 'running' | 'done' | 'failed'>>({})

function onRunDrawerClosed() {
  runNodePhases.value = {}
}

const ready = computed(() => !loading.value && !loadError.value)

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

function onNodeClick(event: NodeMouseEvent) {
  selectedNodeId.value = event.node.id
}

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

function addNode(kind: WorkflowNodeType) {
  if (!WORKFLOW_NODE_TYPES.includes(kind)) return
  const node: Node = {
    id: makeNodeId(kind),
    type: 'wf',
    position: { x: 80 + (canvasNodes.value.length % 5) * 200, y: 80 + Math.floor(canvasNodes.value.length / 5) * 140 },
    data: { kind, params: defaultParams(kind) },
  }
  canvasNodes.value.push(node)
  selectedNodeId.value = node.id
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
  return buildDsl(plainNodes, plainEdges)
}

function setCanvas(dsl: WorkflowDSL) {
  canvasNodes.value = dsl.graph.nodes.map((node) => ({
    id: node.id,
    type: 'wf',
    position: { x: node.position.x, y: node.position.y },
    data: { kind: node.type, params: migrateNodeParams(node.type, (node.data?.params as Record<string, unknown> | undefined) ?? defaultParams(node.type)) },
  }))
  canvasEdges.value = dsl.graph.edges.map((edge) => ({ id: edge.id, source: edge.source, target: edge.target }))
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
  saving.value = true
  try {
    const response = await updateWorkflow(workflowId.value, { name: trimmed, dsl: currentDsl() })
    if (response?.success) {
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

.wf-editor-canvas {
  flex: 1;
  min-height: 0;
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
