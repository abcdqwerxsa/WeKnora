<template>
    <!--
        TaskWorkspaceDrawer — session-level task workspace.

        Shows everything the agent produced and did across the whole
        conversation, not just one reply:
          - 产物 tab: every artifact recorded against the session
            (GET /sessions/:id/artifacts), with inline preview (reuses
            DocumentPreview, same renderer as ChatArtifactsDrawer) and
            download via the message-scoped endpoint.
          - 执行记录 tab: aggregated tool-call history walked from the
            in-memory agentEventStream of each assistant message —
            shell commands, skill scripts, knowledge searches, etc.

        Parent (chat/index.vue) owns the trigger button and passes the
        live message list so both tabs follow the conversation.
    -->
    <t-drawer
        v-model:visible="internalVisible"
        class="task-workspace-drawer"
        placement="right"
        size="520px"
        :z-index="1500"
        attach="body"
        :footer="false"
        :close-on-overlay-click="true"
        :close-on-esc-keydown="true"
    >
        <template #header>
            <div v-if="previewItem" class="tw-header">
                <t-button
                    variant="text"
                    shape="square"
                    size="small"
                    :title="$t('agent.taskWorkspace.previewBack')"
                    :aria-label="$t('agent.taskWorkspace.previewBack')"
                    @click="closePreview"
                >
                    <template #icon><t-icon name="chevron-left" size="18px" /></template>
                </t-button>
                <div class="tw-header-title" :title="previewItem.file_name">{{ previewItem.file_name }}</div>
                <t-button
                    variant="text"
                    shape="square"
                    size="small"
                    :title="$t('agent.taskWorkspace.download')"
                    :loading="!!downloading[`${previewItem.message_id}-${previewItem.index}`]"
                    @click="handleDownload(previewItem)"
                >
                    <template #icon><t-icon name="download" size="16px" /></template>
                </t-button>
            </div>
            <div v-else class="tw-header">
                <div class="tw-header-icon"><t-icon name="folder" /></div>
                <div class="tw-header-title">{{ $t('agent.taskWorkspace.title') }}</div>
                <t-button
                    v-if="activeTab === 'artifacts'"
                    variant="text"
                    shape="square"
                    size="small"
                    class="tw-refresh"
                    :title="$t('agent.taskWorkspace.refresh')"
                    :loading="loading"
                    @click="fetchArtifacts"
                >
                    <template #icon><t-icon name="refresh" size="16px" /></template>
                </t-button>
            </div>
        </template>

        <div v-if="previewItem" class="tw-preview-body">
            <DocumentPreview
                :session-id="sessionId"
                :message-id="previewItem.message_id"
                :artifact-index="previewItem.index"
                :file-type="previewItem.file_type"
                :file-name="previewItem.file_name"
                :active="internalVisible"
                fill-height
            />
        </div>

        <t-tabs v-else v-model="activeTab" class="tw-tabs">
            <t-tab-panel value="artifacts" :label="$t('agent.taskWorkspace.tabArtifacts')">
                <div v-if="collecting" class="tw-hint">
                    <t-loading size="small" />
                    <span>{{ $t('agent.taskWorkspace.collecting') }}</span>
                </div>
                <div v-if="loading && !items.length" class="tw-empty">
                    <t-loading size="small" />
                    <span>{{ $t('common.loading') }}</span>
                </div>
                <div v-else-if="loadError && !items.length" class="tw-empty">
                    <t-icon name="error-circle" size="32px" />
                    <span>{{ $t('agent.taskWorkspace.loadFailed') }}</span>
                </div>
                <div v-else-if="!items.length" class="tw-empty">
                    <t-icon name="folder-open" size="32px" />
                    <span>{{ $t('agent.taskWorkspace.empty') }}</span>
                </div>
                <ul v-else class="tw-artifact-list">
                    <li
                        v-for="item in items"
                        :key="`${item.message_id}-${item.index}`"
                        class="tw-artifact-item"
                        @click="openPreview(item)"
                    >
                        <span class="tw-artifact-icon"><t-icon :name="getFileIcon(item.file_name)" /></span>
                        <div class="tw-artifact-body">
                            <div class="tw-artifact-name" :title="item.file_name">{{ item.file_name }}</div>
                            <div class="tw-artifact-meta">
                                <span>{{ formatFileSize(item.file_size) }}</span>
                                <span>{{ formatDateTime(item.created_at) }}</span>
                            </div>
                        </div>
                        <t-button
                            variant="text"
                            shape="square"
                            size="small"
                            :title="$t('agent.taskWorkspace.download')"
                            :loading="!!downloading[`${item.message_id}-${item.index}`]"
                            @click.stop="handleDownload(item)"
                        >
                            <template #icon><t-icon name="download" size="16px" /></template>
                        </t-button>
                    </li>
                </ul>
            </t-tab-panel>

            <t-tab-panel value="terminal" :label="$t('agent.taskWorkspace.tabTerminal')">
                <div v-if="!toolEvents.length" class="tw-empty">
                    <t-icon name="terminal" size="32px" />
                    <span>{{ $t('agent.taskWorkspace.emptyTerminal') }}</span>
                </div>
                <ul v-else class="tw-tool-list">
                    <li
                        v-for="(ev, i) in toolEvents"
                        :key="`${ev.ts}-${i}`"
                        class="tw-tool-item"
                        :class="{ 'tw-tool-item--failed': ev.success === false }"
                    >
                        <div class="tw-tool-line">
                            <span class="tw-tool-status">
                                <t-icon v-if="ev.pending" name="loading" size="12px" />
                                <t-icon v-else-if="ev.success === false" name="close-circle" size="12px" />
                                <t-icon v-else name="check-circle" size="12px" />
                            </span>
                            <span class="tw-tool-name" :title="ev.tool_name">{{ ev.tool_name }}</span>
                            <span v-if="ev.duration_ms" class="tw-tool-duration">{{ formatDuration(ev.duration_ms) }}</span>
                        </div>
                        <pre v-if="ev.command" class="tw-tool-command">{{ ev.command }}</pre>
                        <pre v-if="ev.output" class="tw-tool-output">{{ ev.output }}</pre>
                    </li>
                </ul>
            </t-tab-panel>
        </t-tabs>
    </t-drawer>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import DocumentPreview from '@/components/document-preview.vue'
import { downloadArtifact, listSessionArtifacts, type ArtifactMeta } from '@/api/chat/index'
import { getFileIcon } from '@/utils/files'

type SessionArtifactMeta = ArtifactMeta & { message_id: string }

const props = defineProps<{
    sessionId?: string
    messages: unknown[]
    visible: boolean
}>()

const emit = defineEmits<{ (e: 'update:visible', v: boolean): void }>()

const { t } = useI18n()

const internalVisible = computed({
    get: () => props.visible,
    set: (v: boolean) => emit('update:visible', v),
})

const activeTab = ref<'artifacts' | 'terminal'>('artifacts')
const items = ref<SessionArtifactMeta[]>([])
const loading = ref(false)
const loadError = ref(false)
const downloading = reactive<Record<string, boolean>>({})
const previewItem = ref<SessionArtifactMeta | null>(null)

// The newest assistant message is still draining its sandbox output dir.
const collecting = computed(() => {
    const msgs = props.messages as Array<Record<string, unknown>>
    const last = [...msgs].reverse().find((m) => m.role === 'assistant')
    return !!last?.artifactsCollecting
})

// A new turn landing changes the message count — refetch while open so the
// 产物 tab tracks a running agent without extra SSE plumbing.
watch(
    () => [props.visible, props.sessionId, props.messages.length] as const,
    ([visible]) => {
        if (visible) fetchArtifacts()
    },
)

// Reset preview and list on session switch to prevent stale preview or 404
watch(
    () => props.sessionId,
    () => {
        items.value = []
        previewItem.value = null
    },
)

// Close preview when drawer closes
watch(
    () => props.visible,
    (v) => {
        if (!v) {
            previewItem.value = null
        }
    },
)

async function fetchArtifacts() {
    if (!props.sessionId) {
        items.value = []
        return
    }
    loading.value = true
    loadError.value = false
    try {
        const res = await listSessionArtifacts(props.sessionId)
        const data = (res as { data?: unknown })?.data
        items.value = Array.isArray(data) ? (data as SessionArtifactMeta[]) : []
    } catch (err) {
        console.error('[TaskWorkspaceDrawer] list artifacts failed:', err)
        loadError.value = true
    } finally {
        loading.value = false
    }
}

function openPreview(item: SessionArtifactMeta) {
    previewItem.value = item
}

function closePreview() {
    previewItem.value = null
}

async function handleDownload(item: SessionArtifactMeta) {
    if (!props.sessionId || !item.message_id) {
        MessagePlugin.error(t('agent.taskWorkspace.downloadFailed'))
        return
    }
    const key = `${item.message_id}-${item.index}`
    downloading[key] = true
    try {
        const blob = await downloadArtifact(props.sessionId, item.message_id, item.index)
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = item.file_name || 'artifact'
        document.body.appendChild(a)
        a.click()
        document.body.removeChild(a)
        setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (err) {
        console.error('[TaskWorkspaceDrawer] download failed:', err)
        MessagePlugin.error(t('agent.taskWorkspace.downloadFailed'))
    } finally {
        downloading[key] = false
    }
}

// Aggregated tool-call history. agentEventStream is a client-side array the
// stream handler maintains per assistant message (live turns and rehydrated
// history where the backend supplies it); events carrying a tool_name are
// tool executions with arguments/output metadata.
// ponytail: best-effort over in-memory streams — messages whose event stream
// was not persisted server-side simply contribute nothing.
const toolEvents = computed(() => {
    const out: Array<{
        tool_name: string
        command: string
        output: string
        success: boolean | undefined
        pending: boolean
        duration_ms: number | undefined
        ts: number
    }> = []
    for (const m of props.messages as Array<Record<string, any>>) {
        if (m?.role !== 'assistant' || !Array.isArray(m.agentEventStream)) continue
        for (const ev of m.agentEventStream as Array<Record<string, any>>) {
            if (!ev?.tool_name) continue
            const args = ev.arguments
            let command = ''
            if (typeof args?.command === 'string') {
                command = args.command
            } else if (args && typeof args === 'object') {
                try {
                    command = JSON.stringify(args)
                } catch {
                    command = ''
                }
            }
            const output = typeof ev.output === 'string' ? ev.output.slice(0, 2000) : ''
            out.push({
                tool_name: String(ev.tool_name),
                command: command.slice(0, 500),
                output,
                success: ev.success,
                pending: !!ev.pending,
                duration_ms: ev.duration_ms,
                ts: ev.timestamp ? new Date(ev.timestamp).getTime() : out.length,
            })
        }
    }
    return out
})

function formatFileSize(size: number): string {
    if (!size || size < 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB']
    let s = size
    let unit = 0
    while (s >= 1024 && unit < units.length - 1) {
        s /= 1024
        unit++
    }
    return unit === 0 ? `${s} ${units[unit]}` : `${s.toFixed(1)} ${units[unit]}`
}

function formatDateTime(raw: string): string {
    if (!raw) return '—'
    const d = new Date(raw)
    if (Number.isNaN(d.getTime())) return raw
    const pad = (n: number) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function formatDuration(ms: number): string {
    if (!ms || ms < 0) return ''
    if (ms < 1000) return `${Math.round(ms)}ms`
    if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
    return `${Math.floor(ms / 60000)}m${Math.round((ms % 60000) / 1000)}s`
}
</script>

<style scoped lang="less">
.tw-header {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    flex: 1;
}

.tw-header-icon {
    display: flex;
    align-items: center;
    color: var(--td-brand-color);
}

.tw-header-title {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-weight: 600;
}

.tw-tabs {
    height: 100%;

    :deep(.t-tabs__content) {
        height: calc(100% - 40px);
        overflow-y: auto;
    }
}

.tw-empty,
.tw-hint {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    padding: 48px 0;
    color: var(--td-text-color-placeholder);
}

.tw-hint {
    flex-direction: row;
    padding: 8px 0;
}

.tw-artifact-list {
    list-style: none;
    margin: 0;
    padding: 0;
}

.tw-artifact-item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 8px;
    border-radius: var(--td-radius-medium);
    cursor: pointer;
    transition: background-color 0.15s;

    &:hover {
        background-color: var(--td-bg-color-container-hover);
    }
}

.tw-artifact-icon {
    display: flex;
    align-items: center;
    color: var(--td-brand-color);
    flex-shrink: 0;
}

.tw-artifact-body {
    flex: 1;
    min-width: 0;
}

.tw-artifact-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.tw-artifact-meta {
    display: flex;
    gap: 12px;
    font-size: 12px;
    color: var(--td-text-color-placeholder);
}

.tw-preview-body {
    height: 100%;
}

.tw-tool-list {
    list-style: none;
    margin: 0;
    padding: 0;
    font-size: 12px;
}

.tw-tool-item {
    padding: 8px;
    border-bottom: 1px solid var(--td-component-border);
}

.tw-tool-item--failed .tw-tool-name {
    color: var(--td-error-color);
}

.tw-tool-line {
    display: flex;
    align-items: center;
    gap: 8px;
}

.tw-tool-status {
    display: flex;
    align-items: center;
    color: var(--td-success-color);
}

.tw-tool-item--failed .tw-tool-status {
    color: var(--td-error-color);
}

.tw-tool-name {
    font-family: var(--td-font-family-code);
    font-weight: 600;
}

.tw-tool-duration {
    color: var(--td-text-color-placeholder);
}

.tw-tool-command,
.tw-tool-output {
    margin: 6px 0 0;
    padding: 6px 8px;
    border-radius: var(--td-radius-small);
    background: var(--td-bg-color-page);
    font-family: var(--td-font-family-code);
    white-space: pre-wrap;
    word-break: break-all;
    max-height: 160px;
    overflow-y: auto;
}
</style>
