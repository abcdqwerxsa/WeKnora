# 知识库设计与入库规范

> 配套文档：`config/builtin_agents.yaml`（13 个新 Agent 引用了以下 KB ID）。

## 1. 推荐知识库清单

| KB ID | 名称 | 内容 | 必备 Agent |
|-------|------|------|-----------|
| `KB_COMPANY_TEMPLATE` | 公司文档模板库 | 各类公文模板（字体/段落/页边距/编号规范） | doc-review-format |
| `KB_TYPO_DICT` | 易错字 / 异形词库 | 中文常见错别字、形近字、规范异形词表 | doc-review-proofread |
| `KB_GLOSSARY` | 行业术语库 | 公司/行业术语对照表（如"账/帐"、"做/作"在公司内的约定） | doc-review-proofread |
| `KB_BID_TEMPLATE` | 招标文件范本库 | 历史招标文件，用于识别章节类型 | bid-parser |
| `KB_BID_HISTORY` | 历史标书库 + 废标案例库 | 历年中标/废标的投标文件、评标报告 | bid-risker、gen-plan |
| `KB_DOC_TEMPLATE` | 通用模板库 | 各类技术方案、商务方案模板 | gen-plan |
| `KB_GOV_TEMPLATE` | 政府汇报模板库 | 政府/上级汇报材料的格式与文风要求 | gen-report |
| `KB_COMPANY_HISTORY` | 公司项目历史知识库 | 历史项目文档、里程碑、设计审查单、经验反馈 | kb-advisor、kb-associator |
| `KB_QA` | 通用问答 KB | 行业基础知识、公司常见问题（可选） | 所有 Agent 兜底 |

> **建议**：在平台内为上述每个 KB 建立独立的 `KnowledgeBase`，开启「按租户隔离」。所有 13 个新 Agent 默认 `kb_selection_mode: all`，会让用户（租户管理员）在创建具体实例时挑选要使用的 KB。

## 2. 文档组织规范

### 2.1 入库前

- 单文件 ≤ 100 MB
- PDF / DOCX / DOC / TXT / MD 优先；扫描件确保 OCR 完成
- 每个文件保留**完整的章节标题层级**，便于 RAG 召回时分块

### 2.2 元数据

每个知识库应统一打以下元数据：

```yaml
doc_id: string            # 唯一 ID
title: string             # 文档标题
source: enum              # bid / review / template / case / project
project: string           # 项目代号（项目库必有）
phase: enum               # proposal / design / construction / acceptance / ops
category: string          # 专业类别（如「电气」「结构」「给排水」）
doc_type: enum            # template / spec / report / feedback / regulation
language: enum            # zh-CN / en-US
effective_date: date      # 生效日期
expire_date: date         # 失效日期（可选）
```

入库时由 `builtin-kb-ingest` 自动从内容中抽取项目/类别/阶段。

### 2.3 分块策略

| 文档类型 | 推荐 chunk_size | chunk_overlap |
|---------|----------------|---------------|
| 模板 / 规范 | 512 tokens | 64 |
| 标书 / 合同 | 768 tokens | 96 |
| 汇报材料 | 1024 tokens | 128 |
| FAQ / 问答 | 256 tokens | 32 |

> 模板由 `KnowledgeBase.ChunkingConfig` 设置，不需要在 Agent 侧配置。

## 3. 检索参数推荐

13 个 Agent 默认检索参数（已在 `builtin_agents.yaml` 中预设）：

```yaml
embedding_top_k: 10       # 向量召回 TopK
keyword_threshold: 0.3    # 关键词阈值
vector_threshold: 0.5     # 向量阈值
rerank_top_k: 10          # 重排 TopK
rerank_threshold: 0.3     # 重排阈值
enable_query_expansion: true
enable_rewrite: true
```

按场景微调：

| 场景 | embedding_top_k | rerank_threshold |
|------|-----------------|------------------|
| 错别字校对（KB_TYPO_DICT） | 5 | 0.1（要召回所有近字） |
| 一致性检查（无 KB） | n/a | n/a |
| 风险标注（KB_BID_HISTORY） | 15 | 0.5（要高 precision） |
| 主动提醒（KB_COMPANY_HISTORY） | 20 | 0.4 |

## 4. KB 准入 / 退出流程

| 阶段 | 操作 |
|------|------|
| 草稿 | 上传到草稿 KB，仅本人可见 |
| 评审 | 至少 2 名业务专家 + 1 名知识管理员 review |
| 上线 | 迁入正式 KB，开启租户级 RAG 检索 |
| 归档 | 设置 `expire_date`，前端按生效日期排序时自动降权 |

> **强烈建议**：所有模板 / 规范 / 历史案例类 KB 都设置「知识管理员」审核流程，避免错误文档污染检索。
