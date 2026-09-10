<template>
  <div class="wf-prop-form">
    <!-- ================= Start ================= -->
    <template v-if="kind === 'Start'">
      <p class="wf-prop-hint">{{ t('workflow.editor.startHint') }}</p>
      <t-form-item :label="t('workflow.editor.formFields')">
        <div class="wf-prop-rows">
          <div v-for="(field, index) in startFields" :key="index" class="wf-prop-case">
            <div class="wf-prop-row">
              <t-input v-model="field.name" :placeholder="t('workflow.editor.fieldName')" />
              <t-select v-model="field.type" class="wf-prop-field-type">
                <t-option value="text" :label="t('workflow.editor.fieldText')" />
                <t-option value="paragraph" :label="t('workflow.editor.fieldParagraph')" />
                <t-option value="number" :label="t('workflow.editor.fieldNumber')" />
                <t-option value="select" :label="t('workflow.editor.fieldSelect')" />
              </t-select>
              <t-checkbox v-model="field.required">{{ t('workflow.editor.fieldRequired') }}</t-checkbox>
              <t-button variant="text" theme="danger" size="small" @click="startFields.splice(index, 1)">
                <template #icon><t-icon name="delete" /></template>
              </t-button>
            </div>
            <div class="wf-prop-row">
              <t-input v-model="field.label" :placeholder="t('workflow.editor.fieldLabel')" />
              <t-input v-model="field.default" :placeholder="t('workflow.editor.fieldDefault')" />
            </div>
            <div v-if="field.type === 'select'" class="wf-prop-row">
              <t-input
                :value="(field.options ?? []).join(', ')"
                :placeholder="t('workflow.editor.fieldOptions')"
                @change="field.options = String($event).split(',').map((s) => s.trim()).filter(Boolean)"
              />
            </div>
          </div>
          <t-button variant="dashed" size="small" block @click="addField()">
            {{ t('workflow.editor.addField') }}
          </t-button>
        </div>
      </t-form-item>
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
          <RefTextarea
          :model-value="strParam('system_prompt')"
            :autosize="{ minRows: 2, maxRows: 8 }"
            :placeholder="t('workflow.editor.promptHint')"
          :suggestions="refSuggestions"
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
          <RefTextarea
          :model-value="strParam('prompt')"
            :autosize="{ minRows: 3, maxRows: 10 }"
            :placeholder="t('workflow.editor.promptHint')"
          :suggestions="refSuggestions"
          @change="setParam('prompt', $event)"
        />
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" :env-names="envNames" @insert="insertRef('prompt', $event)" />
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.temperature')">
        <t-slider :value="numParam('temperature', 0.7)" :min="0" :max="2" :step="0.1" @change="setParam('temperature', $event)" />
      </t-form-item>
      <t-form-item :label="t('workflow.editor.thinking')">
        <t-select :value="thinkingValue" @change="setThinking">
          <t-option value="" :label="t('workflow.editor.thinkingDefault')" />
          <t-option value="on" :label="t('workflow.editor.thinkingOn')" />
          <t-option value="off" :label="t('workflow.editor.thinkingOff')" />
        </t-select>
        <template #tips>{{ t('workflow.editor.thinkingHint') }}</template>
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
          <RefTextarea
          :model-value="strParam('query')"
            :autosize="{ minRows: 2, maxRows: 6 }"
            :placeholder="t('workflow.editor.promptHint')"
          :suggestions="refSuggestions"
          @change="setParam('query', $event)"
        />
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" :env-names="envNames" @insert="insertRef('query', $event)" />
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
      <t-form-item :label="t('workflow.editor.cases')">
        <div class="wf-prop-rows">
          <div v-for="(item, index) in switchCases" :key="index" class="wf-prop-case">
            <div class="wf-prop-case-head">
              <t-select v-model="item.logic" class="wf-prop-logic">
                <t-option value="and" :label="t('workflow.editor.logicAll')" />
                <t-option value="or" :label="t('workflow.editor.logicAny')" />
              </t-select>
              <t-select v-model="item.to" :placeholder="t('workflow.editor.caseTarget')" clearable size="small">
                <t-option v-for="option in nodeOptions" :key="option.value" :value="option.value" :label="option.label" />
              </t-select>
              <t-button variant="text" theme="danger" size="small" @click="switchCases.splice(index, 1)">
                <template #icon><t-icon name="delete" /></template>
              </t-button>
            </div>
            <div v-for="(cond, condIndex) in item.conditions" :key="condIndex" class="wf-prop-cond">
              <t-input v-model="cond.ref" :placeholder="t('workflow.editor.condRef')" class="wf-prop-cond-ref" />
              <t-select v-model="cond.op" class="wf-prop-cond-op">
                <t-option v-for="op in SWITCH_OPERATORS" :key="op" :value="op" :label="t(`workflow.editor.ops.${op}`)" />
              </t-select>
              <t-input
                v-if="cond.op !== 'empty' && cond.op !== 'not_empty'"
                v-model="cond.value"
                :placeholder="t('workflow.editor.condValue')"
              />
              <t-button variant="text" theme="danger" size="small" @click="item.conditions.splice(condIndex, 1)">
                <template #icon><t-icon name="close" /></template>
              </t-button>
            </div>
            <t-button variant="dashed" size="small" block @click="item.conditions.push({ ref: '', op: 'eq', value: '' })">
              {{ t('workflow.editor.addCondition') }}
            </t-button>
          </div>
          <t-button
            variant="dashed"
            size="small"
            block
            @click="switchCases.push({ conditions: [{ ref: '', op: 'eq', value: '' }], logic: 'and', to: '' })"
          >
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
          <RefTextarea
          :model-value="strParam('template')"
            :autosize="{ minRows: 4, maxRows: 10 }"
            :placeholder="t('workflow.editor.promptHint')"
          :suggestions="refSuggestions"
          @change="setParam('template', $event)"
        />
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" :env-names="envNames" @insert="insertRef('template', $event)" />
        </div>
      </t-form-item>
    </template>

    <!-- ================= Template (string transform) ================= -->
    <template v-else-if="kind === 'Template'">
      <t-form-item :label="t('workflow.editor.template')">
        <div class="wf-prop-field">
          <RefTextarea
          :model-value="strParam('template')"
            :autosize="{ minRows: 3, maxRows: 8 }"
            :placeholder="t('workflow.editor.promptHint')"
          :suggestions="refSuggestions"
          @change="setParam('template', $event)"
        />
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" :env-names="envNames" @insert="insertRef('template', $event)" />
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
              :env-names="envNames"
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
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" :env-names="envNames" @insert="insertRef('url', $event)" />
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
          <RefTextarea
          :model-value="strParam('body_template')"
            :autosize="{ minRows: 3, maxRows: 8 }"
            placeholder="{&quot;query&quot;: &quot;{start@query}&quot;}"
          :suggestions="refSuggestions"
          @change="setParam('body_template', $event)"
        />
          <VariableRefPicker :current-node-id="currentNodeId" :nodes="nodes" :edges="edges" :env-names="envNames" @insert="insertRef('body_template', $event)" />
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
              :env-names="envNames"
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
    <!-- ================= WebSearch ================= -->
    <template v-else-if="kind === 'WebSearch'">
      <t-form-item :label="t('workflow.editor.queryTemplate')">
        <div class="wf-prop-field">
          <RefTextarea
            :model-value="strParam('query')"
            :autosize="{ minRows: 2, maxRows: 6 }"
            :placeholder="t('workflow.editor.promptHint')"
            :suggestions="refSuggestions"
            @change="setParam('query', $event)"
          />
          <VariableRefPicker
            :current-node-id="currentNodeId"
            :nodes="nodes"
            :edges="edges"
            :env-names="envNames"
            @insert="insertRef('query', $event)"
          />
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.searchProvider')">
        <t-select
          :value="strParam('provider_id')"
          :placeholder="t('workflow.editor.searchProviderHint')"
          clearable
          filterable
          @change="setParam('provider_id', $event)"
        >
          <t-option v-for="provider in webSearchProviders" :key="provider.id" :value="provider.id" :label="provider.name" />
        </t-select>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.maxResults')">
        <t-input-number :value="numParam('max_results', 5)" :min="1" :max="20" theme="column" @change="setParam('max_results', $event)" />
      </t-form-item>
      <p class="wf-prop-hint">{{ t('workflow.editor.webSearchHint') }}</p>
    </template>

    <!-- ================= QuestionClassifier ================= -->
    <template v-else-if="kind === 'QuestionClassifier'">
      <t-form-item :label="t('workflow.editor.queryTemplate')">
        <div class="wf-prop-field">
          <RefTextarea
            :model-value="strParam('query')"
            :autosize="{ minRows: 2, maxRows: 6 }"
            :placeholder="t('workflow.editor.promptHint')"
            :suggestions="refSuggestions"
            @change="setParam('query', $event)"
          />
          <VariableRefPicker
            :current-node-id="currentNodeId"
            :nodes="nodes"
            :edges="edges"
            :env-names="envNames"
            @insert="insertRef('query', $event)"
          />
        </div>
      </t-form-item>
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
      <t-form-item :label="t('workflow.editor.classes')">
        <div class="wf-prop-rows">
          <div v-for="(item, index) in classifierClasses" :key="index" class="wf-prop-case">
            <div class="wf-prop-row">
              <t-input v-model="item.name" :placeholder="t('workflow.editor.className')" />
              <t-select v-model="item.to" :placeholder="t('workflow.editor.caseTarget')" clearable size="small">
                <t-option v-for="option in nodeOptions" :key="option.value" :value="option.value" :label="option.label" />
              </t-select>
              <t-button variant="text" theme="danger" size="small" @click="classifierClasses.splice(index, 1)">
                <template #icon><t-icon name="delete" /></template>
              </t-button>
            </div>
            <t-input v-model="item.description" :placeholder="t('workflow.editor.classDesc')" />
          </div>
          <t-button variant="dashed" size="small" block @click="classifierClasses.push({ name: '', to: '' })">
            {{ t('workflow.editor.addClass') }}
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

    <!-- ================= Agent ================= -->
    <template v-else-if="kind === 'Agent'">
      <t-form-item :label="t('workflow.editor.platformAgent')">
        <t-select
          :value="strParam('agent_id')"
          :placeholder="t('workflow.editor.platformAgentHint')"
          clearable
          filterable
          :loading="agentOptionsLoading"
          @change="onAgentIdChange"
        >
          <t-option v-for="a in agentOptions" :key="a.id" :value="a.id" :label="a.name" />
        </t-select>
        <p class="wf-prop-hint">{{ t('workflow.editor.platformAgentTip') }}</p>
      </t-form-item>
      <!-- Prompt stays editable in agent_id mode: it is the node's only query
           input — the reused agent's config covers everything else. -->
      <t-form-item :label="t('workflow.editor.prompt')">
        <div class="wf-prop-field">
          <RefTextarea
            :model-value="strParam('prompt')"
            :autosize="{ minRows: 3, maxRows: 10 }"
            :placeholder="t('workflow.editor.promptHint')"
            :suggestions="refSuggestions"
            @change="setParam('prompt', $event)"
          />
          <VariableRefPicker
            :current-node-id="currentNodeId"
            :nodes="nodes"
            :edges="edges"
            :env-names="envNames"
            @insert="insertRef('prompt', $event)"
          />
        </div>
      </t-form-item>
      <t-form-item v-if="!agentIdSelected" :label="t('workflow.editor.systemPrompt')">
        <div class="wf-prop-field">
          <RefTextarea
            :model-value="strParam('system_prompt')"
            :autosize="{ minRows: 2, maxRows: 8 }"
            :placeholder="t('workflow.editor.promptHint')"
            :suggestions="refSuggestions"
            @change="setParam('system_prompt', $event)"
          />
        </div>
      </t-form-item>
      <t-form-item v-if="!agentIdSelected" :label="t('workflow.editor.model')">
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
      <t-form-item v-if="!agentIdSelected" :label="t('workflow.editor.kbSelect')">
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
        <p class="wf-prop-hint">{{ t('workflow.editor.agentKbHint') }}</p>
      </t-form-item>
      <t-form-item v-if="!agentIdSelected" :label="t('workflow.editor.temperature')">
        <t-slider :value="numParam('temperature', 0.4)" :min="0" :max="2" :step="0.1" @change="setParam('temperature', $event)" />
      </t-form-item>
    </template>

    <!-- ================= MCPTool ================= -->
    <template v-else-if="kind === 'MCPTool'">
      <t-form-item :label="t('workflow.editor.mcpService')">
        <t-input :value="strParam('service_id')" placeholder="service id" @change="setParam('service_id', $event)" />
      </t-form-item>
      <t-form-item :label="t('workflow.editor.mcpTool')">
        <t-input :value="strParam('tool')" placeholder="tool name" @change="setParam('tool', $event)" />
      </t-form-item>
      <t-form-item :label="t('workflow.editor.mcpArgs')">
        <div class="wf-prop-field">
          <RefTextarea
            :model-value="strParam('args')"
            :autosize="{ minRows: 2, maxRows: 8 }"
            :placeholder="t('workflow.editor.mcpArgsPlaceholder')"
            :suggestions="refSuggestions"
            @change="setParam('args', $event)"
          />
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
      <p class="wf-prop-hint">{{ t('workflow.editor.mcpIntranetHint') }}</p>
    </template>

    <!-- ================= Iteration ================= -->
    <template v-else-if="kind === 'Iteration'">
      <t-form-item :label="t('workflow.editor.items')">
        <div class="wf-prop-field">
          <RefTextarea
            :model-value="strParam('items')"
            :autosize="{ minRows: 2, maxRows: 6 }"
            :placeholder="t('workflow.editor.itemsPlaceholder')"
            :suggestions="refSuggestions"
            @change="setParam('items', $event)"
          />
          <VariableRefPicker
            :current-node-id="currentNodeId"
            :nodes="nodes"
            :edges="edges"
            :env-names="envNames"
            @insert="insertRef('items', $event)"
          />
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.itemVar')">
        <t-input :value="strParam('item_var') || 'item'" @change="setParam('item_var', $event)" />
      </t-form-item>
      <t-form-item :label="t('workflow.editor.outputRef')">
        <div class="wf-prop-field">
          <RefTextarea
            :model-value="strParam('output_ref')"
            :autosize="{ minRows: 2, maxRows: 4 }"
            :placeholder="t('workflow.editor.outputRefPlaceholder')"
            :suggestions="refSuggestions"
            @change="setParam('output_ref', $event)"
          />
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.outputVar')">
        <t-input :value="strParam('output_var') || 'results'" @change="setParam('output_var', $event)" />
      </t-form-item>
      <p class="wf-prop-hint">{{ t('workflow.editor.iterationHint') }}</p>
    </template>

    <!-- ================= Code ================= -->
    <template v-else-if="kind === 'Code'">
      <t-form-item :label="t('workflow.editor.language')">
        <t-select :value="strParam('language') || 'python3'" @change="setParam('language', $event)">
          <t-option value="python3" label="Python 3" />
          <t-option value="node" label="Node.js" />
        </t-select>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.code')">
        <t-textarea
          :value="strParam('code')"
          :autosize="{ minRows: 6, maxRows: 18 }"
          :placeholder="t('workflow.editor.codePlaceholder')"
          class="wf-prop-code"
          @change="setParam('code', $event)"
        />
        <p class="wf-prop-hint">{{ t('workflow.editor.codeHint') }}</p>
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
              :env-names="envNames"
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
      <t-form-item :label="t('workflow.editor.timeout')">
        <t-input-number
          :value="numParam('timeout_seconds', 30)"
          :min="1"
          :max="120"
          theme="column"
          @change="setParam('timeout_seconds', $event)"
        />
      </t-form-item>
      <p class="wf-prop-hint">{{ t('workflow.editor.codeIntranetHint') }}</p>
    </template>

    <!-- ================= ParameterExtractor ================= -->
    <template v-else-if="kind === 'ParameterExtractor'">
      <t-form-item :label="t('workflow.editor.inputText')">
        <div class="wf-prop-field">
          <RefTextarea
            :model-value="strParam('query')"
            :autosize="{ minRows: 2, maxRows: 8 }"
            :placeholder="t('workflow.editor.promptHint')"
            :suggestions="refSuggestions"
            @change="setParam('query', $event)"
          />
          <VariableRefPicker
            :current-node-id="currentNodeId"
            :nodes="nodes"
            :edges="edges"
            :env-names="envNames"
            @insert="insertRef('query', $event)"
          />
        </div>
      </t-form-item>
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
      <t-form-item :label="t('workflow.editor.extractParams')">
        <div class="wf-prop-rows">
          <div v-for="(item, index) in extractorParams" :key="index" class="wf-prop-case">
            <div class="wf-prop-row">
              <t-input v-model="item.name" :placeholder="t('workflow.editor.varName')" />
              <t-select v-model="item.type" class="wf-prop-field-type">
                <t-option value="string" :label="t('workflow.editor.fieldText')" />
                <t-option value="number" :label="t('workflow.editor.fieldNumber')" />
                <t-option value="boolean" :label="t('workflow.editor.fieldBool')" />
              </t-select>
              <t-checkbox v-model="item.required">{{ t('workflow.editor.fieldRequired') }}</t-checkbox>
              <t-button variant="text" theme="danger" size="small" @click="extractorParams.splice(index, 1)">
                <template #icon><t-icon name="delete" /></template>
              </t-button>
            </div>
            <t-input v-model="item.description" :placeholder="t('workflow.editor.classDesc')" />
          </div>
          <t-button variant="dashed" size="small" block @click="addExtractorParam()">
            {{ t('workflow.editor.addExtractParam') }}
          </t-button>
        </div>
      </t-form-item>
    </template>
    <!-- ================= Error policy + retry (all executable kinds) ================= -->
    <template v-if="kind !== 'Start'">
      <t-form-item v-if="iterationOptions.length > 0" :label="t('workflow.editor.bodyMembership')">
        <t-select
          :value="bodyParent"
          clearable
          :placeholder="t('workflow.editor.bodyMembershipHint')"
          @change="setBodyParent"
        >
          <t-option v-for="option in iterationOptions" :key="option.value" :value="option.value" :label="option.label" />
        </t-select>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.errorHandling')">
        <t-select :value="onErrorAction" @change="setOnErrorAction">
          <t-option value="fail" :label="t('workflow.editor.onErrorFail')" />
          <t-option value="continue" :label="t('workflow.editor.onErrorContinue')" />
          <t-option value="route_to" :label="t('workflow.editor.onErrorRoute')" />
        </t-select>
      </t-form-item>
      <t-form-item v-if="onErrorAction === 'continue'" :label="t('workflow.editor.defaultOutputs')">
        <div class="wf-prop-rows">
          <div v-for="(row, index) in defaultOutputRows" :key="index" class="wf-prop-row">
            <t-input v-model="row.key" :placeholder="t('workflow.editor.varName')" class="wf-prop-var-name" />
            <t-input v-model="row.value" :placeholder="t('workflow.editor.variablesValue')" />
            <t-button variant="text" theme="danger" size="small" @click="defaultOutputRows.splice(index, 1)">
              <template #icon><t-icon name="delete" /></template>
            </t-button>
          </div>
          <t-button variant="dashed" size="small" block @click="defaultOutputRows.push({ key: '', value: '' })">
            {{ t('workflow.editor.addVar') }}
          </t-button>
        </div>
      </t-form-item>
      <t-form-item v-if="onErrorAction === 'route_to'" :label="t('workflow.editor.errorRoute')">
        <div class="wf-prop-field">
          <t-select
            :value="onErrorRoute"
            :placeholder="t('workflow.editor.caseTarget')"
            clearable
            @change="setOnErrorRoute"
          >
            <t-option v-for="option in nodeOptions" :key="option.value" :value="option.value" :label="option.label" />
          </t-select>
          <p class="wf-prop-hint">{{ t('workflow.editor.errorRouteHint') }}</p>
        </div>
      </t-form-item>
      <t-form-item :label="t('workflow.editor.retry')">
        <div class="wf-prop-row">
          <t-input-number
            :value="retryCount"
            :min="0"
            :max="5"
            theme="column"
            :placeholder="t('workflow.editor.retryCount')"
            @change="setRetryParam('count', $event)"
          />
          <t-input-number
            :value="retryDelay"
            :min="0"
            :max="60000"
            :step="100"
            theme="column"
            :placeholder="t('workflow.editor.retryDelay')"
            @change="setRetryParam('delay_ms', $event)"
          />
        </div>
      </t-form-item>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Edge } from '@vue-flow/core'
import type { ModelConfig } from '@/api/model'
import type { CustomAgent } from '@/api/agent'
import { listAgents } from '@/api/agent'
import type { ClassifierClass, ExtractorParam, StartField, SwitchCaseGroup, TemplateOp, WorkflowNodeType } from '@/api/workflow'
import VariableRefPicker from './VariableRefPicker.vue'
import RefTextarea from './RefTextarea.vue'
import { upstreamRefSuggestions } from '../nodeMeta'

/** Switch condition operators (mirror of the engine's conditionOps). */
const SWITCH_OPERATORS = [
  'eq', 'ne', 'contains', 'not_contains', 'starts_with', 'ends_with',
  'empty', 'not_empty', 'gt', 'gte', 'lt', 'lte', 'regex', 'in', 'not_in',
] as const

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
  /** Workflow variable names (env.* suggestions in pickers/autocomplete). */
  envNames?: string[]
  /** Configured web search providers (WebSearch node picker). */
  webSearchProviders?: Array<{ id: string; name: string }>
  /** Iteration body membership (node.data.parent), when set. */
  parent?: string
}>()

const emit = defineEmits<{
  'set-parent': [parentId: string]
}>()

const { t } = useI18n()

// ---- platform agent reuse (Agent node) ----------------------------------

// Loaded once per mount; smart-reasoning agents are the ones whose config
// the node can reuse. Builtins are included: the backend loads them via
// GetAgentByID (DB row or builtin registry fallback).
const agentOptions = ref<CustomAgent[]>([])
const agentOptionsLoading = ref(false)
const agentIdSelected = computed(() => strParam('agent_id') !== '')

async function loadAgentOptions() {
  agentOptionsLoading.value = true
  try {
    const res = await listAgents()
    agentOptions.value = (res.data ?? []).filter((a) => a.config?.agent_mode === 'smart-reasoning')
  } catch {
    agentOptions.value = []
  } finally {
    agentOptionsLoading.value = false
  }
}
loadAgentOptions()

function onAgentIdChange(value: unknown) {
  setParam('agent_id', typeof value === 'string' && value ? value : undefined)
}

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

// Thinking tri-state: '' (param absent) | 'on' | 'off'. Absent defers to the
// model's own default; the wire format for on/off is selected per model via
// extra_config.thinking_control in the model editor.
const thinkingValue = computed(() => {
  const v = props.params.thinking
  if (v === undefined || v === null) return ''
  return v === true || v === 'true' ? 'on' : 'off'
})

function setThinking(next: unknown) {
  const v = typeof next === 'string' ? next : ''
  if (v === '') delete props.params.thinking
  else props.params.thinking = v === 'on'
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

/** Writable view of a params list: raw array in the DSL, typed list in the form. */
function typedListParam<T>(key: string) {
  return computed<Array<T>>({
    get: () => (Array.isArray(props.params[key]) ? (props.params[key] as Array<T>) : []),
    set: (value) => setParam(key, value),
  })
}

const switchCases = typedListParam<SwitchCaseGroup>('cases')

const startFields = typedListParam<StartField>('fields')

function addField() {
  startFields.value.push({ name: '', type: 'text', required: false, default: '', label: '', options: [] })
}

const classifierClasses = typedListParam<ClassifierClass>('classes')

const extractorParams = typedListParam<ExtractorParam>('parameters')

function addExtractorParam() {
  extractorParams.value.push({ name: '', type: 'string', required: false, description: '' })
}

// ---- iteration body membership (generic section) --------------------------

const iterationOptions = computed(() =>
  props.nodes
    .filter((node) => node.kind === 'Iteration' && node.id !== props.currentNodeId)
    .map((node) => ({ value: node.id, label: `${t('workflow.nodes.Iteration')} · ${node.id}` })),
)

const bodyParent = computed(() => (typeof props.parent === 'string' ? props.parent : ''))

function setBodyParent(target: unknown) {
  emit('set-parent', typeof target === 'string' ? target : '')
}

// ---- error policy + retry (generic section) ------------------------------

const onErrorAction = computed(() => {
  const action = (props.params.on_error as Record<string, unknown> | undefined)?.action
  return typeof action === 'string' ? action : 'fail'
})

const onErrorRoute = computed(() => {
  const route = (props.params.on_error as Record<string, unknown> | undefined)?.route_to
  return typeof route === 'string' ? route : ''
})

function setOnErrorAction(action: unknown) {
  const next = typeof action === 'string' ? action : 'fail'
  if (next === 'fail') {
    delete props.params.on_error
    return
  }
  props.params.on_error = { ...((props.params.on_error as object) ?? {}), action: next }
}

function setOnErrorRoute(target: unknown) {
  const routeTo = typeof target === 'string' ? target : ''
  props.params.on_error = { ...((props.params.on_error as object) ?? {}), action: 'route_to', route_to: routeTo }
}

const defaultOutputRows = ref<Array<{ key: string; value: string }>>([])
watch(
  () => props.params.on_error,
  (onError) => {
    const outputs = (onError as Record<string, unknown> | undefined)?.default_outputs
    defaultOutputRows.value =
      outputs && typeof outputs === 'object'
        ? Object.entries(outputs as Record<string, unknown>).map(([key, value]) => ({ key, value: String(value ?? '') }))
        : []
  },
  { immediate: true, deep: true },
)
watch(
  defaultOutputRows,
  (rows) => {
    const onError = (props.params.on_error as Record<string, unknown> | undefined) ?? {}
    if (onError.action !== 'continue') return
    const outputs: Record<string, string> = {}
    for (const row of rows) {
      const key = row.key.trim()
      if (key) outputs[key] = row.value
    }
    // Guard: same no-op-write protection as headerRows — the paired watch
    // rebuilds rows on every params identity change, which would otherwise
    // loop forever once action === 'continue'.
    if (JSON.stringify(outputs) === JSON.stringify(onError.default_outputs ?? {})) return
    props.params.on_error = { ...onError, action: 'continue', default_outputs: outputs }
  },
  { deep: true },
)

const retryCount = computed(() => {
  const count = (props.params.retry as Record<string, unknown> | undefined)?.count
  return typeof count === 'number' ? count : 0
})
const retryDelay = computed(() => {
  const delay = (props.params.retry as Record<string, unknown> | undefined)?.delay_ms
  return typeof delay === 'number' ? delay : 0
})

function setRetryParam(key: 'count' | 'delay_ms', value: unknown) {
  const current = (props.params.retry as Record<string, unknown> | undefined) ?? {}
  const next = { ...current, [key]: typeof value === 'number' ? value : 0 }
  if (next.count === 0 && next.delay_ms === 0) {
    delete props.params.retry
    return
  }
  props.params.retry = next
}

/** Inline {ref} autocomplete options for template textareas. */
const refSuggestions = computed(() =>
  upstreamRefSuggestions(props.currentNodeId, props.nodes, props.edges, props.envNames ?? []),
)

const templateOps = typedListParam<TemplateOp>('ops')

const varList = typedListParam<{ name: string; ref: string }>('variables')

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
    // Guard: skip no-op writes. Without this the two watches ping-pong
    // forever (new object identities each cycle) and the tab freezes.
    if (JSON.stringify(headers) === JSON.stringify(props.params.headers ?? {})) return
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

.wf-prop-case {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: 6px;
}

.wf-prop-case-head {
  display: flex;
  align-items: center;
  gap: 4px;
}

.wf-prop-logic {
  flex: 0 0 84px !important;
}

.wf-prop-cond {
  display: flex;
  align-items: center;
  gap: 4px;
}

.wf-prop-cond .t-input,
.wf-prop-cond .t-select {
  min-width: 0;
}

.wf-prop-cond-ref {
  flex: 1.4 !important;
}

.wf-prop-cond-op {
  flex: 1 !important;
}

.wf-prop-field-type {
  flex: 0 0 100px !important;
}

.wf-prop-code :deep(textarea) {
  font-family: var(--td-font-family-code, monospace);
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
