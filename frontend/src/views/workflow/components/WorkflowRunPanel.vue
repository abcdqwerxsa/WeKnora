<template>
  <div class="wf-run-panel">
    <!-- Published workflows run the frozen snapshot, not the draft -->
    <t-alert
      v-if="publishedStale"
      theme="warning"
      :message="$t('workflow.run.stalePublished')"
    />

    <!-- Start-node input form (declared fields render as run inputs) -->
    <section v-if="(startFields ?? []).length > 0" class="wf-run-section">
      <p class="wf-run-section-title">{{ $t('workflow.run.formTitle') }}</p>
      <t-form label-align="top">
        <t-form-item v-for="field in startFields" :key="field.name" :label="field.label || field.name" :mark="field.required">
          <t-select
            v-if="field.type === 'select'"
            v-model="formValues[field.name]"
            clearable
            :placeholder="field.name"
          >
            <t-option v-for="option in field.options ?? []" :key="option" :value="option" :label="option" />
          </t-select>
          <t-textarea
            v-else-if="field.type === 'paragraph'"
            v-model="formValues[field.name]"
            :autosize="{ minRows: 2, maxRows: 6 }"
            :placeholder="field.name"
          />
          <t-input-number
            v-else-if="field.type === 'number'"
            v-model="formValues[field.name]"
            theme="column"
            :placeholder="field.name"
          />
          <t-input v-else v-model="formValues[field.name]" :placeholder="field.name" />
        </t-form-item>
      </t-form>
    </section>

    <!-- Run attachments: parsed into LLM context at execution -->
    <section class="wf-run-section">
      <p class="wf-run-section-title">{{ $t('workflow.run.attachments') }}</p>
      <div class="wf-run-attach">
        <input ref="fileInput" type="file" multiple hidden @change="onFilesPicked" />
        <t-button variant="outline" size="small" :loading="uploading" @click="fileInput?.click()">
          <template #icon><t-icon name="upload" /></template>
          {{ $t('workflow.run.attachFiles') }}
        </t-button>
        <span v-for="a in attachments" :key="a.id" class="wf-run-attach-chip" :class="`wf-run-attach-chip--${a.status}`">
          <t-icon :name="attachIcon(a.status)" />
          <span class="wf-run-attach-name" :title="a.error_message || a.file_name">{{ a.file_name }}</span>
          <span v-if="a.status === 'ready'" class="wf-run-attach-meta">{{ a.chunk_count }}c</span>
          <t-icon name="close" class="wf-run-attach-remove" @click="removeAttachment(a.id)" />
        </span>
      </div>
      <div v-if="pendingCount > 0" class="wf-run-muted">{{ $t('workflow.run.attachmentProcessing', { n: pendingCount }) }}</div>
    </section>

    <!-- Trigger -->
    <section class="wf-run-section">
      <t-textarea
        v-model="query"
        :autosize="{ minRows: 2, maxRows: 6 }"
        :placeholder="$t('workflow.run.queryPlaceholder')"
        :disabled="starting"
      />
      <div class="wf-run-actions">
        <t-button
          theme="primary"
          size="small"
          :loading="starting"
          :disabled="!query.trim() || missingRequired.length > 0 || pendingCount > 0"
          :title="runBlockedTitle"
          @click="start(false)"
        >
          {{ $t('workflow.run.syncRun') }}
        </t-button>
        <t-button
          variant="outline"
          size="small"
          :loading="starting"
          :disabled="!query.trim() || missingRequired.length > 0 || pendingCount > 0"
          :title="runBlockedTitle"
          @click="start(true)"
        >
          {{ $t('workflow.run.asyncRun') }}
        </t-button>
        <t-button v-if="streaming" variant="text" theme="default" size="small" @click="stop()">
          {{ $t('workflow.run.disconnect') }}
        </t-button>
        <t-button
          v-if="cancellable"
          variant="outline"
          theme="danger"
          size="small"
          :loading="cancelling"
          @click="cancelActiveRun"
        >
          {{ $t('workflow.run.cancel') }}
        </t-button>
      </div>
    </section>

    <!-- Progress timeline -->
    <section class="wf-run-section">
      <p class="wf-run-section-title">
        {{ $t('workflow.run.progress') }}
        <span v-if="streaming" class="wf-run-live">{{ $t('workflow.run.running') }}</span>
      </p>
      <div v-if="frames.length === 0" class="wf-run-muted">{{ $t('workflow.run.noProgress') }}</div>
      <ul v-else class="wf-run-timeline">
        <!-- delta frames are rendered in the live answer area, not the timeline -->
        <li v-for="(frame, index) in timelineFrames" :key="index" class="wf-run-frame" :class="`wf-run-frame--${frame.phase}`">
          <span class="wf-run-frame-dot" />
          <span class="wf-run-frame-text">
            <template v-if="frame.kind === 'node'">
              {{ nodeLabel(frame.node_id) }} · {{ $t(`workflow.run.phase.${frame.phase}`) }}
            </template>
            <template v-else>
              {{ $t('workflow.run.terminalFrame') }} · {{ $t(`workflow.run.status.${frame.status ?? frame.phase}`) }}
            </template>
            <span v-if="frame.replayed" class="wf-run-frame-duration">{{ $t('workflow.run.replayed') }}</span>
            <span v-if="frame.duration_ms" class="wf-run-frame-duration">{{ frame.duration_ms }}ms</span>
          </span>
          <span v-if="frame.error" class="wf-run-frame-error" :title="frame.error">{{ frame.error }}</span>
        </li>
      </ul>
    </section>

    <!-- Per-node outputs (debug payload): live frames or selected history run -->
    <section v-if="nodeRecords.length > 0" class="wf-run-section">
      <p class="wf-run-section-title">{{ $t('workflow.run.nodeOutputs') }}</p>
      <ul class="wf-run-records">
        <li v-for="record in nodeRecords" :key="record.key" class="wf-run-record">
          <button type="button" class="wf-run-record-head" @click="toggleRecord(record.key)">
            <t-tag size="small" :theme="record.phase === 'failed' ? 'danger' : 'success'" class="wf-run-record-kind">
              {{ record.kind }}
            </t-tag>
            <span class="wf-run-record-label">{{ record.label }}</span>
            <span v-if="record.replayed" class="wf-run-frame-duration">{{ $t('workflow.run.replayed') }}</span>
            <span v-if="record.duration_ms" class="wf-run-frame-duration">{{ record.duration_ms }}ms</span>
            <t-icon :name="expandedRecords.has(record.key) ? 'chevron-up' : 'chevron-down'" />
          </button>
          <div v-if="expandedRecords.has(record.key)" class="wf-run-record-body">
            <pre class="wf-run-record-json">{{ JSON.stringify(record.outputs ?? {}, null, 2) }}</pre>
          </div>
        </li>
      </ul>
    </section>

    <!-- Live answer stream (kind=delta frames tagged stream=answer) -->
    <section v-if="answerStream && streaming" class="wf-run-section">
      <p class="wf-run-section-title">
        {{ $t('workflow.run.result') }}
        <span class="wf-run-live">{{ $t('workflow.run.generating') }}</span>
      </p>
      <pre class="wf-run-answer">{{ answerStream }}<span class="wf-run-caret" /></pre>
    </section>

    <!-- Result -->
    <section v-if="answer !== null || resultError || isCancelled" class="wf-run-section">
      <p class="wf-run-section-title">{{ $t('workflow.run.result') }}</p>
      <t-alert v-if="resultError" theme="error" :message="resultError" />
      <pre v-else-if="answer" class="wf-run-answer">{{ answer }}</pre>
      <div v-else-if="isCancelled" class="wf-run-muted">{{ $t('workflow.run.cancelledNotice') }}</div>
      <div v-else class="wf-run-muted">{{ $t('workflow.run.noAnswer') }}</div>
    </section>

    <!-- History -->
    <section class="wf-run-section wf-run-history">
      <p class="wf-run-section-title">
        {{ $t('workflow.run.history') }}
        <t-button variant="text" size="small" @click="loadHistory">
          <template #icon><t-icon name="refresh" /></template>
        </t-button>
      </p>
      <div v-if="historyError" class="wf-run-muted">{{ $t('workflow.run.historyLoadFailed') }}</div>
      <div v-else-if="history.length === 0" class="wf-run-muted">{{ $t('workflow.run.historyEmpty') }}</div>
      <ul v-else class="wf-run-history-list">
        <li
          v-for="item in history"
          :key="item.id"
          class="wf-run-history-row"
          :class="{ 'wf-run-history-row--active': item.id === activeRunId }"
          @click="selectHistoryRow(item)"
        >
          <t-tag size="small" :theme="statusTheme(item.status)">
            {{ $t(`workflow.run.status.${item.status}`) }}
          </t-tag>
          <span class="wf-run-history-time">{{ formatTime(item.created_at) }}</span>
          <span class="wf-run-history-error" :title="item.error">{{ item.error }}</span>
          <t-button
            v-if="item.status === 'failed'"
            class="wf-run-history-resume"
            variant="text"
            size="small"
            theme="primary"
            :loading="resumingRunId === item.id"
            :disabled="!!resumingRunId"
            @click.stop="resumeRun(item)"
          >
            {{ $t('workflow.run.resume') }}
          </t-button>
        </li>
      </ul>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  runWorkflow,
  listWorkflowRuns,
  cancelWorkflowRun,
  resumeWorkflowRun,
  getWorkflowRun,
  uploadWorkflowRunAttachment,
  getWorkflowRunAttachment,
  type WorkflowRun,
  type WorkflowRunTraceEntry,
  type WorkflowRunAttachment,
} from '@/api/workflow'
import { useWorkflowRunStream } from '../useWorkflowRunStream'

const props = defineProps<{
  workflowId: string
  /** Canvas nodes (id + kind) so timelines show labels instead of raw ids. */
  nodes?: Array<{ id: string; kind: string }>
  /** Start-node form fields, rendered as run inputs. */
  startFields?: Array<{ name: string; label?: string; type: string; required?: boolean; default?: string; options?: string[] }>
  /** True when the workflow is published and the saved draft diverges from the published snapshot: runs execute the snapshot. */
  publishedStale?: boolean
}>()

/**
 * Node highlight state lives here and is exported upward: the editor maps
 * nodePhases onto canvas nodes (started → pulse, finished → green, failed →
 * red). "update" emit fires on every phase mutation the stream observes.
 *
 * node-outputs carries the per-node debug payload (live SSE frames or the
 * selected history run's trace) so canvas cards can render the inspect
 * badge. Cleared whenever the payload source changes or the panel unmounts.
 */
const emit = defineEmits<{
  'node-phases': [phases: Record<string, 'running' | 'done' | 'failed'>]
  'node-outputs': [outputs: Record<string, Record<string, unknown>>]
}>()

const { t } = useI18n()

const query = ref('')
const starting = ref(false)
// Start-form values keyed by field name (defaults seeded once per prop set).
const formValues = ref<Record<string, string>>({})
watch(
  () => props.startFields,
  (fields) => {
    const next: Record<string, string> = {}
    for (const field of fields ?? []) {
      next[field.name] = field.default ?? ''
    }
    formValues.value = next
  },
  { immediate: true },
)

/** Missing required form fields (drives the run button's disabled state). */
const missingRequired = computed(() =>
  (props.startFields ?? [])
    .filter((field) => field.required)
    .filter((field) => !String(formValues.value[field.name] ?? '').trim())
    .map((field) => field.label || field.name),
)
const answer = ref<string | null>(null)
const resultError = ref('')
const activeRunId = ref('')
// Mirrors the active run's lifecycle so the cancel affordance stays accurate
// even when the SSE stream is detached (disconnect ≠ cancel).
const activeStatus = ref('')
const cancelling = ref(false)
// Id of the run whose resume request is in flight (drives per-row loading
// state); empty string when idle.
const resumingRunId = ref('')

const history = ref<WorkflowRun[]>([])
const historyError = ref(false)

// ---- run attachments: upload → poll until parsed → carry ids on the run ----
const fileInput = ref<HTMLInputElement | null>(null)
const uploading = ref(false)
const attachments = ref<WorkflowRunAttachment[]>([])
let pollTimer: ReturnType<typeof setInterval> | null = null

const pendingCount = computed(
  () => attachments.value.filter((a) => a.status === 'uploaded' || a.status === 'processing').length,
)
const runBlockedTitle = computed(() => {
  if (missingRequired.value.length > 0) {
    return t('workflow.run.missingFields', { names: missingRequired.value.join(', ') })
  }
  if (pendingCount.value > 0) return t('workflow.run.attachmentWaitHint')
  return undefined
})

function attachIcon(status: WorkflowRunAttachment['status']): string {
  if (status === 'ready') return 'check-circle'
  if (status === 'failed') return 'error-circle'
  return 'loading'
}

async function onFilesPicked(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  if (files.length === 0) return
  uploading.value = true
  try {
    for (const file of files) {
      const response = await uploadWorkflowRunAttachment(props.workflowId, file)
      if (response?.data) attachments.value.push(response.data)
    }
    ensurePolling()
  } catch (error) {
    MessagePlugin.error(error instanceof Error ? error.message : t('workflow.run.attachmentUploadFailed'))
  } finally {
    uploading.value = false
  }
}

/** Poll non-terminal attachments until they are ready or failed. */
function ensurePolling() {
  if (pollTimer !== null) return
  pollTimer = setInterval(async () => {
    const pending = attachments.value.filter((a) => a.status === 'uploaded' || a.status === 'processing')
    if (pending.length === 0) {
      if (pollTimer !== null) clearInterval(pollTimer)
      pollTimer = null
      return
    }
    for (const a of pending) {
      try {
        const response = await getWorkflowRunAttachment(props.workflowId, a.id)
        if (response?.data) Object.assign(a, response.data)
      } catch {
        /* transient poll failure: retry on the next tick */
      }
    }
  }, 2000)
}

function removeAttachment(id: string) {
  attachments.value = attachments.value.filter((a) => a.id !== id)
}

onUnmounted(() => {
  if (pollTimer !== null) clearInterval(pollTimer)
})

// Trace of the selected history run (fetched from the run-detail endpoint);
// live runs build their records straight from SSE frames instead.
const selectedTrace = ref<WorkflowRunTraceEntry[] | null>(null)

const { frames, nodePhases, answerStream, terminalStatus, terminalError, streaming, follow, stop } = useWorkflowRunStream(
  () => props.workflowId,
)

emit('node-phases', nodePhases.value)

/** Lifecycle frames only — delta chunks render in the live answer area. */
const timelineFrames = computed(() => frames.value.filter((frame) => frame.kind !== 'delta'))

const cancellable = computed(
  () => activeStatus.value === 'pending' || activeStatus.value === 'running' || streaming.value,
)
const isCancelled = computed(() => activeStatus.value === 'cancelled' || terminalStatus.value === 'cancelled')

/** nodeId → localized label (kind name + short id) for timelines/records. */
function nodeLabel(nodeId?: string): string {
  if (!nodeId) return ''
  const node = props.nodes?.find((item) => item.id === nodeId)
  const kindName = node ? t(`workflow.nodes.${node.kind}`) : nodeId
  return `${kindName} (${nodeId})`
}

interface NodeRecord {
  key: string
  nodeId: string
  kind: string
  label: string
  phase: string
  duration_ms?: number
  outputs?: Record<string, unknown>
  error?: string
  replayed?: boolean
}

/**
 * Terminal node records in execution order: live frames while streaming,
 * the selected run's trace when reviewing history. started frames carry no
 * payload and are skipped.
 */
const nodeRecords = computed<NodeRecord[]>(() => {
  if (selectedTrace.value) {
    return selectedTrace.value.map((entry, index) => ({
      key: `${entry.node_id}-${index}`,
      nodeId: entry.node_id,
      kind: entry.kind || nodeKind(entry.node_id) || '?',
      label: nodeLabel(entry.node_id),
      phase: entry.phase,
      duration_ms: entry.duration_ms,
      outputs: entry.outputs,
      error: entry.error,
      replayed: entry.replayed,
    }))
  }
  return frames.value
    .filter((frame) => frame.kind === 'node' && frame.phase !== 'started')
    // (delta frames are display-stream data, not node records)
    .map((frame, index) => ({
      key: `${frame.node_id}-${index}`,
      nodeId: frame.node_id ?? '',
      kind: nodeKind(frame.node_id) || '?',
      label: nodeLabel(frame.node_id),
      phase: frame.phase,
      duration_ms: frame.duration_ms,
      outputs: frame.outputs,
      error: frame.error,
      replayed: frame.replayed,
    }))
})

function nodeKind(nodeId?: string): string {
  if (!nodeId) return ''
  return props.nodes?.find((item) => item.id === nodeId)?.kind ?? ''
}

const expandedRecords = ref(new Set<string>())

function toggleRecord(key: string) {
  const next = new Set(expandedRecords.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expandedRecords.value = next
}

const statusTheme = (status: WorkflowRun['status']): string => {
  if (status === 'succeeded') return 'success'
  if (status === 'failed') return 'danger'
  if (status === 'running' || status === 'pending') return 'warning'
  return 'default'
}

function formatTime(value?: string): string {
  if (!value) return ''
  try {
    return new Date(value).toLocaleString()
  } catch {
    return value
  }
}

function applyRunOutcome(run: WorkflowRun) {
  answer.value = run.output?.answer ?? null
  resultError.value = run.error ?? ''
}

async function start(asyncMode: boolean) {
  const trimmed = query.value.trim()
  if (!trimmed) {
    MessagePlugin.warning(t('workflow.run.queryRequired'))
    return
  }
  if (missingRequired.value.length > 0) {
    MessagePlugin.warning(t('workflow.run.missingFields', { names: missingRequired.value.join(', ') }))
    return
  }
  starting.value = true
  answer.value = null
  resultError.value = ''
  selectedTrace.value = null
  expandedRecords.value = new Set()
  try {
    const inputs = Object.keys(formValues.value).length > 0 ? { ...formValues.value } : undefined
    const files = attachments.value.length > 0 ? attachments.value.map((a) => a.id) : undefined
    const response = await runWorkflow(props.workflowId, { query: trimmed, inputs, files, async: asyncMode })
    const run = response?.run
    if (!run) {
      resultError.value = response?.message || t('workflow.run.runFailed')
      MessagePlugin.error(resultError.value)
      return
    }
    activeRunId.value = run.id
    activeStatus.value = run.status
    if (run.status === 'pending' || run.status === 'running') {
      follow(run.id)
    } else {
      // Synchronous runs are terminal by the time the POST returns; the
      // stream subscription would only replay the terminal frame — pull
      // the node trace from the run-detail endpoint instead.
      applyRunOutcome(run)
      void loadRunTrace(run.id)
    }
  } catch (error) {
    resultError.value = error instanceof Error ? error.message : t('workflow.run.runFailed')
    MessagePlugin.error(resultError.value)
  } finally {
    starting.value = false
  }
}

/** Fetch the trace of a run (run-detail endpoint) for the records section. */
async function loadRunTrace(runId: string) {
  try {
    const response = await getWorkflowRun(props.workflowId, runId)
    selectedTrace.value = response?.data?.trace ?? []
  } catch {
    selectedTrace.value = []
  }
}

async function loadHistory() {
  historyError.value = false
  try {
    const response = await listWorkflowRuns(props.workflowId)
    history.value = response?.data?.runs ?? []
  } catch {
    historyError.value = true
  }
}

function selectHistoryRow(run: WorkflowRun) {
  activeRunId.value = run.id
  activeStatus.value = run.status
  answer.value = null
  resultError.value = ''
  expandedRecords.value = new Set()
  if (run.status === 'pending' || run.status === 'running') {
    selectedTrace.value = null
    follow(run.id)
    return
  }
  stop()
  // Terminal rows: outcome inline; per-node records come from the trace.
  applyRunOutcome(run)
  void loadRunTrace(run.id)
}

// Terminal stream state mirrors into the result section (covers async runs
// whose final answer arrives via the terminal run frame).
watch([terminalStatus, terminalError], () => {
  if (terminalStatus.value) activeStatus.value = terminalStatus.value
  if (terminalError.value) {
    resultError.value = terminalError.value
    return
  }
  if (!terminalStatus.value) return
  // The terminal frame does not carry the answer; pull it from the row.
  const row = history.value.find((item) => item.id === activeRunId.value)
  if (row?.output?.answer) answer.value = row.output.answer
  else void refreshActiveRun()
  // Async run finished: fetch its trace for the records section.
  void loadRunTrace(activeRunId.value)
})

/**
 * Cancel the active run. The cancel response is authoritative for the UI;
 * the SSE terminal frame (phase=cancelled, sent right before the server
 * closes the stream) provides final consistency — both paths land in the
 * same state, so no manual stop() is forced here.
 */
async function cancelActiveRun() {
  if (!activeRunId.value || cancelling.value) return
  cancelling.value = true
  try {
    const response = await cancelWorkflowRun(props.workflowId, activeRunId.value)
    const run = response?.run
    if (!run) throw new Error(response?.message || t('workflow.run.cancelFailed'))
    activeStatus.value = run.status
    if (run.status === 'cancelled') {
      answer.value = null
      resultError.value = ''
    }
    void loadHistory()
  } catch (error) {
    MessagePlugin.error(error instanceof Error ? error.message : t('workflow.run.cancelFailed'))
  } finally {
    cancelling.value = false
  }
}

/**
 * Resume a failed run from its checkpoint. The backend flips the row to
 * running and re-executes asynchronously; follow() replaces any attached
 * stream (it stops the previous controller first), so resumed progress
 * renders in the same timeline without leaking the old subscription.
 * 409/404 paths surface the server message and refresh history so the row
 * shows its real state.
 */
async function resumeRun(run: WorkflowRun) {
  if (resumingRunId.value) return
  resumingRunId.value = run.id
  try {
    const response = await resumeWorkflowRun(props.workflowId, run.id)
    const resumed = response?.run
    if (!resumed) throw new Error(response?.message || t('workflow.run.resumeFailed'))
    activeRunId.value = resumed.id
    activeStatus.value = resumed.status
    answer.value = null
    resultError.value = ''
    selectedTrace.value = null
    follow(resumed.id)
    MessagePlugin.info(t('workflow.run.resumedNotice'))
  } catch (error) {
    MessagePlugin.error(error instanceof Error ? error.message : t('workflow.run.resumeFailed'))
  } finally {
    resumingRunId.value = ''
    void loadHistory()
  }
}

async function refreshActiveRun() {
  if (!activeRunId.value) return
  try {
    const response = await listWorkflowRuns(props.workflowId)
    history.value = response?.data?.runs ?? []
    const row = history.value.find((item) => item.id === activeRunId.value)
    if (row) {
      activeStatus.value = row.status
      applyRunOutcome(row)
    }
  } catch {
    /* history refresh is best-effort */
  }
}

// Propagate node phases upward as they change.
watch(
  nodePhases,
  (phases) => {
    emit('node-phases', { ...phases })
  },
  { deep: true },
)

// Propagate the debug payload upward: latest outputs per node (live frames
// or the selected history run's trace), for the canvas inspect badges.
watch(nodeRecords, (records) => {
  const outputs: Record<string, Record<string, unknown>> = {}
  for (const record of records) {
    if (!record.nodeId) continue
    const payload: Record<string, unknown> = { ...(record.outputs ?? {}) }
    if (record.error) payload.error = record.error
    outputs[record.nodeId] = payload
  }
  emit('node-outputs', outputs)
}, { deep: true })

onMounted(loadHistory)

defineExpose({ loadHistory })
</script>

<style scoped>
.wf-run-panel {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.wf-run-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.wf-run-section-title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  display: flex;
  align-items: center;
  gap: 8px;
}

.wf-run-live {
  font-size: 11px;
  font-weight: 500;
  color: var(--td-warning-color);
}

.wf-run-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.wf-run-muted {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.wf-run-timeline {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 180px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.wf-run-frame {
  display: flex;
  align-items: baseline;
  gap: 6px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.wf-run-frame-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--td-brand-color);
  flex: none;
  align-self: center;
}

.wf-run-frame--failed .wf-run-frame-dot {
  background: var(--td-error-color);
}

.wf-run-frame--succeeded .wf-run-frame-dot,
.wf-run-frame--finished .wf-run-frame-dot {
  background: var(--td-success-color);
}

.wf-run-frame-duration {
  color: var(--td-text-color-placeholder);
}

.wf-run-frame-error {
  color: var(--td-error-color);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Per-node output records (debug payload). */
.wf-run-records {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 260px;
  overflow-y: auto;
}

.wf-run-record {
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  overflow: hidden;
}

.wf-run-record-head {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 5px 8px;
  background: none;
  border: none;
  cursor: pointer;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  text-align: left;
}

.wf-run-record-head:hover {
  background: var(--td-bg-color-container-hover);
}

.wf-run-record-kind {
  flex: none;
}

.wf-run-record-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-primary);
}

.wf-run-record-body {
  border-top: 1px solid var(--td-component-stroke);
}

.wf-run-record-json {
  margin: 0;
  padding: 8px;
  max-height: 180px;
  overflow: auto;
  font-size: 11px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
  background: var(--td-bg-color-secondarycontainer);
}

.wf-run-caret {
  display: inline-block;
  width: 7px;
  height: 14px;
  margin-left: 1px;
  vertical-align: text-bottom;
  background: var(--td-brand-color);
  animation: wf-caret-blink 0.9s steps(1) infinite;
}

@keyframes wf-caret-blink {
  50% { opacity: 0; }
}

.wf-run-answer {
  margin: 0;
  padding: 10px;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 200px;
  overflow-y: auto;
}

.wf-run-attach {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.wf-run-attach-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 220px;
  padding: 2px 6px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 4px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.wf-run-attach-chip--ready {
  color: var(--td-success-color);
  border-color: var(--td-success-color-3);
}

.wf-run-attach-chip--failed {
  color: var(--td-error-color);
  border-color: var(--td-error-color-3);
}

.wf-run-attach-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.wf-run-attach-meta {
  flex: none;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}

.wf-run-attach-remove {
  cursor: pointer;
  flex: none;
}

.wf-run-history-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 200px;
  overflow-y: auto;
}

.wf-run-history-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 6px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 12px;
}

.wf-run-history-row:hover {
  background: var(--td-bg-color-container-hover);
}

.wf-run-history-row--active {
  background: var(--td-brand-color-light);
}

.wf-run-history-time {
  color: var(--td-text-color-placeholder);
  flex: none;
}

.wf-run-history-resume {
  margin-left: auto;
  flex: none;
  padding: 0 4px;
}

.wf-run-history-error {
  color: var(--td-error-color);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
