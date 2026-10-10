# 模块参考 · 工作流（Workflow）

> **创建 / 编辑权限**：Contributor+ 创，自己名下 + Admin+ 改
> **发布**：需要 DSL 可编译

---

## 1. 工作流是什么

LuoSA 工作流是 Dify-parity 的可视化节点编排（DAG）。把多个 LLM 调用、检索、代码、Agent、HTTP、MCP、Switch 等节点拼成一个流程。

```
[Start] → [Retrieval] → [LLM] → [Switch] → [Answer]
                          ↓
                       [Code] → [HTTP] → [MCPTool]
```

**适用场景**：

- 多步骤业务流程（工单分类 → 查 KB → 调 HTTP → 写数据库）
- 复杂的 RAG（多次检索 + 重排 + 引用）
- 跨系统集成（多个 MCP 服务）
- 可视化的 Agent 编排

---

## 2. 编辑器（`/platform/workflow/:id/edit`）

Dify 风格画布编辑器。

### 2.1 工具栏

| 按钮 | 功能 |
|---|---|
| **变量抽屉** | 编辑工作流级变量 |
| **自动布局** | 自动整理节点位置 |
| **复制/粘贴** | 节点复制粘贴 |
| **撤销/重做** | 编辑历史 |
| **导入/导出 DSL** | JSON 格式 |
| **自动保存 / 保存** | 保存草稿 |
| **发布 / 重新发布** | 冻结 DSL 为 published 版本 |
| **运行抽屉** | 打开运行历史 |

### 2.2 节点组件

| 节点 | 用途 |
|---|---|
| **Start** | 入口节点 |
| **Answer** | 终端输出 |
| **LLM** | LLM 调用（流式 / 非流式） |
| **Retrieval** | KB 检索 + 重排 |
| **Switch** | 条件分支 |
| **QuestionClassifier** | 问题分类 |
| **ParameterExtractor** | 参数抽取 |
| **WebSearch** | 网络搜索 |
| **Iteration** | 迭代（遍历数组） |
| **Template** | 模板渲染 |
| **VariableAggregator** | 变量聚合 |
| **HTTP** | HTTP 请求 |
| **DataOps** | 数据操作（SQL / DuckDB） |
| **Code** | 代码节点（Python / JS / SQL） |
| **Agent** | 引用平台智能体 |
| **MCPTool** | MCP 工具调用 |

---

## 3. DSL（领域特定语言）

### 3.1 结构（v1）

```json
{
  "version": 1,
  "components": [
    { "id": "start", "component_name": "Start", "data": {...} },
    { "id": "retrieval", "component_name": "Retrieval", "data": {...} },
    { "id": "llm", "component_name": "LLM", "data": {...} },
    { "id": "answer", "component_name": "Answer", "data": {...} }
  ],
  "graph": {
    "edges": [
      { "source": "start", "target": "retrieval" },
      { "source": "retrieval", "target": "llm" },
      { "source": "llm", "target": "answer" }
    ]
  },
  "variables": [
    { "name": "user_query", "type": "string", "required": true }
  ]
}
```

### 3.2 校验规则

- `version == 1`
- `components` 非空
- 每个组件有 `component_name`
- 至少一个 Start 节点
- 边的 source / target 节点存在

### 3.3 导入 / 导出

- **导出**：工具栏 → 「**导出 DSL**」→ JSON 文件
- **导入**：工具栏 → 「**导入 DSL**」→ 选择 JSON 文件 → 合并到画布

---

## 4. 发布模型

| 状态 | 含义 |
|---|---|
| **draft** | 草稿，可以继续编辑 |
| **published** | 已发布，运行时会用这个版本的 DSL（冻结） |
| **archived** | 归档，不可编辑、不可运行 |

### 发布流程

1. 编辑工作流
2. 点击「**保存**」（保存当前 draft）
3. 点击「**发布**」：
   - 校验 DSL 是否可编译
   - 冻结 DSL 快照为 published 副本
   - `dslForRun` 切换到 published 版本

> 状态切换：`SetWorkflowStatus` 仅支持 draft ↔ archived；published 必须走 `PublishWorkflow`。

---

## 5. 运行与调度

| 入口 | 说明 |
|---|---|
| 编辑器「**运行**」按钮 | 调试运行（同步 / 异步可切换） |
| 「**运行抽屉**」 | 看运行历史 |
| 调度（Cron） | Cron 表达式触发（如 `0 9 * * 1-5` 工作日 9 点） |

**Checkpoint**：保存运行中间状态，支持 `ResumeWorkflowRun` 从断点恢复、`CancelWorkflowRun` 取消运行。

**事件流**：运行时通过 SSE / WebSocket 订阅节点级事件，看节点进度。

**单节点调试**：`RunWorkflowNode` 单独跑某个节点，输入手动指定。

---

## 6. 节点配置（典型）

### 6.1 LLM Node

字段：`model` / `system_prompt` / `user_prompt`（支持变量）/ `temperature` / `max_tokens` / `stream`

### 6.2 Retrieval Node

字段：`kb_ids` / `top_k` / `score_threshold` / `rerank` / `hybrid_weight`（向量 / 关键词权重）

### 6.3 Switch Node

多分支条件（变量比较 / 同基真值），默认分支兜底

### 6.4 Code Node

多语言：Python / JavaScript / SQL；输入上节点输出（变量），输出返回的 dict

### 6.5 HTTP Node

URL / Method / Headers / Body；鉴权：None / API Key / Bearer / Basic；响应：JSON / Raw / Stream

---

## 7. 最佳实践

1. **先画图，再写 DSL**：用画布拖拽，再导出 DSL 复用
2. **变量命名规范**：`snake_case`、明确含义
3. **避免过深嵌套**：Switch / Iteration 嵌套不要太深
4. **复用**：常用子图抽出来，用 Agent 节点调用
5. **测试单节点**：用「单节点调试」先验证逻辑
6. **发布前校验**：发布按钮会触发编译检查
7. **看事件流**：运行时用 SSE 订阅，看节点执行进度

---

## 8. 关键不变式

1. **状态 draft/archived 不能直接从 API 切到 published**——必须走 `PublishWorkflow`。
2. **运行使用 published DSL**——draft 修改不影响已运行实例。
3. **checkpoint 在 cancel/resume 间保持**。
4. **节点 ID 在工作流内唯一**——拖拽复制会自动加后缀。

---

## 9. 进一步阅读

- [modules/02-agent.md](./02-agent.md) — Agent 节点使用
- [modules/05-skills-mcp.md](./05-skills-mcp.md) — MCP 节点使用
- [`config/workflows/`](../../../config/workflows/) — 内置工作流示例
- [roles/02-contributor.md](../roles/02-contributor.md) — 创建第一个工作流