<!--
  PendingUsers.vue — SystemAdmin approval queue

  Lists users with is_approved = false (self-registered but not yet
  reviewed) and lets the operator approve or reject each one in place.

  Approval flow:
   1. Operator clicks Approve — server flips users.is_approved = true
      and (if the user registered with a department) flips their
      tenant_members row from 'invited' to 'active' in the same
      transaction.
   2. Operator clicks Reject — t-popconfirm asks for confirmation;
      on accept, the user row is hard-deleted (and so is the
      invited tenant_members row, by cascade).
   3. After either action we re-fetch the page so the table reflects
      the new state without manual refresh.

  The page is SystemAdmin-only. The Settings.vue parent gates it via
  SYSTEM_ADMIN_SETTINGS_SECTIONS in config/settingsAccess.ts so a
  non-admin never even mounts the component, but the API still
  re-checks the role on each request.
-->
<template>
  <div class="pending-users">
    <header class="section-header">
      <div>
        <h2>{{ t('auth.pendingApprovalTitle') }}</h2>
        <p class="section-description">{{ t('auth.pendingApprovalSubtitle') }}</p>
      </div>
      <button
        type="button"
        class="refresh-btn"
        :disabled="loading"
        :title="t('common.refresh')"
        :aria-label="t('common.refresh')"
        @click="reload"
      >
        <t-icon :name="loading ? 'loading' : 'refresh'" :class="{ 'refresh-btn__spin': loading }" />
      </button>
    </header>

    <div v-if="loading && users.length === 0" class="state state--loading">
      <t-loading size="small" />
      <span>{{ t('common.loading') }}</span>
    </div>

    <div v-else-if="error" class="state state--error" role="alert">
      <t-icon name="error-circle" size="20px" />
      <span>{{ error }}</span>
      <t-button size="small" variant="outline" @click="reload">{{ t('common.retry') }}</t-button>
    </div>

    <div v-else-if="users.length === 0" class="state state--empty">
      <t-icon name="check-circle" size="20px" />
      <span>{{ t('auth.pendingApprovalEmpty') }}</span>
    </div>

    <table v-else class="pending-users-table">
      <thead>
        <tr>
          <th>{{ t('auth.username') }}</th>
          <th>{{ t('auth.email') }}</th>
          <th>{{ t('common.tenant') }}</th>
          <th>{{ t('common.createdAt') }}</th>
          <th class="actions-col">{{ t('common.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="user in users" :key="user.id">
          <td>
            <span class="username">{{ user.username }}</span>
          </td>
          <td>
            <span class="email">{{ user.email }}</span>
          </td>
          <td>
            <span class="tenant-id">#{{ user.tenant_id }}</span>
          </td>
          <td>
            <span class="created-at">{{ formatDate(user.created_at) }}</span>
          </td>
          <td class="actions-cell">
            <t-button
              size="small"
              theme="primary"
              :loading="acting[user.id]?.approve"
              :disabled="acting[user.id]?.approve || acting[user.id]?.reject"
              @click="handleApprove(user)"
            >
              {{ t('auth.pendingApprovalApprove') }}
            </t-button>
            <t-popconfirm
              :content="t('auth.pendingApprovalRejectConfirm')"
              @confirm="handleReject(user)"
            >
              <t-button
                size="small"
                theme="danger"
                variant="outline"
                :loading="acting[user.id]?.reject"
                :disabled="acting[user.id]?.approve || acting[user.id]?.reject"
              >
                {{ t('auth.pendingApprovalReject') }}
              </t-button>
            </t-popconfirm>
          </td>
        </tr>
      </tbody>
    </table>

    <div v-if="total > limit" class="pagination">
      <span class="pagination__info">
        {{ (offset / limit) + 1 }} / {{ Math.ceil(total / limit) }}
      </span>
      <t-button
        size="small"
        variant="outline"
        :disabled="offset === 0"
        @click="prevPage"
      >
        {{ t('common.previous') }}
      </t-button>
      <t-button
        size="small"
        variant="outline"
        :disabled="offset + limit >= total"
        @click="nextPage"
      >
        {{ t('common.next') }}
      </t-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  approveUser,
  listPendingUsers,
  rejectUser,
  type PendingUser,
} from '@/api/system'

const { t } = useI18n()

const users = ref<PendingUser[]>([])
const total = ref(0)
const offset = ref(0)
const limit = 50
const loading = ref(false)
const error = ref('')
// Track per-user pending-action flags so the row's two buttons can show
// spinners independently without the table-level loading flicker.
const acting = reactive<Record<string, { approve: boolean; reject: boolean }>>({})

const reload = async () => {
  loading.value = true
  error.value = ''
  try {
    const resp = await listPendingUsers(offset.value, limit)
    users.value = resp.users
    total.value = resp.total
  } catch (err: any) {
    console.error('Failed to load pending users:', err)
    error.value = err?.message || String(err)
  } finally {
    loading.value = false
  }
}

const handleApprove = async (user: PendingUser) => {
  if (!acting[user.id]) acting[user.id] = { approve: false, reject: false }
  acting[user.id].approve = true
  try {
    await approveUser(user.id)
    MessagePlugin.success(`${t('auth.pendingApprovalApproved')}: ${user.email}`)
    // Drop the row locally so the table updates without a full refetch,
    // and decrement total to keep the pager consistent.
    users.value = users.value.filter((u) => u.id !== user.id)
    total.value = Math.max(0, total.value - 1)
  } catch (err: any) {
    console.error('Approve failed:', err)
    MessagePlugin.error(err?.message || t('auth.pendingApprovalFailed'))
  } finally {
    acting[user.id].approve = false
  }
}

const handleReject = async (user: PendingUser) => {
  if (!acting[user.id]) acting[user.id] = { approve: false, reject: false }
  acting[user.id].reject = true
  try {
    await rejectUser(user.id)
    MessagePlugin.success(`${t('auth.pendingApprovalRejected')}: ${user.email}`)
    users.value = users.value.filter((u) => u.id !== user.id)
    total.value = Math.max(0, total.value - 1)
  } catch (err: any) {
    console.error('Reject failed:', err)
    MessagePlugin.error(err?.message || t('auth.pendingApprovalFailed'))
  } finally {
    acting[user.id].reject = false
  }
}

const prevPage = () => {
  offset.value = Math.max(0, offset.value - limit)
  reload()
}

const nextPage = () => {
  if (offset.value + limit < total.value) {
    offset.value += limit
    reload()
  }
}

const formatDate = (iso: string) => {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

onMounted(reload)
</script>

<style scoped>
.pending-users {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.section-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}
.section-header h2 {
  margin: 0 0 4px;
  font-size: 18px;
  font-weight: 600;
}
.section-description {
  margin: 0;
  color: var(--td-text-color-secondary, #666);
  font-size: 13px;
}

.refresh-btn {
  padding: 6px;
  border: 1px solid var(--td-component-stroke, #e7e7e7);
  border-radius: 4px;
  background: transparent;
  cursor: pointer;
}
.refresh-btn:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}
.refresh-btn__spin {
  animation: spin 0.8s linear infinite;
}
@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.state {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 24px;
  border-radius: 6px;
  color: var(--td-text-color-secondary, #666);
  font-size: 13px;
}
.state--loading,
.state--empty {
  background: var(--td-bg-color-container, #f5f5f5);
  justify-content: center;
}
.state--error {
  background: var(--td-error-color-1, #fff0ee);
  color: var(--td-error-color, #d54941);
}

.pending-users-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.pending-users-table th,
.pending-users-table td {
  padding: 10px 12px;
  text-align: left;
  border-bottom: 1px solid var(--td-component-stroke, #e7e7e7);
}
.pending-users-table th {
  font-weight: 600;
  color: var(--td-text-color-secondary, #666);
  background: var(--td-bg-color-container, #f5f5f5);
}
.username {
  font-weight: 500;
}
.email {
  color: var(--td-text-color-secondary, #666);
}
.tenant-id {
  font-family: monospace;
  font-size: 12px;
  background: var(--td-bg-color-container, #f5f5f5);
  padding: 2px 6px;
  border-radius: 3px;
}
.created-at {
  color: var(--td-text-color-secondary, #666);
  font-size: 12px;
}
.actions-col {
  width: 200px;
  text-align: right;
}
.actions-cell {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}

.pagination {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  font-size: 13px;
  color: var(--td-text-color-secondary, #666);
}
.pagination__info {
  margin-right: auto;
}
</style>
