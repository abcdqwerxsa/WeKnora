<template>
  <aside class="kb-folder-tree" :class="{ 'is-collapsed': collapsed }">
    <div class="kb-folder-tree__header">
      <template v-if="!collapsed">
        <span class="kb-folder-tree__title">{{ t('knowledgeBase.folderTree.title') }}</span>
        <t-tooltip :content="t('knowledgeBase.folderTree.collapse')" placement="top">
          <button
            type="button"
            class="kb-folder-tree__icon-btn"
            :aria-label="t('knowledgeBase.folderTree.collapse')"
            @click="emit('update:collapsed', true)"
          >
            <t-icon name="chevron-left-double" size="15px" />
          </button>
        </t-tooltip>
      </template>
      <t-tooltip v-else :content="t('knowledgeBase.folderTree.expand')" placement="right">
        <button
          type="button"
          class="kb-folder-tree__icon-btn"
          :aria-label="t('knowledgeBase.folderTree.expand')"
          @click="emit('update:collapsed', false)"
        >
          <t-icon name="chevron-right-double" size="15px" />
        </button>
      </t-tooltip>
    </div>

    <div v-if="!collapsed" class="kb-folder-tree__body">
      <template v-if="loading && !tree">
        <div v-for="n in 5" :key="'folder-skel-' + n" class="kb-folder-tree__skeleton">
          <t-skeleton animation="gradient" :row-col="[{ width: '100%', height: '16px' }]" />
        </div>
      </template>
      <template v-else>
        <template v-for="row in rows" :key="row.kind === 'separator' ? row.id : (row.kind === 'file' ? `file-${row.file.id}` : (row.path || '__root__'))">
          <div
            v-if="row.kind === 'separator'"
            class="kb-folder-separator"
            :style="{ '--kb-folder-depth': row.depth }"
            role="separator"
          >
            <span class="kb-folder-separator__line" aria-hidden="true" />
            <span class="kb-folder-separator__label">{{ row.label }}</span>
            <span class="kb-folder-separator__line" aria-hidden="true" />
          </div>
          <div
            v-else
            class="kb-folder-row"
            :class="{
              active: row.kind === 'folder' && selectedPath === row.path,
              'is-root': row.kind === 'root',
              'is-editable': canEdit && row.kind === 'folder',
              'is-menu-open': row.kind === 'folder' && menuOpenPath === row.path,
              'is-file': row.kind === 'file',
            }"
            :style="{ '--kb-folder-depth': row.depth }"
            :title="row.kind === 'root' ? t('knowledgeBase.folderTree.rootRowTip') : row.path"
            role="button"
            tabindex="0"
            @click="onRowActivate(row)"
            @keydown.enter="onRowActivate(row)"
          >
          <span
            v-if="row.hasChildren"
            class="kb-folder-row__toggle"
            role="button"
            :aria-label="t(isExpanded(row.path)
              ? 'knowledgeBase.folderTree.collapseFolder'
              : 'knowledgeBase.folderTree.expandFolder')"
            @click.stop="toggle(row.path)"
          >
            <t-icon :name="isExpanded(row.path) ? 'chevron-down' : 'chevron-right'" />
          </span>
          <span v-else class="kb-folder-row__toggle-placeholder" aria-hidden="true" />

          <t-icon
            :name="row.kind === 'root' || (row.hasChildren && isExpanded(row.path)) ? 'folder-open' : 'folder'"
            class="kb-folder-row__icon"
          />

          <input
            v-if="isRenaming(row)"
            ref="renameInputRef"
            v-model="renameValue"
            class="kb-folder-row__rename"
            :placeholder="t('knowledgeBase.folderTree.renamePlaceholder')"
            @click.stop
            @keydown.enter="commitRename(row)"
            @keydown.esc="cancelRename"
            @blur="commitRename(row)"
          />
          <template v-else-if="row.kind === 'folder'">
            <span class="kb-folder-row__label">
              {{ row.kind === 'root' ? t('knowledgeBase.folderTree.rootRow') : row.name }}
            </span>
            <span class="kb-folder-row__trailing">
              <span class="kb-folder-row__count">{{ row.totalCount }}</span>
              <t-popup
                v-if="canEdit && row.kind === 'folder'"
                :visible="menuOpenPath === row.path"
                trigger="click"
                placement="bottom-right"
                destroy-on-close
                overlay-class-name="card-more-popup"
                @visible-change="(visible: boolean) => onFolderMenuVisible(row.path, visible)"
              >
                <button
                  type="button"
                  class="kb-folder-row__more"
                  :class="{ 'is-open': menuOpenPath === row.path }"
                  :aria-label="t('knowledgeBase.moreOptions')"
                  @click.stop
                >
                  <t-icon name="more" />
                </button>
                <template #content>
                  <div class="popup-menu kb-folder-row__menu" @click.stop>
                    <div class="popup-menu-item" @click="onFolderMenuRename(row)">
                      <t-icon name="edit" class="menu-icon" />
                      <span>{{ t('knowledgeBase.folderTree.rename') }}</span>
                    </div>
                  </div>
                </template>
              </t-popup>
            </span>
          </template>
          <template v-else-if="row.kind === 'file'">
            <span class="kb-folder-row__file-icon" aria-hidden="true">
              <t-icon :name="getFileIcon(row.file)" />
            </span>
            <span class="kb-folder-row__file-name" :title="row.file.file_name">
              {{ row.file.file_name }}
            </span>
            <span
              v-if="row.file.parse_status && row.file.parse_status !== 'success' && row.file.parse_status !== 'completed'"
              class="kb-folder-row__file-status"
              :class="`is-${row.file.parse_status}`"
              :title="row.file.parse_status"
            />
          </template>
          </div>
        </template>
      </template>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { KnowledgeFolderTree } from '@/api/knowledge-base/index'
import {
  buildFolderRows,
  folderAncestorPaths,
  joinFolderPath,
  ROOT_FOLDER_PATH,
  type FolderRow,
} from '../folderTree'
import { getFileIcon } from '@/utils/files'

type SidebarFile = {
  id: string
  file_name?: string
  file_type?: string
  parse_status?: string
  folder_path?: string
}

type FileRow = {
  kind: 'file'
  file: SidebarFile
  depth: number
}

// Separator header between sub-folders and files of the selected folder.
// It is part of the row stream so it lines up with the existing v-for, but
// it carries no path / id of its own and renders as a "── 文件 ──" line
// instead of a clickable folder row. Files are now a separate section from
// the collapsible tree, so collapsing a folder no longer hides them.
type SeparatorRow = {
  kind: 'separator'
  id: string
  label: string
  depth: number
}

type AnyRow = FolderRow | FileRow | SeparatorRow

const props = withDefaults(defineProps<{
  tree: KnowledgeFolderTree | null
  /** Selected folder path; the empty string is the knowledge base top level. */
  selectedPath: string
  /** Documents stored directly under the selected folder. They render under
   *  the selected folder row, after its sub-folders, so the sidebar mirrors
   *  the main list — clickable to open the document drawer. */
  files?: SidebarFile[]
  loading?: boolean
  collapsed?: boolean
  canEdit?: boolean
}>(), {
  files: () => [],
  loading: false,
  collapsed: false,
  canEdit: false,
})

const emit = defineEmits<{
  select: [path: string]
  'update:collapsed': [collapsed: boolean]
  rename: [payload: { from: string; to: string }]
  'open-file': [item: SidebarFile]
}>()

const { t } = useI18n()

// The root starts expanded so the uploaded structure is visible without a click.
const expanded = ref(new Set<string>([ROOT_FOLDER_PATH]))
// null, not '', because '' is the root's own path: a falsy sentinel would put
// the root row into rename mode permanently.
const renamingPath = ref<string | null>(null)
const menuOpenPath = ref<string | null>(null)
const renameValue = ref('')
const renameInputRef = ref<HTMLInputElement | HTMLInputElement[] | null>(null)

const rows = computed<AnyRow[]>(() => {
  const fr = buildFolderRows(props.tree, expanded.value)
  // Files render as a separate "files" section under the selected folder,
  // NOT as children inside the collapsible tree. The chevron now controls
  // sub-folders only — collapsing the selected folder leaves the file list
  // visible, because that list is "what's in this folder" (working context)
  // rather than "what's inside this tree node".
  if (!props.files?.length) return fr
  // The selected folder has to be visible in the tree to anchor the files;
  // when an ancestor of the selected folder is collapsed, the folder row
  // itself is hidden and the files would float in space. Drop the files in
  // that case — the user explicitly hid the branch.
  const selIdx = fr.findIndex(
    (r) => (r.kind === 'folder' || r.kind === 'root') && r.path === props.selectedPath,
  )
  if (selIdx < 0) return fr
  const selRow = fr[selIdx]
  // Splice after the selected folder's own sub-folders so the order is:
  // folder → its sub-folders → "── 文件 ──" → its files → siblings.
  let ins = selIdx + 1
  while (ins < fr.length && fr[ins].depth > selRow.depth) ins++
  const separator: SeparatorRow = {
    kind: 'separator',
    id: `files-${props.selectedPath}`,
    label: t('knowledgeBase.folderTree.filesHeader', { count: props.files.length }),
    depth: selRow.depth,
  }
  const fileRows: FileRow[] = props.files.map((f) => ({
    kind: 'file',
    file: f,
    depth: selRow.depth + 1,
  }))
  return [...fr.slice(0, ins), separator, ...fileRows, ...fr.slice(ins)]
})

const isExpanded = (path: string) => expanded.value.has(path)

// The root has no name of its own to edit, and excluding it here means no
// sentinel value can ever put it into rename mode.
const isRenaming = (row: FolderRow) => row.kind === 'folder' && renamingPath.value === row.path

const toggle = (path: string) => {
  const next = new Set(expanded.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  expanded.value = next
}

// Clicking a folder row both selects it and expands it, so the files inside
// become visible at the same time. The chevron is the only way to collapse:
// clicking it hides sub-folders and the files together, matching how file
// managers behave. Files route straight to the document drawer.
const onRowActivate = (row: AnyRow) => {
  if (row.kind === 'file') {
    emit('open-file', row.file)
    return
  }
  if (!expanded.value.has(row.path)) toggle(row.path)
  emit('select', row.path)
}

const startRename = async (row: FolderRow) => {
  renamingPath.value = row.path
  renameValue.value = row.name
  await nextTick()
  const input = Array.isArray(renameInputRef.value) ? renameInputRef.value[0] : renameInputRef.value
  input?.focus()
  input?.select()
}

const onFolderMenuVisible = (path: string, visible: boolean) => {
  menuOpenPath.value = visible ? path : null
}

const onFolderMenuRename = async (row: FolderRow) => {
  menuOpenPath.value = null
  await startRename(row)
}

const cancelRename = () => {
  renamingPath.value = null
  renameValue.value = ''
}

const commitRename = (row: FolderRow) => {
  if (!isRenaming(row)) return
  const name = renameValue.value.trim()
  cancelRename()
  // Only the last segment is edited here; the folder keeps its place in the tree.
  if (!name || name === row.name) return
  const parent = row.path.slice(0, Math.max(0, row.path.length - row.name.length - 1))
  emit('rename', { from: row.path, to: joinFolderPath(parent, name) })
}

// Keep the selected folder reachable: expand the root and every folder above
// the active path, both on first load and when the selection changes from
// elsewhere (e.g. opening a folder from the document list).
watch(
  () => [props.selectedPath, props.tree] as const,
  () => {
    const next = new Set(expanded.value)
    folderAncestorPaths(props.selectedPath).forEach((path) => next.add(path))
    expanded.value = next
  },
  { immediate: true },
)

// First load also opens the top-level folders, so a two-level upload is visible
// in full without any expanding.
watch(
  () => props.tree,
  (tree) => {
    if (!tree?.folders?.length || expanded.value.size > 1) return
    const next = new Set(expanded.value)
    tree.folders.forEach((folder) => next.add(folder.path))
    expanded.value = next
  },
  { immediate: true },
)
</script>

<style scoped lang="less">
.kb-folder-tree {
  --kb-folder-indent: 10px;
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  width: 268px;
  min-height: 0;
  padding-right: 12px;
  margin-right: 12px;
  border-right: 1px solid var(--td-component-stroke);
  box-sizing: border-box;

  &.is-collapsed {
    width: auto;
    padding-right: 8px;
    margin-right: 8px;
  }
}

.kb-folder-tree__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  height: 32px;
  flex-shrink: 0;
}

.kb-folder-tree__title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.kb-folder-tree__icon-btn {
  width: 24px;
  height: 24px;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-bg-color-container-hover);
  }
}

.kb-folder-tree__body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 4px 0 12px;
  scrollbar-width: thin;

  &::-webkit-scrollbar {
    width: 4px;
  }

  &::-webkit-scrollbar-thumb {
    border-radius: 2px;
    background: var(--td-scrollbar-color);
  }
}

.kb-folder-tree__skeleton {
  padding: 7px 8px;
}

.kb-folder-row {
  display: flex;
  align-items: center;
  gap: 4px;
  box-sizing: border-box;
  width: 100%;
  height: 30px;
  padding: 0 8px 0 calc(var(--kb-folder-depth, 0) * var(--kb-folder-indent));
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 13px;
  text-align: left;
  cursor: pointer;
  user-select: none;
  transition: background 0.15s ease, color 0.15s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-brand-color) 30%, transparent);
  }

  &.active {
    background: var(--td-bg-color-container-hover);

    .kb-folder-row__label,
    .kb-folder-row__icon {
      color: var(--td-brand-color);
    }
  }

  &.is-root .kb-folder-row__label {
    font-weight: 500;
  }
}

.kb-folder-row__toggle,
.kb-folder-row__toggle-placeholder {
  flex: 0 0 auto;
  width: 16px;
  height: 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-placeholder);
  border-radius: 4px;
}

.kb-folder-row__toggle {
  cursor: pointer;

  &:hover {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-secondarycontainer);
  }

  .t-icon {
    font-size: 14px;
  }
}

.kb-folder-row__icon {
  flex: 0 0 auto;
  font-size: 15px;
  color: var(--td-text-color-placeholder);
}

.kb-folder-row__label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.kb-folder-row__trailing {
  flex: 0 0 auto;
  width: 22px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-left: 2px;
}

.kb-folder-row__count {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
  line-height: 1;
}

.kb-folder-row__more {
  display: none;
  width: 20px;
  height: 20px;
  padding: 0;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: color 0.15s ease, background-color 0.15s ease;

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-bg-color-secondarycontainer);
  }

  .t-icon {
    font-size: 14px;
  }
}

.kb-folder-row.is-editable:hover,
.kb-folder-row.is-menu-open {
  .kb-folder-row__count {
    display: none;
  }

  .kb-folder-row__more {
    display: inline-flex;
  }
}

.kb-folder-row__more.is-open {
  color: var(--td-brand-color);
}

.kb-folder-row__menu {
  min-width: 140px;
}

.kb-folder-row__rename {
  flex: 1;
  min-width: 0;
  height: 22px;
  padding: 0 6px;
  border: 1px solid var(--td-brand-color);
  border-radius: 4px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 13px;
  outline: none;
}
.kb-folder-row__file-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  margin-right: 6px;
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
  font-size: 14px;
}
.kb-folder-row__file-name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  color: var(--td-text-color-primary);
}
.kb-folder-row__file-status {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--td-success-color);
  margin-left: 6px;
}
.kb-folder-row__file-status.is-failed,
.kb-folder-row__file-status.is-error {
  background: var(--td-error-color);
}
.kb-folder-row__file-status.is-parsing,
.kb-folder-row__file-status.is-processing,
.kb-folder-row__file-status.is-pending {
  background: var(--td-brand-color);
}
.kb-folder-row__file-status.is-draft {
  background: var(--td-warning-color);
}
.kb-folder-row.is-file {
  cursor: pointer;
}
.kb-folder-row.is-file:hover {
  background: var(--td-bg-color-container-hover);
}

.kb-folder-separator {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 22px;
  padding: 0 calc(8px + var(--kb-folder-depth, 0) * var(--kb-folder-indent));
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.04em;
  color: var(--td-text-color-placeholder);
  user-select: none;
}

.kb-folder-separator__line {
  flex: 1;
  height: 1px;
  background: var(--td-component-stroke);
  min-width: 0;
}

.kb-folder-separator__label {
  flex: 0 0 auto;
  text-transform: uppercase;
}
</style>
