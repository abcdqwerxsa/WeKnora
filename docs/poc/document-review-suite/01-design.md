# 设计文档：四大功能的工作流与智能体

## 1. 总体架构

```
                            ┌──────────────────────┐
                            │  调度层（Workflow）   │
                            │  Start → 路由分发    │
                            └──────────┬───────────┘
                                       │
        ┌──────────────────────────────┼──────────────────────────────┐
        │                              │                              │
        ▼                              ▼                              ▼
  ┌───────────┐                ┌───────────┐                  ┌───────────┐
  │ 专业执行层 │                │ 专业执行层 │                  │ 专业执行层 │
  │ N 个 Agent │  ...           │ N 个 Agent │  ...             │ N 个 Agent │
  └─────┬─────┘                └─────┬─────┘                  └─────┬─────┘
        │                            │                              │
        └────────────────────────────┼──────────────────────────────┘
                                     ▼
                          ┌──────────────────────┐
                          │  校核层（Reviewer）  │
                          │  事实 / 合规 / 格式  │
                          └──────────┬───────────┘
                                     ▼
                          ┌──────────────────────┐
                          │  报告层（Answer）    │
                          │  模板化输出 + HITL   │
                          └──────────────────────┘
```

平台映射：

| 抽象层 | 平台组件 | 说明 |
|--------|---------|------|
| 调度 | Workflow `Switch` / `VariableAggregator` 节点 | 根据输入类型（审查/比对/生成/入库）选择分支 |
| 专业执行 | `CustomAgent`（`agent_type: custom`） | 每个专业角色是一个内置 Agent |
| 校核 | `CustomAgent`（强约束 system_prompt + 工具白名单） | "安全阀" |
| 报告 | Workflow `Answer` 节点 + `Template` 节点 | 用模板把结构化输出渲染成可读报告 |

---

## 2. 13 个智能体清单

所有智能体的 `id` 命名遵循 `builtin-doc-<场景>-<角色>` 模式，便于批量管理。

### 2.1 功能一：基础文档审查（3 个）

#### 2.1.1 `builtin-doc-review-format` — 格式审查智能体

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 对照公司模板规范检查字体/段落/编号/页边距等格式 |
| 知识库 | 公司模板库（`KB_COMPANY_TEMPLATE`，需在租户内手动配置） |
| 工具白名单 | `knowledge_search`、`grep_chunks`、`get_document_info` |
| temperature | `0.2`（格式判断需要确定性） |
| max_iterations | `20` |

**System prompt 关键要点**：
- 明确给出"格式规范清单"的占位（由 `KB_COMPANY_TEMPLATE` 提供）
- 输出必须为 JSON：`{ location, severity, rule_id, current_value, expected_value, suggestion }`
- 严禁改写原文，只标记问题

#### 2.1.2 `builtin-doc-review-proofread` — 文本校对智能体

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 错别字、标点、语法、不规范用语 |
| 知识库 | 通用词库 + 行业术语库（`KB_TYPO_DICT` + `KB_GLOSSARY`） |
| 工具白名单 | `knowledge_search`、`grep_chunks`、`get_document_info` |
| temperature | `0.1` |

**System prompt 关键要点**：
- 重点处理中文错别字、形近字（如"账/帐"、"做/作"）
- 行业术语必须以 `KB_GLOSSARY` 为准，不确定时**不报**（避免误改）
- 输出 JSON：`{ location, original, corrected, type: 'typo'|'punctuation'|'grammar'|'terminology', confidence }`

#### 2.1.3 `builtin-doc-review-consistency` — 一致性检查智能体

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 全文实体（金额/日期/编号/人员/项目）交叉比对 |
| 知识库 | 无（纯上下文比对） |
| 工具白名单 | `grep_chunks`、`list_knowledge_chunks`、`get_document_info` |
| temperature | `0.1` |

**System prompt 关键要点**：
- 必须先用规则引擎提取实体（金额统一为数字、日期统一为 ISO），再交 LLM 做语义判断
- 输出 JSON：`{ entity, occurrences: [{ chapter, paragraph, value }], conflict_type, severity }`
- 必须包含原文位置和所有出现处的引用

#### 2.1.4（调度）`builtin-doc-review-dispatcher` — 文档审查调度智能体

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 接收待审查文档，调度 3 个专业 Agent 并汇总报告 |
| 工具 | 无（纯编排） |
| temperature | `0.3` |

注：调度逻辑也可以完全用 Workflow 节点实现（不调 LLM），是否使用 Agent 视性能/成本取舍。

---

### 2.2 功能二：招投标文件智能比对（4 个）

#### 2.2.1 `builtin-bid-parser` — 招标解析智能体

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 结构化拆解招标文件，提取评分细则、资质要求、技术参数、废标条款 |
| 知识库 | 招标文件范本库（`KB_BID_TEMPLATE`，帮助识别章节类型） |
| 工具 | `knowledge_search`、`grep_chunks`、`get_document_info` |
| temperature | `0.1` |

**System prompt 关键要点**：
- 严格区分"资格审查项（K/O）"、"评分项（A/B/C）"、"废标条款"三类
- 输出 JSON：`{ qualifications: [...], scoring_table: [...], tech_specs: [...], knockout_clauses: [...] }`

#### 2.2.2 `builtin-bid-scorer` — 评分表结构化智能体

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 把评分表拆解为"评分项-分值-要求"三元组 |
| 知识库 | 无 |
| 工具 | `grep_chunks`、`list_knowledge_chunks` |
| temperature | `0.05`（结构性提取，最严格） |

**System prompt 关键要点**：
- 先用启发式规则按"评分项 / 分值 / 评审内容"三栏定位表格，再交 LLM 补全字段
- 输出 JSON：`{ items: [{ id, name, max_score, category: 'tech'|'commerce', requirement_text }] }`
- 强调"不能漏项"——所有评分项必须 1:1 对应

#### 2.2.3 `builtin-bid-comparator` — 逐项比对智能体

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 对每个评分项在投标文件中检索对应应答，判断满足/偏离/缺失 |
| 知识库 | 无（直接在两份文档间对比） |
| 工具 | `grep_chunks`、`list_knowledge_chunks` |
| temperature | `0.2` |

**System prompt 关键要点**：
- 输出 JSON：`{ item_id, status: 'fulfill'|'positive_deviation'|'negative_deviation'|'missing', bid_quote, evidence, confidence }`
- 强调"先召回相关章节、再做判断"，不要凭全文印象下结论

#### 2.2.4 `builtin-bid-risker` — 风险标注智能体

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 对缺失项 / 负偏离项标注风险等级 + 修改建议 |
| 知识库 | 历史废标案例库（`KB_BID_HISTORY`） |
| 工具 | `knowledge_search`、`grep_chunks` |
| temperature | `0.3` |

**System prompt 关键要点**：
- 风险等级：`high`（废标风险） / `medium`（扣分风险） / `low`（表述建议）
- 必须引用历史类似案例，给出可操作的修改建议

#### 2.2.5（调度）`builtin-bid-dispatcher` — 招投标比对调度智能体

可省略，调度逻辑由 Workflow `Switch` + `VariableAggregator` 完成。

---

### 2.3 功能三：知识文档自动生成（3 个）

#### 2.3.1 `builtin-gen-plan` — 方案生成智能体

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 根据新项目参数检索历史标书/模板，生成技术方案初稿 |
| 知识库 | 历史标书库（`KB_BID_HISTORY`） + 通用模板库（`KB_DOC_TEMPLATE`） |
| 工具 | `knowledge_search`、`grep_chunks`、`list_knowledge_chunks`、`get_document_info` |
| temperature | `0.5`（生成允许一定创造性） |

**System prompt 关键要点**：
- **绝对禁止编造**：业绩、资质、人员、参数等关键信息用 `{{待填:xxx}}` 占位符
- 必须从知识库检索至少 3-5 份历史相似文档
- 输出结构化：章节标题 + 占位符 + 引用溯源（哪份历史文档的哪一节）

#### 2.3.2 `builtin-gen-report` — 汇报材料生成智能体

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 面向政府的标准化汇报材料 |
| 知识库 | 政府汇报模板库（`KB_GOV_TEMPLATE`） + 历史汇报材料（`KB_REPORT_HISTORY`） |
| 工具 | `knowledge_search`、`grep_chunks`、`list_knowledge_chunks` |
| temperature | `0.6` |

**System prompt 关键要点**：
- 文风偏官方、严谨
- 必须遵循目标模板的章节顺序和字数要求
- 数据必须可溯源

#### 2.3.3 `builtin-gen-reviewer` — 校核智能体（生成安全阀）

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 对生成内容做事实核查、合规检查、格式校验 |
| 知识库 | 全部知识库（交叉验证） |
| 工具 | `knowledge_search`、`grep_chunks`、`query_knowledge_graph` |
| temperature | `0.1` |
| max_iterations | `15` |

**System prompt 关键要点**：
- 三项强制检查：① 关键事实是否在 KB 中可证 ② 是否符合模板/规范 ③ 是否有 `{{待填}}` 占位符未填
- 输出 `pass` 或 `fail`，失败时必须给出具体修改指令
- **硬约束**：不通过则工作流的 Switch 节点不会进入"人工确认"环节

---

### 2.4 功能四：内部知识库构建与应用（3 个）

#### 2.4.1 `builtin-kb-ingest` — 文档解析与入库智能体

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 将非结构化文档解析、分块、向量化、写入知识库 |
| 知识库 | 无（写入知识库） |
| 工具 | `get_document_info`、`list_knowledge_chunks` |
| temperature | `0.1` |

**System prompt 关键要点**：
- 提取元数据：项目名称、专业类别、问题类型、阶段、文档类型
- 调用知识库的 `create_chunk` / 写入 metadata
- 输出：`{ chunks_created, metadata_summary }`

#### 2.4.2 `builtin-kb-associator` — 知识关联智能体（后台异步）

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 识别文档间的关联关系（同专业、同问题类型、同项目阶段） |
| 知识库 | 全部知识库 |
| 工具 | `knowledge_search`、`query_knowledge_graph`、`list_knowledge_chunks` |
| temperature | `0.2` |

**System prompt 关键要点**：
- 调用知识图谱查询，建立关联三元组 `(doc_a, related_to, doc_b)`
- 输出：`{ associations: [{ source, target, relation_type, confidence }] }`

#### 2.4.3 `builtin-kb-advisor` — 主动提醒智能体

| 字段 | 取值 |
|------|------|
| agent_mode | `smart-reasoning` |
| agent_type | `custom` |
| 角色 | 新项目启动时，检索关联历史问题，生成"注意事项清单" |
| 知识库 | 全部知识库 |
| 工具 | `knowledge_search`、`query_knowledge_graph`、`grep_chunks` |
| temperature | `0.3` |

**System prompt 关键要点**：
- 输入：新项目信息（专业、规模、阶段、关键参数）
- 输出结构化清单：`{ warnings: [{ category, history_ref, lesson, severity }] }`
- 必须给每条警告挂历史文档引用（点击可跳转）

---

## 3. 工作流设计

每个功能一个工作流，DSL JSON 见 [02-workflow-dsl.md](./02-workflow-dsl.md)。下面是节点拓扑概要：

### 3.1 `wf-doc-review`（文档审查）

```
Start
  └─ LLM (Dispatcher, agent=builtin-doc-review-dispatcher)
       ├─ Retrieval (kb_ids=[KB_COMPANY_TEMPLATE, KB_TYPO_DICT, KB_GLOSSARY])
       │   └─ LLM (Format Reviewer, agent=builtin-doc-review-format)
       ├─ LLM (Proofreader, agent=builtin-doc-review-proofread)
       ├─ LLM (Consistency Checker, agent=builtin-doc-review-consistency)
       └─ VariableAggregator (merge 3 results)
            └─ LLM (Synthesizer, 用 default_kb 模板生成可读报告)
                 └─ Answer (render 报告模板)
```

### 3.2 `wf-bid-comparison`（招投标比对）

```
Start
  ├─ LLM (Bid Parser, agent=builtin-bid-parser)
  │    └─ Retrieval (kb_ids=[KB_BID_TEMPLATE])
  ├─ LLM (Scorer, agent=builtin-bid-scorer)
  │    └─ VariableAggregator (scoring_items[])
  │         └─ HTTP loop → LLM (Comparator, agent=builtin-bid-comparator)  ×N
  │              └─ VariableAggregator (compare_results[])
  │                   └─ LLM (Risker, agent=builtin-bid-risker)
  │                        └─ Retrieval (kb_ids=[KB_BID_HISTORY])
  │                             └─ Answer (report)
```

> 注：循环逻辑在 Workflow DSL 中通过 `Switch` + `VariableAggregator` 模拟；高频调用场景建议后端代码层用 `for` 循环驱动 `POST /workflows/:id/runs`。

### 3.3 `wf-doc-generation`（生成 + 校核循环）

```
Start
  └─ LLM (Plan Generator, agent=builtin-gen-plan)
       └─ Retrieval (kb_ids=[KB_BID_HISTORY, KB_DOC_TEMPLATE])
            └─ Switch (reviewer.passed?)
                 ├─ true → LLM (Report Generator, agent=builtin-gen-report)
                 │          └─ Retrieval (kb_ids=[KB_GOV_TEMPLATE])
                 │               └─ Switch (reviewer.passed?)
                 │                    ├─ true → Answer (交付待人工确认)
                 │                    └─ false → back to Plan Generator (loop<3)
                 ├─ false → LLM (Reviewer, agent=builtin-gen-reviewer)
                 │           └─ Retrieval (all KBs)
                 └─ default → Answer (超过 3 轮仍不通过，输出失败原因)
```

### 3.4 `wf-kb-ingest` & `wf-kb-advisory`

**入库工作流**：

```
Start
  └─ LLM (Ingest Agent, agent=builtin-kb-ingest)
       ├─ DataOps (写 metadata 到 structured KV)
       ├─ HTTP (调 /knowledge/chunks 批量写入)
       └─ Answer (入库摘要)
       (异步) → LLM (Associator, agent=builtin-kb-associator)
                  └─ query_knowledge_graph (写入关联边)
```

**主动提醒工作流**：

```
Start (新项目信息)
  └─ LLM (Advisor, agent=builtin-kb-advisor)
       ├─ Retrieval (all KBs)
       └─ Answer (注意事项清单 + 历史引用)
```

---

## 4. Prompt 工程通用要点

| 维度 | 建议 |
|------|------|
| 输出格式 | 始终要求 JSON 输出（`{...}`），便于 Workflow 后续节点解析 |
| 引用溯源 | 涉及事实/参数/案例必须带 `[来源: doc_name §章节]` 格式 |
| 占位符 | 生成类 Agent 必须用 `{{待填:xxx}}` 标记无法核实的项 |
| 不编造硬约束 | 在 system_prompt 中重复至少 2 次："没有依据的内容必须留占位符" |
| 中文处理 | 校对/一致性 Agent 的 prompt 中显式列出易错字、异形词、政府公文规范 |
| 温度设定 | 格式/校对/一致性类 = `0.1`；生成类 = `0.5–0.6`；摘要类 = `0.3` |

## 5. 一致性 / 风险标注的设计技巧**

校核 Agent、风险标注 Agent 的 system_prompt 中应明确：
- 输出"判定 + 证据 + 修改指令"三段式，不要只给结论
- 风险等级枚举值要固定，方便前端展示和筛选
- 强制每条结论带 `confidence`（0–1），低于阈值的不输出，由人工补充

---

## 6. 与现有功能的边界

| 已有功能 | 与新功能的关系 |
|---------|--------------|
| `builtin-quick-answer` | 不冲突；新功能是"专业角色"，老的是"通用问答" |
| `builtin-data-analyst` | 不冲突；新功能偏文档/标书，新老互不替代 |
| `builtin-wiki-researcher` / `builtin-wiki-fixer` | 部分场景可复用：一致性检查的实体抽取可借鉴 wiki 的页面导航思路 |
| 现有 `kb_*` 知识库 | 新功能复用同一套 KB 平台，**新建**专用 KB：`KB_COMPANY_TEMPLATE`、`KB_BID_HISTORY` 等 |

---

## 7. 上线节奏

详见 [04-rollout.md](./04-rollout.md)。简版：

1. **第 1 周**：13 个 Agent YAML 入库 + 4 个工作流 JSON 通过 API 导入
2. **第 2-3 周**：在测试租户内用历史样本验证 4 个工作流
3. **第 4 周**：补 KB 内容（模板库、术语库、历史案例库），调优 prompt
4. **第 5 周**：小范围灰度 → 全量
