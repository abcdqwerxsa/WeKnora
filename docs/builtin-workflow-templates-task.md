# 任务书：内置工作流模板（Built-in Workflow Templates）

> 自包含工作单。执行者假定 fresh context，本文件包含全部所需事实坐标。
> 范围：代码 + 模板 + 测试。**不做部署、不动 docker。**

## 0. 一句话目标

把 8 个预置工作流以**只读模板**形式内置进平台：启动时从 `config/workflow_templates/` 加载校验，前端"从模板创建"，一键在当前租户生成已发布的可运行副本。

## 1. 已核实的现状坐标（全部已验证，直接引用）

| 事实 | 位置 |
|---|---|
| 引擎 DSL 契约：`DSL{version, graph?, components: map[id]{obj:{component_name, params}, upstream, downstream, parent?}}`；入口节点类型必须是 `Start`；缺 graph 时 Normalize 自动补布局 | `internal/agent/workflow/dsl.go` L19-120 |
| 变量引用语法 `{nodeId@param}` / `{sys.query}` / `{sys.files}` / `{env.x}`；未解析即报错 | `internal/agent/workflow/nodes/template.go` L21 |
| 节点类型 17 种：`Start/Answer/LLM/Retrieval/Switch/Agent/Template/VariableAggregator/HTTP/DataOps/Code/MCPTool/WebSearch/QuestionClassifier/ParameterExtractor/Iteration` | `internal/agent/workflow/nodes/*.go`（builtin.go L16-20, agent.go L11, extended.go L20-23, code.go L16, mcp_tool.go L14, advanced.go L27-34） |
| **LLM 节点 `model` 参数可省**，空则走租户默认模型回退 | `nodes/builtin.go` L211（`model, _ := params["model"].(string)`），服务侧回退 `workflow_service.go` ~L1746 |
| builtin_agents.yaml 从 config 目录按路径启动加载（**非 go:embed**，平台既有机制，生产已验证） | `internal/types/builtin_agent_config.go` L49-58 |
| 工作流服务：CreateWorkflow / PublishWorkflow（发布快照，运行只用 PublishedDSL） | `internal/application/service/workflow_service.go` L275 / L474 |
| DSL normalize 入口 | `workflow_service.go` L857 |
| 路由与权限模式（Contributor/Viewer/OwnedWorkflowOrAdmin） | `internal/router/routes_workflow.go` L39-60 |
| Handler 模式（Create/List/Get/Run/Publish…） | `internal/handler/workflow.go`（Publish :443） |
| 前端：列表页、编辑器、路由 | `frontend/src/views/workflow/WorkflowList.vue`、`WorkflowEditor.vue`、`frontend/src/router` L126-133 |
| 前端节点契约枚举 | `frontend/src/api/workflowContract.ts` |
| 编译测试的 Deps stub 模式（模板校验测试直接抄） | `internal/agent/workflow/compile_test.go` L40 |
| 可用内置 Agent（`config/builtin_agents.yaml`）：`builtin-doc-review-{format,proofread,consistency,dispatcher}`、`builtin-bid-{parser,scorer,comparator,risker}`、`builtin-gen-{plan,report,reviewer}`、`builtin-kb-{ingest,associator,advisor}`、`builtin-wiki-researcher` 等 | `config/builtin_agents.yaml` |

### ⚠️ 关键坑：旧 POC 示例与引擎契约存在格式漂移

`config/workflows/document-review-suite/` 下 5 个 JSON（wf-doc-review / wf-doc-generation / wf-bid-comparison / wf-kb-advisory / wf-kb-ingest）是**扁平旧格式**，当前引擎无法解析：

- components 是 `{params, upstream, downstream}`，**缺 `obj.component_name` 包装**（节点类型未声明）；
- 变量引用 `{start_id@query}` 与节点 id `start-1` 不匹配，现行语法是 `{sys.query}`。

所以"转正"= 迁移重写，不是直接复制。旧目录 `config/workflows/` 保持原样不动（POC 存档，不在本任务删除范围）。

### KB 占位符分布（instantiate 时需绑定替换）

- `KB_COMPANY_TEMPLATE`（doc-review）、`KB_COMPANY_HISTORY`（kb-advisory）、`KB_DOC_TEMPLATE` + `KB_BID_HISTORY`（doc-generation）、`KB_BID_TEMPLATE` + `KB_BID_HISTORY`（bid-comparison）。均为 `params.kb_ids` 数组里的裸字符串。**kb_ids 无降级机制，占位符不替换则运行必失败。**

## 2. 已定设计决策（不再开放讨论）

1. **只读模板 + instantiate 复制**，不做每租户自动 seed、不做升级合并逻辑（业界一致做法：Coze/n8n/Dify 均为复制后解耦）。
2. **加载机制跟随 builtin_agents 先例**：启动时从 config 目录读 `config/workflow_templates/`（路径加载，非 embed），启动即校验全部模板 Normalize+Compile，失败 fail-fast。
3. **模板中一律不写死模型**：LLM 节点省略 `model`；Agent 节点引用现有内置 Agent（它们自身不锁模型）。
4. **KB 占位符在 instantiate 请求中绑定替换**，服务端校验 KB 归属租户；模板有占位符而请求未全绑定 → 400 列出缺失项。
5. i18n 复用 builtin_agents.yaml 的 `i18n: {default, zh-CN, ...}` 结构，按请求 locale 解析，不新建 i18n 设施。

## 3. 任务分解

### A. 后端机制

1. **模板包** `internal/agent/workflow/templates.go`（package workflow）：
   - `LoadWorkflowTemplates(configDir string) ([]WorkflowTemplate, error)`：读 `config/workflow_templates/*.json`；
   - 启动加载后对每个模板执行 `Normalize` + `Compile(tpl.DSL, stubDeps)`（stub 抄 compile_test.go L40），任一失败返回错误；
   - `WorkflowTemplate` 结构见 §5。
2. **服务方法** `InstantiateWorkflowTemplate(ctx, templateID, req)`（放 workflow_service.go）：
   - 深拷贝模板 DSL → 按 `kb_bindings` map 遍历 components 的 `params.kb_ids` 数组做字符串替换（遍历结构体，不做序列化文本替换）；
   - 校验所有绑定 KB 归属当前租户（复用现有 KB 校验路径，参考 `workflow_service.go` L1676-1686 的 SearchTarget 解析）；
   - 调 `CreateWorkflow`（L275）→ `PublishWorkflow`（L474），返回已发布工作流；
   - name 默认取模板 locale 名。
3. **路由**（routes_workflow.go，新组 `/workflow-templates`，不进 apiKeyGroup）：
   - `GET /workflow-templates` — Viewer，列表（locale 解析后的 name/description + category + kb_placeholders）；
   - `GET /workflow-templates/:id` — Viewer，含完整 DSL（编辑器预览用）；
   - `POST /workflow-templates/:id/instantiate` — Contributor，body `{name?: string, kb_bindings: {占位符: kb_id}}`。
4. **Handler** `internal/handler/workflow_template.go`，抄现有 handler 模式。

### B. 前端

`WorkflowList.vue` 加"从模板创建"按钮 → 弹窗两步：
1. 模板列表（name/description/category，调 GET 列表）；
2. KB 绑定步骤：模板有 `kb_placeholders` 时，每个占位符一个必选 KB 选择器（复用现有 KB 选择组件，若无则用简单下拉 + 现有 KB list API）；无占位符直接跳过；
提交 POST instantiate → 跳转 `workflow/:id/edit`。文案进现有 i18n locale 文件（zh-CN/en-US 至少）。

### C1. 迁移 5 个旧模板 → `config/workflow_templates/`

按 §5 新格式重写（保留原业务语义与内置 Agent 引用），修复漂移：
- 每个节点补 `obj.component_name`（start→`Start`、retrieval-*→`Retrieval`、llm-*（带 agent_id）→`Agent`、answer→`Answer`，其余按参数语义对照 nodes/*.go 判断）；
- `{start_id@query}` → `{sys.query}`；文件输入用 `{sys.files}`；
- KB 占位符原样保留并登记到顶层 `kb_placeholders`；
- graph 投影可省（Normalize 自动补布局）。
每个迁移模板必须在 A1 的启动校验中通过。

### C2. 新写 3 个模板

全部不写死模型，优先 LLM 节点（不耦合 agent_id）：

1. `wf-deep-research.json`（category: research）
   Start(`sys.query` 主题) → LLM 问题分解 → Iteration（子问题循环体：WebSearch + Retrieval(可选 KB) + LLM 段落摘要，子节点 `parent` 挂到 Iteration id）→ LLM 汇总结构化报告 → Answer。
   ⚠️ 动手前先读 `nodes/advanced.go` Iteration 节点的参数 schema（迭代输入如何引用、循环体变量如何回传），以实际契约为准。
2. `wf-doc-extraction.json`（category: extraction）
   Start(`sys.files` 文档 + `sys.query` 抽取字段说明) → ParameterExtractor → Switch（字段完整性分支）→ Answer（JSON 输出）。参考 advanced.go ParameterExtractor 契约。
3. `wf-doc-translation.json`（category: content）
   Start(`sys.files` + `sys.query` 目标语言) → Iteration（分段 LLM 翻译，保持术语一致）→ Template 拼接 → Answer。

### D. 测试

1. `internal/agent/workflow/templates_test.go`：加载真实 `config/workflow_templates/` 全量模板，断言 ≥8 个、全部 Normalize+Compile 通过、kb_placeholders 与 DSL 中实际占位符一致；
2. 服务层 instantiate 测试（跟现有 workflow service 测试同款脚手架）：绑定替换正确、缺绑定 400、创建后 status=published 且 PublishedDSL 含替换后 kb_ids；
3. `cd frontend && npm run build` 通过。

## 4. API 契约

```
GET  /api/v1/workflow-templates
     → [{id, category, name, description, kb_placeholders: ["KB_BID_TEMPLATE",...], node_count}]
GET  /api/v1/workflow-templates/:id
     → {id, category, name, description, kb_placeholders, dsl}
POST /api/v1/workflow-templates/:id/instantiate   (Contributor)
     body  {name?: string, kb_bindings: {"KB_BID_TEMPLATE": "<uuid>"}}
     → 201 types.Workflow（已发布，可直接 Run）
     err  400 {missing_placeholders: [...]} / 404 / 403
```

## 5. 模板文件格式（`config/workflow_templates/*.json`）

```json
{
  "id": "wf-doc-review",
  "category": "document-review",
  "kb_placeholders": ["KB_COMPANY_TEMPLATE"],
  "i18n": {
    "default": { "name": "Document Review", "description": "..." },
    "zh-CN":   { "name": "文档审查", "description": "..." }
  },
  "dsl": { "version": 1, "components": { "...": { "obj": { "component_name": "Start", "params": {} }, "upstream": [], "downstream": [] } } }
}
```

## 6. 验收标准

1. `go test ./internal/...` 全绿，含新增 templates_test 与 instantiate 测试；
2. 启动日志/校验确认 8 个模板全部可编译（迁移 5 + 新写 3）；
3. instantiate（带合法 KB 绑定）返回 published 工作流，RunWorkflow 在测试租户可跑通；
4. 前端 build 通过，"从模板创建"流程可用；
5. 全程未引入新 Go 依赖、未新增 npm 依赖。

## 7. 边界与禁止事项

- 不删除/不修改 `config/workflows/`（旧 POC 存档）；
- 不做模板市场、模板 CRUD、定时触发类模板（YAGNI）；
- 不在模板中写死任何 model id / rerank model id；
- 不做 docker 镜像、不做部署（验证止于 go test + npm build）；
- TypeScript strict、Go 公共函数带 context/类型注释，跟随项目现有风格。

## 8. 验证命令

```bash
go test ./internal/agent/workflow/... ./internal/application/service/... -run 'Template|Workflow' -v
go build ./cmd/...
cd frontend && npm run build
```
