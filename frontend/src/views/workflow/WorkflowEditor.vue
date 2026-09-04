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
      <div v-if="selectedParams" class="wf-editor-form">
        <p class="wf-editor-form-kind">{{ $t(`workflow.nodes.${selectedKind}`) }} · {{ selectedNodeId }}</p>

        <template v-if="selectedKind === 'Start'">
          <t-form-item label="queryPlaceholder">
            <t-input v-model="selectedParams.queryPlaceholder" :placeholder="$t('workflow.editor.queryPlaceholder')" />
          </t-form-item>
        </template>

        <template v-else-if="selectedKind === 'LLM'">
          <t-form-item :label="$t('workflow.editor.prompt')">
            <t-textarea v-model="selectedParams.prompt" :autosize="{ minRows: 4, maxRows: 10 }" :placeholder="$t('workflow.editor.promptHint')" />
          </t-form-item>
          <t-form-item :label="$t('workflow.editor.model')">
            <t-input v-model="selectedParams.model" />
          </t-form-item>
          <t-form-item :label="$t('workflow.editor.temperature')">
            <t-input-number v-model="llmTemperature" :min="0" :max="2" :step="0.1" theme="column" />
          </t-form-item>
        </template>

        <template v-else-if="selectedKind === 'Retrieval'">
          <t-form-item :label="$t('workflow.editor.kbIds')">
            <t-textarea v-model="kbIdsText" :autosize="{ minRows: 3, maxRows: 8 }" />
          </t-form-item>
          <t-form-item :label="$t('workflow.editor.topK')">
            <t-input-number v-model="retrievalTopK" :min="1" :max="50" theme="column" />
          </t-form-item>
        </template>

        <template v-else-if="selectedKind === 'Switch'">
          <t-form-item :label="$t('workflow.editor.switchValue')">
            <t-input v-model="selectedParams.value" :placeholder="$t('workflow.editor.switchValueHint')" />
          </t-form-item>
          <t-form-item :label="$t('workflow.editor.cases')">
            <div class="wf-editor-cases">
              <div v-for="(item, index) in switchCases" :key="index" class="wf-editor-case-row">
                <t-input v-model="item.value" :placeholder="$t('workflow.editor.caseValue')" />
                <t-select v-model="item.to" :placeholder="$t('workflow.editor.caseTarget')" clearable>
                  <t-option v-for="option in nodeOptions(selectedNodeId)" :key="option.value" :value="option.value" :label="option.label" />
                </t-select>
                <t-button variant="text" theme="danger" size="small" @click="switchCases.splice(index, 1)">
                  <template #icon><t-icon name="delete" /></template>
                </t-button>
              </div>
              <t-button variant="dashed" size="small" block @click="switchCases.push({ value: '', to: '' })">
                {{ $t('workflow.editor.addCase') }}
              </t-button>
            </div>
          </t-form-item>
          <t-form-item :label="$t('workflow.editor.defaultBranch')">
            <t-select v-model="selectedParams.default" :placeholder="$t('workflow.editor.caseTarget')" clearable>
              <t-option v-for="option in nodeOptions(selectedNodeId)" :key="option.value" :value="option.value" :label="option.label" />
            </t-select>
          </t-form-item>
        </template>

        <template v-else-if="selectedKind === 'Answer'">
          <t-form-item :label="$t('workflow.editor.template')">
            <t-textarea v-model="selectedParams.template" :autosize="{ minRows: 4, maxRows: 10 }" :placeholder="$t('workflow.editor.promptHint')" />
          </t-form-item>
        </template>
      </div>
      <div v-else class="wf-editor-form-empty">
        {{ $t('workflow.editor.selectNode') }}
      </div>
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
import { WORKFLOW_NODE_TYPES, getWorkflow, updateWorkflow, type Workflow, type WorkflowDSL, type WorkflowNodeType } from '@/api/workflow'
import { buildDsl, defaultParams, makeNodeId, normalizeDsl } from './dsl'

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

const selectedNodeId = ref<string | null>(null)
const importDslFile = ref<HTMLInputElement | null>(null)

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
  const params = (data as { params?: Record<string, unknown> } | undefined)?.params
  if (!params) return ''
  if (typeof params.model === 'string' && params.model) return String(params.model)
  if (Array.isArray(params.cases)) return `${params.cases.length} cases`
  return ''
}

function nodeOptions(excludeId: string | null): Array<{ value: string; label: string }> {
  return canvasNodes.value
    .filter((node) => node.id !== excludeId)
    .map((node) => ({ value: node.id, label: `${(node.data?.kind as string) ?? ''} · ${node.id}` }))
}

const llmTemperature = computed<number>({
  get: () => Number(selectedParams.value?.temperature ?? 0.7),
  set: (value: number) => {
    if (selectedParams.value) selectedParams.value.temperature = value
  },
})

const retrievalTopK = computed<number>({
  get: () => Number(selectedParams.value?.topK ?? 10),
  set: (value: number) => {
    if (selectedParams.value) selectedParams.value.topK = value
  },
})

const kbIdsText = computed<string>({
  get: () => (Array.isArray(selectedParams.value?.kbIds) ? (selectedParams.value!.kbIds as string[]).join('\n') : ''),
  set: (value: string) => {
    if (selectedParams.value) {
      selectedParams.value.kbIds = value.split('\n').map((line) => line.trim()).filter(Boolean)
    }
  },
})

const switchCases = computed<Array<{ value: string; to: string }>>({
  get: () => {
    const cases = selectedParams.value?.cases
    return Array.isArray(cases) ? (cases as Array<{ value: string; to: string }>) : []
  },
  set: (value: Array<{ value: string; to: string }>) => {
    if (selectedParams.value) selectedParams.value.cases = value
  },
})

// Keep Switch case labels visible on the edges they route to.
watch([switchCases, selectedParams], () => refreshEdgeLabels(), { deep: true })

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
    data: { kind: node.type, params: node.data?.params ?? defaultParams(node.type) },
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
  router.push('/workflow')
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
