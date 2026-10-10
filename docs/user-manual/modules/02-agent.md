# 模块参考 · 智能体（Agent）

> **对应后端**：`internal/application/service/agent_service.go` + `custom_agent.go` + `internal/agent/tools/*`
> **对应前端**：`frontend/src/views/agent/*`
> **创建 / 编辑权限**：Contributor+ 创，自己名下 + Admin+ 改

---

## 1. 智能体的概念

LuoSA 的「智能体」= **绑定 KB + 工具 + 系统提示 + 模型的 AI 实体**，可以做单轮推理（ReAct 循环）。

```
用户提问
  ↓
Agent（system prompt + model）
  ↓
LLM 思考 → 调用工具 → 看结果 → 再思考 → ...
  ↓
最终回答（带引用）
```

---

## 2. 内置智能体（`config/builtin_agents.yaml`）

> 启动时由 `cmd/server/bootstrap.go` 自动 upsert 到 DB。所有工作区都有。

| ID | 中文名 | display_group | 用途 |
|---|---|---|---|
| `builtin-quick-answer` | 默认对话 | chat | 直接 LLM 聊天，不依赖 KB |
| `builtin-doc-review-proofread-chat` | 文档与招投标审查 | chat | 四维度审查 + 招投标对比，自动生成修订 docx |
| `builtin-doc-generation-chat` | 投标文件编写 | chat | 基于招标文件评分项生成应答，可转 docx |

每个内置 agent 支持 default / zh-CN / zh-TW / ja-JP / ko-KR 多语言 i18n。

---

## 3. Agent 类型与预设（`config/agent_type_presets.yaml`）

`smart-reasoning` 模式下选择 `agent_type` 时，预设会自动填入：

- 推荐系统提示
- 工具 allowlist
- KB 过滤规则（any_of / all_of / none_of，按 capabilities：vector / keyword / wiki / graph / faq）

| agent_type | 典型场景 |
|---|---|
| `customer-service` | 客服问答 |
| `document-qa` | 文档检索 + 回答 |
| `data-analysis` | 数据分析（需要 data_analysis 工具） |
| `code-review` | 代码审查 |
| `research` | 研究助手 |

---

## 4. Agent 编辑器（左导航分组）

`/platform/agents` → 点击 Agent → 编辑器打开，**左侧导航**包含：

| 分组 | 内容 |
|---|---|
| **基础** | 名称、描述、type、display_group、icon、tags |
| **提示词** | system prompt + welcome message + 推荐问题 |
| **模型** | temperature / max_tokens / 模型覆盖 |
| **知识库** | 选择 KB + 选择具体文档 + capability 过滤 |
| **工具** | 知识检索 / 文件操作 / 数据 / MCP / Skill / 多模态 |
| **技能** | 启用 Skill 列表（`SkillsEnabled` + `SkillsSelectionMode`） |
| **沙箱** | 选择沙箱后端（Docker / E2B / Cube）+ 网络策略 |
| **对话策略** | temperature / max_tokens / follow-up |
| **问题建议** | 推荐问题管理 |

> 前端代码：`frontend/src/views/agent/AgentEditorModal.vue`

---

## 5. Agent 能调用的工具（`internal/agent/tools/`）

### 5.1 知识检索

| 工具 | 作用 |
|---|---|
| `knowledge_search` | KB 混合检索（向量 + 关键词 + FAQ） |
| `grep_chunks` | 按关键词搜索 chunk |
| `list_knowledge_chunks` | 列出文档的所有 chunk |
| `get_document_info` | 查文档元信息 |
| `faq_snippet` | FAQ 精确匹配 |

### 5.2 文件操作

| 工具 | 作用 |
|---|---|
| `kb_get_file` | 拿 KB 内原始文件（PDF / DOCX / XLSX） |
| `kb_get_image` | 拿 KB 内的 figure / 表格 |

### 5.3 Wiki

| 工具 | 作用 |
|---|---|
| `wiki_*` | Wiki 模式专用工具（读取页面、列表、图谱） |

### 5.4 数据

| 工具 | 作用 |
|---|---|
| `data_analysis` | DuckDB SQL 查询 + 数据分析 |
| `database_query` | 数据库查询 |
| `data_schema` | 查数据 schema |

### 5.5 多模态

| 工具 | 作用 |
|---|---|
| `imagefetch/*` | 图片处理 |
| `image_markdown_output` | 图片转 Markdown |

### 5.6 其他

| 工具 | 作用 |
|---|---|
| `json_repair` | JSON 修复 |
| `normalize_id` | ID 规范化 |
| `output_budget` | 输出预算控制 |
| `mcp_tool` | MCP 工具调用 |
| `mcp_oauth` | MCP OAuth 中途授权 |
| `file_mutation_queue` | 文件变更队列 |

---

## 6. Agent 类型与 Skill / Sandbox 集成

### SkillsEnabled

- 启用哪些 Skill 在该 Agent 中可用
- 选择模式：
  - `all`：所有启用的 Skill
  - `whitelist`：仅白名单
  - `mention`：通过 `@Skill` 提及启用（按会话范围）

### SkillsSelectionMode

- `mention` 模式：`@doc-coauthoring` 在对话中被识别并启用

### Sandbox

- 后端：Docker（默认）/ E2B / Cube
- 网络策略：默认限制出网
- TTL：会话级沙箱生命周期

---

## 7. Agent 服务核心流程（`CreateAgentEngine`）

> 来源：`internal/application/service/agent_service.go` `CreateAgentEngine`

```
1. 注册 MCP 工具（pinned MCP service）
2. 解析 KB 与文档上下文（kbRetrievalCapabilities）
3. 注册沙箱文件工具（如果 sandbox 启用）
4. 注册 Skill 文件工具（read_skill / write_skill_file）
5. 注册沙箱 shell 工具（如果允许）
6. 解析 pinned Skill（loadInstalledSkillBundle）
7. userEnvResolver + skillEnvCapture（个人 / 工作区 env）
8. 注册最终工具集
9. ValidateConfig
10. 构建 ReAct 引擎
```

---

## 8. 共享 Agent

> 跨工作区共享 Agent，按 `OrgMemberRole` 裁剪。

- **Owner** 启用共享
- **共享 Agent 的写工具被过滤**（`filterSharedAgentWriteTools`）：跨工作区共享自动转为只读语义
- 在 Agent 详情 → 「**共享**」Tab 配置

---

## 9. Agent 的运行生命周期

```
用户发起对话
  ↓
会话层（session_agent_qa.go）
  ↓
CreateAgentEngine（懒构建）
  ↓
ReAct 循环
  ├─ LLM 思考 → 输出 tool_call
  ├─ 工具执行 → 返回结果
  ├─ 再次 LLM 思考 → ...
  └─ 最终回答
  ↓
流式输出到前端（chunked transfer / WebSocket）
  ↓
持久化到 session_messages
```

---

## 10. 调试与测试

### 10.1 Agent 编辑器内测试

- 点击「**测试**」按钮 → 右侧对话面板
- 输入问题 → 看流式输出
- 看调用的工具与参数（在面板中显示）

### 10.2 单步调试

- 部分工具支持「**单步运行**」（测试工具本身）
- 例如 `data_analysis` 工具：直接输入 SQL 测试

### 10.3 Langfuse 集成（观测性）

- 后端配置 `LANGFUSE_*` 环境变量
- Agent 运行 trace 上传到 Langfuse
- 看 LLM 延迟、token 用量、工具调用

> 详见 [`docs/Langfuse集成.md`](../../Langfuse集成.md)

---

## 11. 自定义 Agent 的最佳实践

1. **从 agent_type_presets 选类型**：自动填入推荐配置
2. **系统提示通用化**：不绑特定租户 / KB / 文档（遵循 [agent-prompt-contract](../../contributing/agent-prompt-contract.md)）
3. **KB 选择模式**：
   - 固定 1-2 个 KB：精确场景
   - 任意 KB（`any_of`）：开放场景
   - 按 capability 过滤：保证 KB 有 vector / faq / graph
4. **工具最小化**：只启用必需工具（减少 token 与误用）
5. **测试后再发布**：通过「测试」按钮多轮对话验证
6. **配置 follow-up**：自动推荐下一步问题
7. **共享时注意只读**：跨工作区共享会自动过滤写工具

---

## 12. 关键不变式

1. **共享 Agent 的写入工具被过滤**——确保只读语义。
2. **`SkillsSelectionMode=mention` 时**，必须在对话中 `@Skill_name` 才会启用。
3. **沙箱 shell_exec 仅在 sandbox 启用且 `SkillsEnabled` 配置允许时可用**。
4. **Agent Type 预设不可关闭**——选择 type 时会自动应用。
5. **内置 Agent 不能删除**——只能「**重置为内置**」。

---

## 13. 进一步阅读

- [modules/05-skills-mcp.md](./05-skills-mcp.md) — Skills / MCP 详细
- [modules/03-workflow.md](./03-workflow.md) — 工作流（多节点编排）
- [modules/04-session-chat.md](./04-session-chat.md) — 会话与聊天
- [roles/02-contributor.md](../roles/02-contributor.md) — 创建第一个 Agent
- [`config/builtin_agents.yaml`](../../../config/builtin_agents.yaml) — 内置 Agent 配置
- [`config/agent_type_presets.yaml`](../../../config/agent_type_presets.yaml) — Agent 类型预设
- [`docs/contributing/agent-prompt-contract.md`](../../contributing/agent-prompt-contract.md) — Prompt 合约