# 工作流功能完善：对标 Dify 完整度（内网版）

## Context

WeKnora 工作流已有骨架：eino 引擎（`internal/agent/workflow/`）+ 9 种节点 + REST/RBAC + vue-flow 画布 + 同步/异步运行 + SSE 进度 + 取消/断点续跑。但离 Dify 的工作流完整度差距明显：无调试面板、条件分支只有字符串相等、无输入表单、无发布模型、节点能力单薄。

**目标**：分五个阶段把工作流拉到 Dify 完整度。
**硬约束（内网）**：
- 不引入任何依赖公网的能力：无 Dify marketplace/外部工具预置，模板走本地内置
- HTTP 节点维持内网/私网地址策略
- Web 搜索类节点只走管理员已配置的搜索 Provider（可为内网 SearXNG）
- 模型/知识库/沙箱全部使用本系统已有自托管设施

**现状关键事实（已核实）**
- 引擎限制：compile.go 强制"单入口 + 单终端"，无并行分支（Dify 允许多路不汇合）→ Phase 4 放宽
- `OnNodeEvent` 回调只有 phase/duration，无 payload → 调试面板需扩展
- StateSnapshot 已含每节点 outputs（checkpoint.go ExportState），节点级检查有数据基础
- `DSL.Variables` → `env.*` 编译链路已通（compile.go），但无编辑 UI
- 前端无 dagre/elkjs；后端 `defaultLayout`（BFS 分层，dsl.go）可移植为 TS（~60 行，零新依赖）
- 可复用设施：skill 沙箱（Docker/E2B/Cube，`internal/sandbox`）→ Code 节点；ReAct 引擎（`internal/agent/engine.go`）→ Agent 节点；搜索（searchutil）→ WebSearch 节点；API Key 授权器目前对 workflow 路由 default-deny（routes_workflow.go 注释）

## Approach

五阶段递进，每阶段是独立可合并的切片。执行顺序即 Phase 1→5；Phase 1 先做"看得见"的调试与编辑体验（后续所有阶段的验收都依赖它）。

### 不移植的 Dify 特性（内网排除项）
Marketplace/外部模板市场、需要公网的内置工具（Google/Wikipedia 等）、SaaS 发布渠道。WeKnora 已有的 IM/embed 渠道不在本计划内扩展。

---

## Phase 1 — 运行调试 + 编辑器基础体验（UI/UX 地基）

**后端**
- `NodeEvent` / `WorkflowRunEvent` 增加 `inputs`/`outputs` 字段（从 CanvasState 快照取），节点完成时携带 payload
- `workflow_runs` 增加节点级 trace（JSON 列，migrations/versioned 新增 + sqlite 对应）
- 新端点 `GET /workflows/:id/runs/:run_id`（run 详情，含逐节点 trace）
- 复用：`workflow_run_events.go` 的事件装配、checkpoint 的 StateSnapshot

**前端**
- 运行抽屉：时间轴显示节点类型+名称（非裸 id）；点击节点 → 输入/输出 JSON 查看器；历史条目 → run 详情抽屉（回看任意一次运行的逐节点数据）
- 画布：节点卡片显示上次运行的输出摘要徽标；`WfNodeCard` 增加 output popover
- 编辑器基础：未保存离开拦截（dirty guard）、节点复制/粘贴、自动布局按钮（移植 `defaultLayout` 为 TS）、保存前校验（单入口/单终端/不可达节点/失效 `{nodeId@param}` 引用高亮）
- i18n 补齐 zh/en/ko/ru 四语言

**Files**：`internal/agent/workflow/compile.go`、`internal/application/service/workflow_run_events.go`、`workflow_service.go`、`internal/types/workflow.go`、`internal/handler/workflow.go`、`internal/router/routes_workflow.go`、migrations、`frontend/src/views/workflow/**`、`frontend/src/api/workflow.ts`、`frontend/src/i18n/locales/*.ts`

## Phase 2 — Dify 核心语义：条件运算符 + 输入表单 + 全局变量

- **条件升级**：Switch 扩展为 Dify IF/ELSE 语义——运算符 `is/is-not/contains/not-contains/starts-with/ends-with/empty/not-empty/=/≠/>/</≥/≤/regex/in`，多条件 AND/OR；旧 DSL 的相等 cases 自动迁移（复用 `migrateNodeParams` 模式）；边标签跟随分支名
- **Start 输入表单**：字段定义（text/paragraph/number/select，必填/默认值），运行面板按表单渲染输入控件；run API 接受 form 值 → 物化进 Start 输出与 `sys.*`
- **工作流变量 UI**：编辑器侧栏编辑 `DSL.Variables`（name/value），运行时经既有 `env.*` 链路生效；`{nodeId@param}` 引用选择器增加 `env.`/`sys.` 组
- **变量引用体验**：模板输入框内联自动补全（基于上游节点输出元数据 `NODE_OUTPUT_PARAMS`），失效引用红色高亮

**Files**：`internal/agent/workflow/nodes/builtin.go`（Switch 重构）、`state.go`、`workflow_service.go`（run 入参）、前端 `NodePropertyForm.vue`、`VariableRefPicker.vue`、`dsl.ts`、`nodeMeta.ts`

## Phase 3 — 高级节点（全部复用已有自托管设施）

按性价比排序，逐节点成切片：
1. **Code**：Python/JS，注入 `CodeFunc` 适配器 → 复用 skill 沙箱（Docker/E2B/Cube，超时/资源沿用现有策略）；无沙箱配置时节点编译明确报错
2. **Agent**：注入 `AgentFunc` → 复用 ReAct 引擎（限定该工作流可用的 KB/工具/模型参数）
3. **WebSearch**：注入 `SearchFunc` → 复用 searchutil Provider（管理员配置，内网可用）
4. **QuestionClassifier**：LLM + 类别列表 → 输出分支目标（复用 Switch 路由机制 `RouteOutputKey`）
5. **ParameterExtractor**：LLM + 字段描述列表 → 结构化输出（与 QuestionClassifier 共享 LLM-JSON 基座）
6. **Iteration**：数组循环执行子链（引擎工作量最大：eino 子图迭代 + checkpoint 兼容续跑；单独成片，若排期紧可后置到 Phase 5 前）
7. **MCP Tool**（可选）：调用租户已配置 MCP 工具

每个节点：`nodes/` 新文件 + `Deps` 注入 + `workflow_nodes_adapters.go` 适配器 + 前端 palette/属性表单/nodeMeta/i18n 四语言

**Files**：`internal/agent/workflow/nodes/*.go`、`registry.go`、`compile.go`（Deps 扩展）、`internal/application/service/workflow_nodes_adapters.go`、`internal/sandbox`、`internal/agent`、`internal/searchutil`（只读复用）、前端 workflow 目录

## Phase 4 — 发布模型 + 触发渠道 + 流式 + 并行（Dify 应用化）

- **Draft/Published 快照**：发布时冻结当前 DSL 为 published 副本（复用 `Version` 字段），草稿继续编辑互不影响；对外运行执行 published，编辑器调试运行执行 draft；未发布工作流仅创建者可在编辑器内调试
- **状态管理 UI**：列表页 + 编辑器的 发布/取消发布/归档 操作（A1/B1）；发布前置校验（Phase 1 的校验器复用）
- **API Key 开放**：在 APIKeyRouteAuthorizer 为 run 端点声明能力集（只读 run 历史 + 执行 published），补 scoped key 语义
- **Agent 工具触发**：注册 `run_workflow` 工具，聊天/Agent 会话可调用已发布工作流（走 ReAct 工具链，无需新 UI）
- **Answer 流式**：run events 增加增量 token 帧（`WorkflowRunEvent` 扩展 kind=delta），运行面板打字机渲染
- **并行分支**：放宽单终端约束为"多终端输出合并"；eino 图多下游并发（若 eino 版本限制则退化为顺序执行多分支，标注 ceiling）

**Files**：`workflow_service.go`、`internal/types/workflow.go`、migrations（published 快照存储）、`internal/router/routes_workflow.go` + APIKey 授权器、`internal/agent`（工具注册）、`compile.go`

## Phase 5 — 可靠性

- 节点级错误处理：continue-on-fail + 默认值输出 + 失败分支（Switch 路由复用）
- 节点重试策略（次数/退避），复用任务队列重试基建
- 定时触发（cron）：任务队列调度已发布工作流，输入模板化
- 撤销/重做：画布操作历史栈（若 Phase 1 未含）

**Files**：`nodes/`（错误包装）、`compile.go`、`workflow_service.go`、任务队列相关

---

## Reuse 清单（已定位）

| 需求 | 复用 | 位置 |
|---|---|---|
| 节点输出快照 | StateSnapshot / ExportState | `internal/agent/workflow/checkpoint.go`、`state.go` |
| 事件装配 | run events broker | `internal/application/service/workflow_run_events.go` |
| Code 执行 | skill 沙箱 Docker/E2B/Cube | `internal/sandbox/` |
| Agent 节点 | ReAct 引擎 | `internal/agent/engine.go` |
| WebSearch | searchutil Provider | `internal/searchutil/` |
| 自动布局算法 | defaultLayout（移植 TS） | `internal/agent/workflow/dsl.go` |
| 全局变量链路 | DSL.Variables → env.* | `internal/agent/workflow/compile.go` |
| 分支路由 | RouteOutputKey 机制 | `internal/agent/workflow/nodes/registry.go` |
| 迁移模式 | migrateNodeParams | `frontend/src/views/workflow/dsl.ts` |
| LLM 适配 | LLMFunc 注入 | `workflow_nodes_adapters.go` |

## Steps

> 执行记录（本轮）：Phase 1 ✅ 全部、Phase 2 ✅ 全部、Phase 3 ✅ 交付 3/6 节点（WebSearch/QuestionClassifier/ParameterExtractor，均带测试；Code/Agent/Iteration/MCP 依计划各自成片）、Phase 4 ✅ 核心切片（发布快照+门禁+状态 UI+API Key；run_workflow 工具/流式/并行各自成片）、Phase 5 ✅ 错误处理+重试+撤销重做（cron 另成片）。
> 后续待办：Code（沙箱临时会话）、Agent 节点、Iteration、MCP Tool、run_workflow Agent 工具、Answer 流式、多终端/并行放宽、cron 定时触发。

- [x] Phase 1 后端：事件 payload + run trace 持久化 + run 详情端点
- [x] Phase 1 前端：调试面板 / run 详情 / 画布输出徽标 / dirty guard / 复制粘贴 / 自动布局 / 保存校验 / i18n×4
- [x] Phase 2：Switch→条件运算符（含 DSL 迁移）、Start 输入表单、工作流变量 UI、引用自动补全
- [x~] Phase 3：Code → Agent → WebSearch → QuestionClassifier → ParameterExtractor →（Iteration、MCP Tool）
- [x~] Phase 4：发布快照 + 状态 UI + API Key + run_workflow 工具 + Answer 流式 + 并行放宽
- [x~] Phase 5：错误处理 / 重试 / cron / 撤销重做

（x~ = 交付了主切片，余项在上文待办清单）

## Verification

每阶段：
- `go test ./internal/agent/workflow/... ./internal/application/service/... ./internal/handler/...`
- `cd frontend && npm run build`（vue-tsc 类型检查）
- 手动 E2E：画布搭"Start →(输入表单) Retrieval → LLM → 条件分支 → Answer"全链路，运行后在调试面板逐节点核对输入/输出；发布后从聊天 Agent 工具调用同一工作流；四语言切换检查
- DSL 兼容回归：导入旧版 DSL（相等 cases）确认自动迁移

## 明确不做（内网排除）

Dify marketplace、外部公网工具预置、SaaS 发布渠道、依赖公网的模板市场。
