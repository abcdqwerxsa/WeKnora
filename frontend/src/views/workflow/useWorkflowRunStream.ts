import { onBeforeUnmount, ref } from 'vue'
import { fetchEventSource } from '@microsoft/fetch-event-source'
import i18n from '@/i18n'
import { generateRandomString } from '@/utils'
import { getApiBaseUrl } from '@/utils/api-base'
import { workflowRunEventsUrl, type WorkflowRunEventFrame } from '@/api/workflow'

/**
 * Live progress of one workflow run over SSE.
 *
 * Mirrors useConfigSkillInstallProgress (same fetchEventSource + auth-header
 * recipe). One stream at a time: following a new run implicitly closes the
 * previous one. Terminal frames (kind=run) close the stream; late
 * subscribers get a single terminal replay frame from the server, which is
 * what makes clicking a history row "just work".
 *
 * Note: synchronously executed runs finish before this client can subscribe,
 * so only the terminal replay frame arrives — live node progress is an
 * async-mode affordance.
 */
export type NodeRunPhase = 'running' | 'done' | 'failed'

function framePhaseToNodePhase(phase: string): NodeRunPhase {
  if (phase === 'started') return 'running'
  if (phase === 'failed') return 'failed'
  return 'done'
}

export function useWorkflowRunStream(workflowId: () => string) {
  const frames = ref<WorkflowRunEventFrame[]>([])
  const nodePhases = ref<Record<string, NodeRunPhase>>({})
  const terminalStatus = ref('')
  const terminalError = ref('')
  const streaming = ref(false)
  let controller: AbortController | null = null

  function resetStreamState() {
    frames.value = []
    nodePhases.value = {}
    terminalStatus.value = ''
    terminalError.value = ''
  }

  /** Detach from the stream only — the run keeps executing server-side. */
  function stop() {
    controller?.abort()
    controller = null
    streaming.value = false
  }

  function follow(runId: string) {
    stop()
    resetStreamState()
    if (!workflowId() || !runId) return
    controller = new AbortController()
    streaming.value = true
    const token = localStorage.getItem('weknora_token')
    const tenantId = localStorage.getItem('weknora_selected_tenant_id')
    const url = `${getApiBaseUrl()}${workflowRunEventsUrl(workflowId(), runId)}`

    void fetchEventSource(url, {
      method: 'GET',
      headers: {
        Authorization: token ? `Bearer ${token}` : '',
        'Accept-Language': i18n.global.locale?.value || localStorage.getItem('locale') || 'zh-CN',
        'X-Request-ID': generateRandomString(12),
        ...(tenantId ? { 'X-Tenant-ID': tenantId } : {}),
      },
      signal: controller.signal,
      openWhenHidden: true,
      onmessage(ev) {
        if (!ev.data) return
        let frame: WorkflowRunEventFrame
        try {
          frame = JSON.parse(ev.data) as WorkflowRunEventFrame
        } catch {
          return
        }
        frames.value = [...frames.value, frame]
        if (frame.kind === 'node' && frame.node_id) {
          nodePhases.value = {
            ...nodePhases.value,
            [frame.node_id]: framePhaseToNodePhase(frame.phase),
          }
        }
        if (frame.kind === 'run') {
          terminalStatus.value = frame.status ?? frame.phase
          terminalError.value = frame.error ?? ''
          stop()
        }
      },
      onerror() {
        stop()
        // Do not let fetch-event-source auto-retry: a closed stream on a
        // terminal/failed run should stay closed (history shows the row).
        throw new Error('workflow run stream closed')
      },
    }).catch(() => {
      stop()
    })
  }

  onBeforeUnmount(stop)

  return { frames, nodePhases, terminalStatus, terminalError, streaming, follow, stop, resetStreamState }
}
