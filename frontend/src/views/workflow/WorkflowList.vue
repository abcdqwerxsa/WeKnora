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
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { deleteWorkflow, createWorkflow, listWorkflows, type Workflow } from '@/api/workflow'

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
  { colKey: 'description', title: t('workflow.description'), ellipsis: true, minWidth: 220 },
  { colKey: 'status', title: t('workflow.status'), width: 110, align: 'center' as const },
  { colKey: 'updated_at', title: t('workflow.updatedAt'), width: 180 },
  { colKey: 'actions', title: t('workflow.actions'), width: 140, align: 'right' as const },
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
    workflows.value = Array.isArray(response?.data) ? response.data : []
  } catch (error) {
    loadError.value = true
    loadErrorDetail.value = error instanceof Error ? error.message : String(error)
    workflows.value = []
  } finally {
    loading.value = false
  }
}

function goEdit(workflow: Workflow) {
  router.push(`/workflow/${workflow.id}/edit`)
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
      router.push(`/workflow/${created.id}/edit`)
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
  gap: 4px;
}
</style>
