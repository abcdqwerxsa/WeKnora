<template>
  <div class="wf-list-page">
    <div class="wf-list-header">
      <div class="wf-list-heading">
        <h2>{{ $t('workflow.title') }}</h2>
        <p class="wf-list-subtitle">{{ $t('workflow.subtitle') }}</p>
      </div>
      <t-button theme="primary" @click="openCreate">
        <template #icon><t-icon name="add" /></template>
        {{ $t('workflow.create') }}
      </t-button>
    </div>

    <div v-if="loading" class="wf-list-state">
      <t-loading />
    </div>

    <div v-else-if="loadError" class="wf-list-state">
      <p>{{ $t('workflow.loadFailed') }}</p>
      <p v-if="loadErrorDetail" class="wf-list-error-detail">{{ loadErrorDetail }}</p>
      <t-button variant="outline" @click="loadWorkflows">{{ $t('workflow.retry') }}</t-button>
    </div>

    <div v-else-if="workflows.length === 0" class="wf-list-state">
      <p>{{ $t('workflow.empty') }}</p>
      <p class="wf-list-subtitle">{{ $t('workflow.emptyHint') }}</p>
    </div>

    <div v-else class="data-table-shell">
      <t-table row-key="id" :data="workflows" :columns="columns" size="medium" hover>
        <template #name="{ row }">
          <span class="wf-list-name" role="button" @click="goEdit(row)">{{ row.name }}</span>
        </template>
        <template #status="{ row }">
          <t-tag :theme="statusTheme(row.status)" size="small">
            {{ $t(`workflow.status.${row.status}`) }}
          </t-tag>
        </template>
        <template #updated_at="{ row }">
          <span>{{ formatTime(row.updated_at) }}</span>
        </template>
        <template #actions="{ row }">
          <div class="wf-list-actions">
            <t-button variant="text" size="small" @click="goEdit(row)">{{ $t('workflow.edit') }}</t-button>
            <t-button
              v-if="row.status === 'published'"
              variant="text"
              size="small"
              @click="openSchedules(row)"
            >
              {{ $t('workflow.schedules.manage') }}
            </t-button>
            <t-popconfirm
              v-if="row.status !== 'published'"
              :content="$t('workflow.publishConfirm', { name: row.name })"
              @confirm="publish(row)"
            >
              <t-button variant="text" size="small" theme="primary" :loading="actingId === row.id">{{ $t('workflow.publish') }}</t-button>
            </t-popconfirm>
            <t-button v-else variant="text" size="small" :loading="actingId === row.id" @click="unpublish(row)">
              {{ $t('workflow.unpublish') }}
            </t-button>
            <t-button v-if="row.status !== 'archived'" variant="text" size="small" :disabled="actingId === row.id" @click="archive(row)">
              {{ $t('workflow.archive') }}
            </t-button>
            <t-popconfirm :content="$t('workflow.deleteConfirm', { name: row.name })" @confirm="removeWorkflow(row)">
              <t-button variant="text" size="small" theme="danger">{{ $t('workflow.delete') }}</t-button>
            </t-popconfirm>
          </div>
        </template>
      </t-table>
    </div>

    <t-dialog
      v-model:visible="createVisible"
      :header="$t('workflow.createTitle')"
      :confirm-btn="{ content: $t('workflow.confirm'), loading: creating }"
      :cancel-btn="$t('workflow.cancel')"
      @confirm="submitCreate"
    >
      <t-form label-align="top">
        <t-form-item :label="$t('workflow.name')" :mark="true">
          <t-input v-model="createForm.name" :placeholder="$t('workflow.namePlaceholder')" :maxlength="255" />
        </t-form-item>
        <t-form-item :label="$t('workflow.description')">
          <t-textarea v-model="createForm.description" :placeholder="$t('workflow.descriptionPlaceholder')" :maxlength="2000" :autosize="{ minRows: 2, maxRows: 4 }" />
        </t-form-item>
      </t-form>
    </t-dialog>
    <t-dialog
      v-model:visible="scheduleDialogVisible"
      :header="$t('workflow.schedules.dialogTitle', { name: scheduleWorkflow?.name ?? '' })"
      width="640px"
      :footer="false"
      :close-btn="true"
    >
      <div class="wf-schedules">
        <t-form label-align="top">
          <t-form-item :label="t('workflow.schedules.cron')" :mark="true">
            <t-input v-model="scheduleForm.cron" placeholder="*/5 * * * *" />
          </t-form-item>
          <t-form-item :label="t('workflow.schedules.query')">
            <t-input v-model="scheduleForm.query" :placeholder="t('workflow.schedules.queryPlaceholder')" />
          </t-form-item>
          <t-form-item :label="t('workflow.schedules.enabled')">
            <t-switch v-model="scheduleForm.enabled" />
          </t-form-item>
        </t-form>
        <div class="wf-schedules-actions">
          <t-button theme="primary" size="small" :loading="scheduleSaving" :disabled="!scheduleForm.cron.trim()" @click="createSchedule">
            {{ $t('workflow.schedules.create') }}
          </t-button>
        </div>
        <p class="wf-schedules-hint">{{ t('workflow.schedules.hint') }}</p>
        <div v-if="schedulesLoading" class="wf-schedules-state"><t-loading /></div>
        <div v-else-if="schedules.length === 0" class="wf-schedules-state">{{ $t('workflow.schedules.empty') }}</div>
        <ul v-else class="wf-schedules-list">
          <li v-for="item in schedules" :key="item.id" class="wf-schedules-row">
            <t-tag size="small" :theme="item.enabled ? 'success' : 'default'">{{ item.cron }}</t-tag>
            <span class="wf-schedules-query" :title="item.query">{{ item.query || '—' }}</span>
            <span class="wf-schedules-time">{{ formatTime(item.created_at) }}</span>
            <t-button
              variant="text"
              size="small"
              :theme="item.enabled ? 'warning' : 'primary'"
              :disabled="scheduleActingId === item.id"
              @click="toggleSchedule(item)"
            >
              {{ item.enabled ? t('workflow.schedules.disable') : t('workflow.schedules.enable') }}
            </t-button>
            <t-popconfirm :content="t('workflow.schedules.deleteConfirm')" @confirm="removeSchedule(item)">
              <t-button variant="text" size="small" theme="danger" :disabled="scheduleActingId === item.id">
                {{ $t('workflow.delete') }}
              </t-button>
            </t-popconfirm>
          </li>
        </ul>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { deleteWorkflow, createWorkflow, listWorkflows, publishWorkflow, setWorkflowStatus, type Workflow, type WorkflowMutationResponse } from '@/api/workflow'
import {
  listWorkflowSchedules,
  createWorkflowSchedule,
  deleteWorkflowSchedule,
  enableWorkflowSchedule,
  disableWorkflowSchedule,
  type WorkflowSchedule,
} from '@/api/workflowSchedule'

const { t } = useI18n()
const router = useRouter()

const loading = ref(false)
const loadError = ref(false)
const loadErrorDetail = ref('')
const workflows = ref<Workflow[]>([])

const createVisible = ref(false)
const creating = ref(false)
const createForm = ref({ name: '', description: '' })

const columns = computed(() => [
  { colKey: 'name', title: t('workflow.name'), minWidth: 180 },
  { colKey: 'description', title: t('workflow.description'), ellipsis: true, minWidth: 200 },
  { colKey: 'status', title: t('workflow.status'), width: 110, align: 'center' as const },
  { colKey: 'updated_at', title: t('workflow.updatedAt'), width: 170 },
  { colKey: 'actions', title: t('workflow.actions'), width: 250, align: 'right' as const },
])

function statusTheme(status: Workflow['status']) {
  if (status === 'published') return 'success'
  if (status === 'archived') return 'default'
  return 'warning'
}

function formatTime(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

async function loadWorkflows() {
  loading.value = true
  loadError.value = false
  loadErrorDetail.value = ''
  try {
    const response = await listWorkflows()
    workflows.value = Array.isArray(response?.data?.workflows) ? response.data.workflows : []
  } catch (error) {
    loadError.value = true
    loadErrorDetail.value = error instanceof Error ? error.message : String(error)
    workflows.value = []
  } finally {
    loading.value = false
  }
}

function goEdit(workflow: Workflow) {
  router.push(`/platform/workflow/${workflow.id}/edit`)
}

function openCreate() {
  createForm.value = { name: '', description: '' }
  createVisible.value = true
}

async function submitCreate() {
  const name = createForm.value.name.trim()
  if (!name) {
    MessagePlugin.warning(t('workflow.nameRequired'))
    return
  }
  creating.value = true
  try {
    const response = await createWorkflow({ name, description: createForm.value.description.trim() })
    const created = response?.data
    if (response?.success && created?.id) {
      createVisible.value = false
      MessagePlugin.success(t('workflow.created'))
      router.push(`/platform/workflow/${created.id}/edit`)
    } else {
      MessagePlugin.error(response?.message || t('workflow.createFailed'))
    }
  } catch (error) {
    MessagePlugin.error(error instanceof Error ? error.message : t('workflow.createFailed'))
  } finally {
    creating.value = false
  }
}

async function removeWorkflow(workflow: Workflow) {
  try {
    await deleteWorkflow(workflow.id)
    MessagePlugin.success(t('workflow.deleted'))
    await loadWorkflows()
  } catch (error) {
    MessagePlugin.error(error instanceof Error ? error.message : t('workflow.deleteFailed'))
  }
}

// Id of the workflow whose publish/unpublish/archive request is in flight.
const actingId = ref('')

// One scaffold for the status actions (publish / unpublish / archive):
// actingId guard → call → toast → reload → clear.
async function act(workflow: Workflow, call: () => Promise<WorkflowMutationResponse>, successKey: string) {
  if (actingId.value) return
  actingId.value = workflow.id
  try {
    const response = await call()
    if (response?.success) {
      MessagePlugin.success(t(successKey))
      await loadWorkflows()
    } else {
      MessagePlugin.error(response?.message || t('workflow.actionFailed'))
    }
  } catch (error) {
    MessagePlugin.error(error instanceof Error ? error.message : t('workflow.actionFailed'))
  } finally {
    actingId.value = ''
  }
}

function publish(workflow: Workflow) {
  return act(workflow, () => publishWorkflow(workflow.id), 'workflow.published')
}

function unpublish(workflow: Workflow) {
  return act(workflow, () => setWorkflowStatus(workflow.id, 'draft'), 'workflow.unpublished')
}

function archive(workflow: Workflow) {
  return act(workflow, () => setWorkflowStatus(workflow.id, 'archived'), 'workflow.archived')
}

// ---- schedules dialog -------------------------------------------------------

const scheduleDialogVisible = ref(false)
const scheduleWorkflow = ref<Workflow | null>(null)
const schedules = ref<WorkflowSchedule[]>([])
const schedulesLoading = ref(false)
const scheduleSaving = ref(false)
const scheduleActingId = ref('')
const scheduleForm = ref({ cron: '', query: '', enabled: true })

function openSchedules(workflow: Workflow) {
  scheduleWorkflow.value = workflow
  scheduleForm.value = { cron: '', query: '', enabled: true }
  scheduleDialogVisible.value = true
  void loadSchedules()
}

async function loadSchedules() {
  if (!scheduleWorkflow.value) return
  schedulesLoading.value = true
  try {
    const response = await listWorkflowSchedules(scheduleWorkflow.value.id)
    schedules.value = response?.data?.schedules ?? []
  } catch {
    schedules.value = []
  } finally {
    schedulesLoading.value = false
  }
}

async function createSchedule() {
  if (!scheduleWorkflow.value) return
  scheduleSaving.value = true
  try {
    const response = await createWorkflowSchedule(scheduleWorkflow.value.id, {
      cron: scheduleForm.value.cron.trim(),
      query: scheduleForm.value.query.trim(),
      enabled: scheduleForm.value.enabled,
    })
    if (response?.success) {
      MessagePlugin.success(t('workflow.schedules.created'))
      scheduleForm.value = { cron: '', query: '', enabled: true }
      await loadSchedules()
    } else {
      MessagePlugin.error(response?.message || t('workflow.schedules.createFailed'))
    }
  } catch (error) {
    MessagePlugin.error(error instanceof Error ? error.message : t('workflow.schedules.createFailed'))
  } finally {
    scheduleSaving.value = false
  }
}

async function toggleSchedule(item: WorkflowSchedule) {
  if (!scheduleWorkflow.value || scheduleActingId.value) return
  scheduleActingId.value = item.id
  try {
    const workflowId = scheduleWorkflow.value.id
    const response = item.enabled
      ? await disableWorkflowSchedule(workflowId, item.id)
      : await enableWorkflowSchedule(workflowId, item.id)
    if (response?.success) {
      await loadSchedules()
    } else {
      MessagePlugin.error(response?.message || t('workflow.schedules.createFailed'))
    }
  } catch (error) {
    MessagePlugin.error(error instanceof Error ? error.message : t('workflow.schedules.createFailed'))
  } finally {
    scheduleActingId.value = ''
  }
}

async function removeSchedule(item: WorkflowSchedule) {
  if (!scheduleWorkflow.value) return
  try {
    await deleteWorkflowSchedule(scheduleWorkflow.value.id, item.id)
    MessagePlugin.success(t('workflow.schedules.deleted'))
    await loadSchedules()
  } catch (error) {
    MessagePlugin.error(error instanceof Error ? error.message : t('workflow.schedules.createFailed'))
  }
}

onMounted(loadWorkflows)
</script>

<style scoped>
.wf-list-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 24px;
  height: 100%;
  overflow: auto;
  box-sizing: border-box;
}

.wf-list-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.wf-list-heading h2 {
  margin: 0;
}

.wf-list-subtitle {
  margin: 4px 0 0;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}

.wf-list-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 64px 0;
  color: var(--td-text-color-secondary);
}

.wf-list-error-detail {
  margin: 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.wf-list-name {
  cursor: pointer;
  color: var(--td-brand-color);
  font-weight: 500;
}

.wf-list-actions {
  display: inline-flex;
  gap: 2px;
}
</style>
