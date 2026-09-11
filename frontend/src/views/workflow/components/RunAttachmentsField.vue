<template>
  <div class="wf-attach">
    <input ref="fileInput" type="file" multiple hidden @change="onFilesPicked" />
    <t-button variant="outline" size="small" :loading="uploading" :disabled="disabled" @click="fileInput?.click()">
      <template #icon><t-icon name="upload" /></template>
      {{ t('workflow.run.attachFiles') }}
    </t-button>
    <span v-for="a in attachments" :key="a.id" class="wf-attach-chip" :class="`wf-attach-chip--${a.status}`">
      <t-icon :name="attachIcon(a.status)" />
      <span class="wf-attach-name" :title="a.error_message || a.file_name">{{ a.file_name }}</span>
      <span v-if="a.status === 'ready'" class="wf-attach-meta">{{ a.chunk_count }}c</span>
      <t-icon name="close" class="wf-attach-remove" @click="removeAttachment(a.id)" />
    </span>
    <p class="wf-attach-hint">{{ t('workflow.run.attachHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  getWorkflowRunAttachment,
  uploadWorkflowRunAttachment,
  type WorkflowRunAttachment,
} from '@/api/workflow'

/**
 * Shared run-attachments field (upload → async parse → poll → chips).
 * The ready attachment ids are the run's `files` payload; the backend
 * resolves them into LLM context at execution.
 */
const props = defineProps<{
  workflowId: string
  disabled?: boolean
}>()

const attachments = defineModel<WorkflowRunAttachment[]>({ default: () => [] })

const { t } = useI18n()
const fileInput = ref<HTMLInputElement | null>(null)
const uploading = ref(false)
let pollTimer: number | null = null

const pendingCount = computed(
  () => attachments.value.filter((a) => a.status === 'uploaded' || a.status === 'processing').length,
)
const readyIds = computed(() =>
  attachments.value.filter((a) => a.status === 'ready').map((a) => a.id),
)

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
  } catch (error) {
    MessagePlugin.error(error instanceof Error ? error.message : t('workflow.run.attachmentUploadFailed'))
  } finally {
    // Poll even on partial failure: earlier uploads already sit at
    // "uploaded" and must not strand un-polled (they would block the run
    // forever via pendingCount).
    ensurePolling()
    uploading.value = false
  }
}

function ensurePolling() {
  if (pollTimer !== null) return
  pollTimer = window.setInterval(async () => {
    const pending = attachments.value.filter((a) => a.status === 'uploaded' || a.status === 'processing')
    if (pending.length === 0) {
      if (pollTimer !== null) window.clearInterval(pollTimer)
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

defineExpose({ pendingCount, readyIds })

onUnmounted(() => {
  if (pollTimer !== null) window.clearInterval(pollTimer)
})
</script>

<style scoped>
.wf-attach {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.wf-attach-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 160px;
  padding: 2px 6px;
  border-radius: var(--td-radius-medium);
  border: 1px solid var(--td-component-stroke);
  font-size: 11px;
  background: var(--td-bg-color-container);
}

.wf-attach-chip--ready {
  border-color: var(--td-success-color);
}

.wf-attach-chip--failed {
  border-color: var(--td-error-color);
}

.wf-attach-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.wf-attach-meta {
  color: var(--td-text-color-placeholder);
  flex: none;
}

.wf-attach-remove {
  cursor: pointer;
  flex: none;
  color: var(--td-text-color-placeholder);
}

.wf-attach-remove:hover {
  color: var(--td-error-color);
}

.wf-attach-hint {
  width: 100%;
  margin: 2px 0 0;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}
</style>
