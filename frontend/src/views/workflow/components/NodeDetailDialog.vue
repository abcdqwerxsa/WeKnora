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

      <div class="wf-detail-cols">
        <!-- Left: INPUT — direct upstream outputs, editable JSON (n8n pinned-data style). -->
        <aside class="wf-detail-pane">
          <p class="wf-detail-pane-title">{{ t('workflow.editor.nodeInputs') }}</p>
          <div v-if="upstreams.length === 0" class="wf-detail-empty">
            {{ t('workflow.editor.runNodeNoUpstream') }}
          </div>
          <div v-for="up in upstreamList" :key="up.id" class="wf-detail-up">
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

        <!-- Middle: the node's property form (the old right-side drawer body). -->
        <section class="wf-detail-main">
          <slot />
        </section>

        <!-- Right: OUTPUT — this node's execution result. -->
        <aside class="wf-detail-pane">
          <p class="wf-detail-pane-title">{{ t('workflow.editor.nodeOutputs') }}</p>
          <pre v-if="outputShown !== null" class="wf-detail-json-out" :class="{ 'wf-detail-json-out--err': failed }">{{ outputShown }}</pre>
          <div v-else class="wf-detail-empty">{{ t('workflow.editor.runNodeNoOutput') }}</div>
          <p v-if="runError" class="wf-detail-err">{{ runError }}</p>
        </aside>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { runWorkflowNode, type WorkflowRunTraceEntry, type WorkflowNodeType } from '@/api/workflow'
import { NODE_COLORS } from '../nodeMeta'

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
  /** Show the test-step button (false for Start / Iteration, which the
   * backend rejects as not runnable in isolation). */
  runnable: boolean
  upstreams: DetailUpstream[]
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  /** Fired after a successful single-node run so the editor's outputs
   * cache (canvas cards, future debug prefill) stays in sync. */
  'node-output': [nodeId: string, outputs: Record<string, unknown>]
}>()

const { t } = useI18n()
const running = ref(false)
const runError = ref('')
const outputShown = ref<string | null>(null)
const failed = ref(false)

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
    for (const up of props.upstreams) {
      drafts.set(up.id, { draft: JSON.stringify(up.outputs ?? up.seed ?? {}, null, 2), parseError: '' })
    }
    runError.value = ''
    outputShown.value = null
    failed.value = false
  },
  { immediate: true },
)

function pretty(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

async function run() {
  // Parse every upstream draft first — abort on the first syntax error.
  const inputs: Record<string, Record<string, unknown>> = {}
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
  grid-template-columns: 290px minmax(0, 1fr) 290px;
  gap: 14px;
  height: min(66vh, 640px);
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
