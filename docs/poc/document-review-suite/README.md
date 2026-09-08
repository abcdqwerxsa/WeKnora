# 文档智能审查 / 比对 / 生成 / 入库 套件

> 配套工作流 DSL 与内置 Agent 配置，覆盖「基础文档审查 / 招投标比对 / 知识文档生成 / 内部知识库构建」四大场景，全部基于 LuoSA 的 **Custom Agent + Workflow DSL + RAG 工具链** 落地，无需引入 LangGraph / Dify 等额外编排框架。

| 文档 | 内容 |
|------|------|
| [01-design.md](./01-design.md) | 总体架构、4 大功能的工作流与 13 个智能体的详细规格 |
| [02-workflow-dsl.md](./02-workflow-dsl.md) | 4 个工作流的 JSON DSL（可直接 POST `/workflows` 导入） |
| [03-knowledge-base.md](./03-knowledge-base.md) | 推荐知识库结构、文档入库规范、检索阈值 |
| [04-rollout.md](./04-rollout.md) | 上线路径、验收标准、风险与回退方案 |
| [05-deployment-guide.md](./05-deployment-guide.md) | 客户侧部署与模型配置指南（vLLM / 单点配置全员生效） |

## TL;DR

- **13 个内置 Agent**：4 个功能模块，每个模块 2–4 个专业 Agent
- **4 个工作流**：分别对应 4 个业务场景，可独立部署、单独调用
- **零外部依赖**：所有编排、检索、模型调用都在 LuoSA 平台内完成
- **人工复核节点（HITL）**：每个工作流的最终环节都是模板化的"待确认"输出，由人工决定是否采纳

## 平台能力映射

| 设计层 | 平台对应物 | 说明 |
|--------|----------|------|
| 智能体角色 | `CustomAgent`（`agent_type: custom`，`agent_mode: smart-reasoning`） | 通过 `config/builtin_agents.yaml` 注册，租户级共享 |
| 工作流 | `WorkflowDSL`（Start / LLM / Retrieval / Switch / Template / VariableAggregator / HTTP / DataOps） | 通过 POST `/workflows` 入库 |
| 知识库 | `KnowledgeBase` | 租户内独立，Agent 通过 `kb_selection_mode` 选择 |
| 工具 | `knowledge_search`、`grep_chunks`、`list_knowledge_chunks`、`query_knowledge_graph`、`get_document_info`、`database_query` 等 | 内置工具 + MCP 扩展 |
| 模型 | 租户级注册的任何 chat/embedding/rerank | Agent 配置 `model_id` 即可 |

## 4 个功能模块概览

| 模块 | Agent 数 | 工作流 | 关键校验 |
|------|---------|--------|---------|
| 基础文档审查 | 3 | `wf-doc-review` | 格式 / 错别字 / 数据一致性 |
| 招投标文件智能比对 | 4 | `wf-bid-comparison` | 评分项逐条对照 + 风险标注 |
| 知识文档自动生成 | 3 | `wf-doc-generation` | 生成-校核循环（最多 3 轮） |
| 内部知识库构建与应用 | 3 | `wf-kb-ingest` + `wf-kb-advisory` | 元数据 + 跨项目关联提醒 |
