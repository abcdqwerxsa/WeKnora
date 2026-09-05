<template>
  <div class="wf-prop-form">
    <!-- ================= Start ================= -->
    <template v-if="kind === 'Start'">
      <p class="wf-prop-hint">{{ t('workflow.editor.startHint') }}</p>
    </template>

    <!-- ================= LLM ================= -->
    <template v-else-if="kind === 'LLM'">
      <t-form-item :label="t('workflow.editor.model')">
        <t-select
          :value="strParam('model')"
          :placeholder="t('workflow.editor.modelPlaceholder')"
          clearable
          filterable
          @change="setParam('model', $event)"
        >
          <t-option v-for="m in chatModels" :key="m.id" :value="m.id" :label="modelLabel(m.name)" />
        </t-select>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.systemPrompt')">
        <div class="wf-prop-field">
          <t-textarea
            :value="strParam('system_prompt')"
            :autosize="{ minRows: 2, maxRows: 8 }"
            :placeholder="t('workflow.editor.promptHint')"
            @focus="rememberCaret('system_prompt', $event)"
            @click="rememberCaret('system_prompt', $event)"
            @change="setParam('system_prompt', $event)"
          />
          <VariableRefPicker
            :current-node-id="currentNodeId"
            :nodes="nodes"
            :edges="edges"
            @insert="insertRef('system_prompt', $event)"
          />
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.prompt')">
        <div class="wf-prop-field">
          <t-textarea
            :value="strParam('prompt')"
            :autosize="{ minRows: 3, maxRows: 10 }"
            :placeholder="t('workflow.editor.promptHint')"
            @focus="rememberCaret('prompt', $event)"
            @click="rememberCaret('prompt', $event)"
            @change="setParam('prompt', $event)"
          />
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" @insert="insertRef('prompt', $event)" />
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.temperature')">
        <t-slider :value="numParam('temperature', 0.7)" :min="0" :max="2" :step="0.1" @change="setParam('temperature', $event)" />
      </t-form-item>
      <t-form-item :label="t('workflow.editor.maxTokens')">
        <t-input-number
          :value="numParam('max_tokens', 0)"
          :min="0"
          :max="32768"
          :step="256"
          theme="column"
          :placeholder="t('workflow.editor.maxTokensHint')"
          @change="setParam('max_tokens', $event)"
        />
      </t-form-item>
    </template>

    <!-- ================= Retrieval ================= -->
    <template v-else-if="kind === 'Retrieval'">
      <t-form-item :label="t('workflow.editor.kbSelect')">
        <t-select
          :value="kbIds"
          :placeholder="t('workflow.editor.kbSelectHint')"
          multiple
          clearable
          filterable
          @change="setParam('kb_ids', $event)"
        >
          <t-option v-for="kb in kbOptions" :key="kb.id" :value="kb.id" :label="kb.name" />
        </t-select>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.queryTemplate')">
        <div class="wf-prop-field">
          <t-textarea
            :value="strParam('query')"
            :autosize="{ minRows: 2, maxRows: 6 }"
            :placeholder="t('workflow.editor.promptHint')"
            @focus="rememberCaret('query', $event)"
            @click="rememberCaret('query', $event)"
            @change="setParam('query', $event)"
          />
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" @insert="insertRef('query', $event)" />
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.topK')">
        <t-input-number :value="numParam('top_k', 10)" :min="1" :max="50" theme="column" @change="setParam('top_k', $event)" />
      </t-form-item>
      <t-form-item :label="t('workflow.editor.similarityThreshold')">
        <t-input-number
          :value="numParam('similarity_threshold', 0)"
          :min="0"
          :max="1"
          :step="0.05"
          theme="column"
          :placeholder="t('workflow.editor.thresholdHint')"
          @change="setParam('similarity_threshold', $event)"
        />
      </t-form-item>
      <t-form-item :label="t('workflow.editor.vectorThreshold')">
        <t-input-number
          :value="numParam('vector_threshold', 0)"
          :min="0"
          :max="1"
          :step="0.05"
          theme="column"
          :placeholder="t('workflow.editor.thresholdHint')"
          @change="setParam('vector_threshold', $event)"
        />
      </t-form-item>
      <t-form-item :label="t('workflow.editor.keywordThreshold')">
        <t-input-number
          :value="numParam('keyword_threshold', 0)"
          :min="0"
          :max="1"
          :step="0.05"
          theme="column"
          :placeholder="t('workflow.editor.thresholdHint')"
          @change="setParam('keyword_threshold', $event)"
        />
      </t-form-item>
      <t-form-item :label="t('workflow.editor.useRerank')">
        <t-switch :value="boolParam('use_rerank')" @change="setParam('use_rerank', $event)" />
      </t-form-item>
      <t-form-item v-if="boolParam('use_rerank')" :label="t('workflow.editor.rerankModel')">
        <t-select
          :value="strParam('rerank_model_id')"
          :placeholder="t('workflow.editor.rerankModelHint')"
          clearable
          filterable
          @change="setParam('rerank_model_id', $event)"
        >
          <t-option v-for="m in rerankModels" :key="m.id" :value="m.id" :label="modelLabel(m.name)" />
        </t-select>
      </t-form-item>
    </template>

    <!-- ================= Switch ================= -->
    <template v-else-if="kind === 'Switch'">
      <t-form-item :label="t('workflow.editor.switchValue')">
        <div class="wf-prop-field">
          <t-input
            :value="strParam('value')"
            :placeholder="t('workflow.editor.switchValueHint')"
            @focus="rememberCaret('value', $event)"
            @click="rememberCaret('value', $event)"
            @change="setParam('value', $event)"
          />
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" @insert="insertRef('value', $event)" />
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.cases')">
        <div class="wf-prop-rows">
          <div v-for="(item, index) in switchCases" :key="index" class="wf-prop-row">
            <t-input v-model="item.value" :placeholder="t('workflow.editor.caseValue')" />
            <t-select v-model="item.to" :placeholder="t('workflow.editor.caseTarget')" clearable>
              <t-option v-for="option in nodeOptions" :key="option.value" :value="option.value" :label="option.label" />
            </t-select>
            <t-button variant="text" theme="danger" size="small" @click="removeAt('cases', index)">
              <template #icon><t-icon name="delete" /></template>
            </t-button>
          </div>
          <t-button variant="dashed" size="small" block @click="switchCases.push({ value: '', to: '' })">
            {{ t('workflow.editor.addCase') }}
          </t-button>
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.defaultBranch')">
        <t-select
          :value="strParam('default')"
          :placeholder="t('workflow.editor.caseTarget')"
          clearable
          @change="setParam('default', $event)"
        >
          <t-option v-for="option in nodeOptions" :key="option.value" :value="option.value" :label="option.label" />
        </t-select>
      </t-form-item>
    </template>

    <!-- ================= Answer ================= -->
    <template v-else-if="kind === 'Answer'">
      <t-form-item :label="t('workflow.editor.template')">
        <div class="wf-prop-field">
          <t-textarea
            :value="strParam('template')"
            :autosize="{ minRows: 4, maxRows: 10 }"
            :placeholder="t('workflow.editor.promptHint')"
            @focus="rememberCaret('template', $event)"
            @click="rememberCaret('template', $event)"
            @change="setParam('template', $event)"
          />
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" @insert="insertRef('template', $event)" />
        </div>
      </t-form-item>
    </template>

    <!-- ================= Template (string transform) ================= -->
    <template v-else-if="kind === 'Template'">
      <t-form-item :label="t('workflow.editor.template')">
        <div class="wf-prop-field">
          <t-textarea
            :value="strParam('template')"
            :autosize="{ minRows: 3, maxRows: 8 }"
            :placeholder="t('workflow.editor.promptHint')"
            @focus="rememberCaret('template', $event)"
            @click="rememberCaret('template', $event)"
            @change="setParam('template', $event)"
          />
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" @insert="insertRef('template', $event)" />
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.ops')">
        <div class="wf-prop-rows">
          <div v-for="(op, index) in templateOps" :key="index" class="wf-prop-op">
            <t-select v-model="op.op" :placeholder="t('workflow.editor.opName')" class="wf-prop-op-type">
              <t-option value="upper" :label="t('workflow.editor.opUpper')" />
              <t-option value="lower" :label="t('workflow.editor.opLower')" />
              <t-option value="trim" :label="t('workflow.editor.opTrim')" />
              <t-option value="replace" :label="t('workflow.editor.opReplace')" />
              <t-option value="regex_extract" :label="t('workflow.editor.opRegex')" />
            </t-select>
            <template v-if="op.op === 'replace'">
              <t-input v-model="op.from" :placeholder="t('workflow.editor.opFrom')" />
              <t-input v-model="op.to" :placeholder="t('workflow.editor.opTo')" />
            </template>
            <template v-else-if="op.op === 'regex_extract'">
              <t-input v-model="op.pattern" :placeholder="t('workflow.editor.opPattern')" />
              <t-input-number v-model="op.group" :min="0" :max="9" theme="column" :placeholder="t('workflow.editor.opGroup')" />
            </template>
            <t-button variant="text" theme="danger" size="small" @click="templateOps.splice(index, 1)">
              <template #icon><t-icon name="delete" /></template>
            </t-button>
          </div>
          <t-button
            variant="dashed"
            size="small"
            block
            @click="templateOps.push({ op: 'upper' as const, from: '', to: '', pattern: '', group: 1 })"
          >
            {{ t('workflow.editor.addOp') }}
          </t-button>
        </div>
      </t-form-item>
    </template>

    <!-- ================= VariableAggregator ================= -->
    <template v-else-if="kind === 'VariableAggregator'">
      <t-form-item :label="t('workflow.editor.variables')">
        <div class="wf-prop-rows">
          <div v-for="(item, index) in varList" :key="index" class="wf-prop-row">
            <t-input v-model="item.name" :placeholder="t('workflow.editor.varName')" class="wf-prop-var-name" />
            <t-input v-model="item.ref" :placeholder="t('workflow.editor.varRef')" readonly />
            <VariableRefPicker
              :current-node-id="currentNodeId"
              :nodes="nodes"
              :edges="edges"
              @insert="(ref: string) => (item.ref = ref)"
            />
            <t-button variant="text" theme="danger" size="small" @click="varList.splice(index, 1)">
              <template #icon><t-icon name="delete" /></template>
            </t-button>
          </div>
          <t-button variant="dashed" size="small" block @click="varList.push({ name: '', ref: '' })">
            {{ t('workflow.editor.addVar') }}
          </t-button>
        </div>
      </t-form-item>
    </template>

    <!-- ================= HTTP ================= -->
    <template v-else-if="kind === 'HTTP'">
      <t-form-item :label="t('workflow.editor.method')">
        <t-select :value="strParam('method') || 'GET'" @change="setParam('method', $event)">
          <t-option v-for="m in ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']" :key="m" :value="m" :label="m" />
        </t-select>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.url')">
        <div class="wf-prop-field">
          <t-input
            :value="strParam('url')"
            placeholder="http://intranet-service/api"
            @focus="rememberCaret('url', $event)"
            @click="rememberCaret('url', $event)"
            @change="setParam('url', $event)"
          />
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" @insert="insertRef('url', $event)" />
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.headers')">
        <div class="wf-prop-rows">
          <div v-for="(row, index) in headerRows" :key="index" class="wf-prop-row">
            <t-input v-model="row.key" :placeholder="t('workflow.editor.headerKey')" />
            <t-input v-model="row.value" :placeholder="t('workflow.editor.headerValue')" />
            <t-button variant="text" theme="danger" size="small" @click="headerRows.splice(index, 1)">
              <template #icon><t-icon name="delete" /></template>
            </t-button>
          </div>
          <t-button variant="dashed" size="small" block @click="headerRows.push({ key: '', value: '' })">
            {{ t('workflow.editor.addHeader') }}
          </t-button>
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.bodyTemplate')">
        <div class="wf-prop-field">
          <t-textarea
            :value="strParam('body_template')"
            :autosize="{ minRows: 3, maxRows: 8 }"
            placeholder="{&quot;query&quot;: &quot;{start@query}&quot;}"
            @focus="rememberCaret('body_template', $event)"
            @click="rememberCaret('body_template', $event)"
            @change="setParam('body_template', $event)"
          />
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" @insert="insertRef('body_template', $event)" />
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.timeout')">
        <t-input-number
          :value="numParam('timeout_seconds', 30)"
          :min="1"
          :max="300"
          theme="column"
          @change="setParam('timeout_seconds', $event)"
        />
      </t-form-item>
      <p class="wf-prop-hint">{{ t('workflow.editor.httpIntranetHint') }}</p>
    </template>

    <!-- ================= DataOps ================= -->
    <template v-else-if="kind === 'DataOps'">
      <t-form-item :label="t('workflow.editor.sql')">
        <t-textarea
          :value="strParam('sql')"
          :autosize="{ minRows: 3, maxRows: 10 }"
          :placeholder="t('workflow.editor.sqlPlaceholder')"
          class="wf-prop-sql"
          @change="setParam('sql', $event)"
        />
      </t-form-item>
      <t-form-item :label="t('workflow.editor.variables')">
        <div class="wf-prop-rows">
          <div v-for="(item, index) in varList" :key="index" class="wf-prop-row">
            <t-input v-model="item.name" :placeholder="t('workflow.editor.varName')" class="wf-prop-var-name" />
            <t-input v-model="item.ref" :placeholder="t('workflow.editor.varRef')" readonly />
            <VariableRefPicker
              :current-node-id="currentNodeId"
              :nodes="nodes"
              :edges="edges"
              @insert="(ref: string) => (item.ref = ref)"
            />
            <t-button variant="text" theme="danger" size="small" @click="varList.splice(index, 1)">
              <template #icon><t-icon name="delete" /></template>
            </t-button>
          </div>
          <t-button variant="dashed" size="small" block @click="varList.push({ name: '', ref: '' })">
            {{ t('workflow.editor.addVar') }}
          </t-button>
        </div>
      </t-form-item>
      <p class="wf-prop-hint">{{ t('workflow.editor.dataOpsHint') }}</p>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Edge } from '@vue-flow/core'
import type { ModelConfig } from '@/api/model'
import type { TemplateOp, WorkflowNodeType } from '@/api/workflow'
import VariableRefPicker from './VariableRefPicker.vue'

/**
 * Typed property form for one canvas node. Mutates `params` in place —
 * the object is the reactive node.data.params owned by the editor, so
 * canvas + DSL stay in sync without an event round-trip.
 */
const props = defineProps<{
  kind: WorkflowNodeType
  currentNodeId: string
  params: Record<string, unknown>
  nodes: Array<{ id: string; kind: WorkflowNodeType; params?: Record<string, unknown> }>
  edges: Edge[]
  chatModels: ModelConfig[]
  rerankModels: ModelConfig[]
  kbs: Array<{ id: string; name: string }>
}>()

const { t } = useI18n()

// ---- param accessors ----------------------------------------------------

function strParam(key: string): string {
  const value = props.params[key]
  return typeof value === 'string' ? value : ''
}

function numParam(key: string, fallback: number): number {
  const value = props.params[key]
  if (typeof value === 'number' && Number.isFinite(value)) return value
  return fallback
}

function boolParam(key: string): boolean {
  return props.params[key] === true
}

function setParam(key: string, value: unknown) {
  props.params[key] = value
}

// Caret tracking for {ref} insertion: remember the last caret position per
// field (updated on focus/click); insert there, else append at the end.
const carets = new Map<string, number>()

function rememberCaret(key: string, event: Event) {
  const target = event.target as HTMLTextAreaElement | HTMLInputElement
  carets.set(key, target.selectionStart ?? target.value.length)
}

function insertRef(key: string, reference: string) {
  const current = strParam(key)
  const caret = carets.get(key) ?? current.length
  const next = current.slice(0, caret) + reference + current.slice(caret)
  setParam(key, next)
  carets.set(key, caret + reference.length)
}

// ---- list editors (reactive views over params arrays) --------------------

const switchCases = computed<Array<{ value: string; to: string }>>({
  get: () => (Array.isArray(props.params.cases) ? (props.params.cases as Array<{ value: string; to: string }>) : []),
  set: (value) => setParam('cases', value),
})

const templateOps = computed<Array<TemplateOp & Record<string, unknown>>>({
  get: () => (Array.isArray(props.params.ops) ? (props.params.ops as Array<TemplateOp & Record<string, unknown>>) : []),
  set: (value) => setParam('ops', value),
})

const varList = computed<Array<{ name: string; ref: string }>>({
  get: () =>
    Array.isArray(props.params.variables) ? (props.params.variables as Array<{ name: string; ref: string }>) : [],
  set: (value) => setParam('variables', value),
})

function removeAt(key: string, index: number) {
  const list = Array.isArray(props.params[key]) ? [...(props.params[key] as unknown[])] : []
  list.splice(index, 1)
  setParam(key, list)
}

// Headers: object in the DSL, rows in the form. Write-through on mutation.
const headerRows = ref<Array<{ key: string; value: string }>>([])
watch(
  () => props.params.headers,
  (headers) => {
    headerRows.value = Object.entries((headers as Record<string, string>) ?? {}).map(([key, value]) => ({
      key,
      value: String(value ?? ''),
    }))
  },
  { immediate: true },
)
watch(
  headerRows,
  (rows) => {
    const headers: Record<string, string> = {}
    for (const row of rows) {
      const key = row.key.trim()
      if (key) headers[key] = row.value
    }
    setParam('headers', headers)
  },
  { deep: true },
)

// ---- pickers context -----------------------------------------------------

const kbIds = computed<string[]>(() => (Array.isArray(props.params.kb_ids) ? (props.params.kb_ids as string[]) : []))

/** KB options: live KBs plus synthetic entries for ids that no longer exist. */
const kbOptions = computed(() => {
  const known = new Map(props.kbs.map((kb) => [kb.id, kb.name]))
  const options = [...props.kbs]
  for (const id of kbIds.value) {
    if (!known.has(id)) options.push({ id, name: `${id} (missing)` })
  }
  return options
})

const nodeOptions = computed(() =>
  props.nodes
    .filter((node) => node.id !== props.currentNodeId)
    .map((node) => ({ value: node.id, label: `${t(`workflow.nodes.${node.kind}`)} · ${node.id}` })),
)

function modelLabel(name: string): string {
  return name || '—'
}
</script>

<style scoped>
.wf-prop-form {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.wf-prop-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
}

.wf-prop-rows {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.wf-prop-row {
  display: flex;
  align-items: center;
  gap: 4px;
}

.wf-prop-row .t-input,
.wf-prop-row .t-select {
  min-width: 0;
  flex: 1;
}

.wf-prop-var-name {
  flex: 0 0 88px !important;
}

.wf-prop-op {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}

.wf-prop-op .t-select,
.wf-prop-op .t-input {
  min-width: 0;
}

.wf-prop-op-type {
  flex: 0 0 110px !important;
}

.wf-prop-hint {
  margin: 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.wf-prop-sql :deep(textarea) {
  font-family: var(--td-font-family-code, monospace);
}
</style>
