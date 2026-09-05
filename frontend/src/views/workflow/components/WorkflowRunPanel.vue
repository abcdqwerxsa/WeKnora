<template>
  <div class="wf-run-panel">
    <!-- Trigger -->
    <section class="wf-run-section">
      <t-textarea
        v-model="query"
        :autosize="{ minRows: 2, maxRows: 6 }"
        :placeholder="$t('workflow.run.queryPlaceholder')"
        :disabled="starting"
      />
      <div class="wf-run-actions">
        <t-button theme="primary" size="small" :loading="starting" :disabled="!query.trim()" @click="start(false)">
          {{ $t('workflow.run.syncRun') }}
        </t-button>
        <t-button variant="outline" size="small" :loading="starting" :disabled="!query.trim()" @click="start(true)">
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
        <li v-for="(frame, index) in frames" :key="index" class="wf-run-frame" :class="`wf-run-frame--${frame.phase}`">
          <span class="wf-run-frame-dot" />
          <span class="wf-run-frame-text">
            <template v-if="frame.kind === 'node'">
              {{ frame.node_id }} · {{ $t(`workflow.run.phase.${frame.phase}`) }}
            </template>
            <template v-else>
              {{ $t('workflow.run.terminalFrame') }} · {{ $t(`workflow.run.status.${frame.status ?? frame.phase}`) }}
            </template>
            <span v-if="frame.duration_ms" class="wf-run-frame-duration">{{ frame.duration_ms }}ms</span>
          </span>
          <span v-if="frame.error" class="wf-run-frame-error">{{ frame.error }}</span>
        </li>
      </ul>
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
import { computed, onMounted, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { runWorkflow, listWorkflowRuns, cancelWorkflowRun, resumeWorkflowRun, type WorkflowRun } from '@/api/workflow'
import { useWorkflowRunStream } from '../useWorkflowRunStream'

const props = defineProps<{ workflowId: string }>()

/**
 * Node highlight state lives here and is exported upward: the editor maps
 * nodePhases onto canvas nodes (started → pulse, finished → green, failed →
 * red). "update" emit fires on every phase mutation the stream observes.
 */
const emit = defineEmits<{ 'node-phases': [phases: Record<string, 'running' | 'done' | 'failed'>] }>()

const { t } = useI18n()

const query = ref('')
const starting = ref(false)
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

const { frames, nodePhases, terminalStatus, terminalError, streaming, follow, stop } = useWorkflowRunStream(
  () => props.workflowId,
)

emit('node-phases', nodePhases.value)

const cancellable = computed(
  () => activeStatus.value === 'pending' || activeStatus.value === 'running' || streaming.value,
)
const isCancelled = computed(() => activeStatus.value === 'cancelled' || terminalStatus.value === 'cancelled')

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
  starting.value = true
  answer.value = null
  resultError.value = ''
  try {
    const response = await runWorkflow(props.workflowId, { query: trimmed, async: asyncMode })
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
      // stream subscription would only replay the terminal frame.
      applyRunOutcome(run)
    }
  } catch (error) {
    resultError.value = error instanceof Error ? error.message : t('workflow.run.runFailed')
    MessagePlugin.error(resultError.value)
  } finally {
    starting.value = false
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
  if (run.status === 'pending' || run.status === 'running') {
    follow(run.id)
    return
  }
  stop()
  // Terminal rows carry their outcome inline; no stream needed.
  answer.value = run.output?.answer ?? null
  resultError.value = run.error ?? ''
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
