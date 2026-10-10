# 角色手册 · Contributor（编辑）

> **对应后端角色**：`tenant_members.role = 'contributor'`（level 20）
> **典型用户**：领域专家、产品经理、运营、HR（写自己负责的 KB）、写智能体的业务人员
> **核心心智**：**您是「创作者」**。能创建 KB / Agent / 上传文档 / 调试 Agent，但不能动基础设施和团队成员。

---

## 1. 您的能力地图

### ✅ 您能做的事

| 操作 | 中间件门 | 说明 |
|---|---|---|
| 创建知识库（自己名下） | `g.Contributor()` | KB 类型：Document / FAQ / Wiki |
| 创建智能体（自己名下） | `g.Contributor()` | 自定义 Agent |
| 创建工作流（自己名下） | `g.Contributor()` | Dify-parity 节点编排 |
| 创建会话 | `g.Contributor()` | `/platform/creatChat` |
| 上传文档 / URL / 文件夹到自己 KB | `g.OwnedKBOrAdmin()` | 触发解析任务 |
| 编辑自己创建的 KB / Agent / Workflow | `g.OwnedKBOrAdmin()` | 自己是 `creator_id` |
| 复制 KB / Agent（成为副本的创建者） | `CopyKnowledgeBase` | 副本默认名「原名 的副本」 |
| 启用 / 关闭 Agent 的 `runnable_by_viewer` | `g.OwnedKBOrAdmin()` | 让 Viewer 也能跑 |
| 浏览 / 检索他人资源 | Viewer 默认放行 | 只读 |
| 修改个人资料 | 自己 | — |

### ⚠️ 部分能做（条件性）

| 操作 | 条件 |
|---|---|
| 编辑他人 KB / Agent | 您是该资源的 `creator_id`，**或** Admin+ 帮您提升 |
| 启用 KB 共享 | KB 创建者可以启用 Owner 给出的共享策略 |

### ❌ 您不能做的事

| 操作 | 谁能做 |
|---|---|
| 配置基础设施（模型 / 向量库 / MCP / 沙箱 / Skill / 解析器 / 存储 / IM / 记忆 / Web 搜索） | Admin+（`g.Admin()`） |
| 邀请 / 移除 / 修改成员 | Owner+（`g.Owner()`） |
| 修改空间配置（空间名、quota、API Key） | Owner+ |
| 批准待审批用户 | Admin+ |
| 查看审计日志 | Admin+ |
| 删除工作区 | Owner+ |

---

## 2. 典型工作流

### 场景 A：建立您的第一个知识库

> **目标**：把「客服 FAQ」整理成可检索的知识库。

1. 进入 `/platform/knowledge-bases`，点击右上角「**+ 新建知识库**」（Contributor+ 显示）
2. 填写：
   - **名称**：例如「客服 FAQ 2025」
   - **类型**：FAQ
   - **描述**：写清楚这个 KB 的范围
   - **嵌入模型**：默认工作区配置（Admin 设置过）
   - **向量库 / 存储后端**：默认工作区配置
   - **共享策略**：默认私有
3. 点击「**创建**」→ 自动跳转到 KB 详情页
4. 进入「**FAQ**」Tab，开始添加问答对
5. 也可以在「**文档**」Tab 上传文件（FAQ / Markdown）

### 场景 B：上传文档到 KB

> **前置**：您是 KB 的创建者，或 KB 是您所在部门的公共 KB。

1. 进入 KB 详情页
2. 点击「**上传**」按钮（三种来源：文件 / URL / 文件夹）
3. 选择源：
   - **文件**：拖拽或点击上传（PDF / Word / Excel / 图片 / EPUB / XMind …）
   - **URL**：粘贴 URL，自动抓取
   - **文件夹**：批量上传
4. 上传后**自动触发解析**（派发到 DocReader gRPC，端口 50051）
5. 在文档列表查看解析状态（pending / parsing / done / failed）
6. 解析完成后**自动切片 + 嵌入**，可立即检索

### 场景 C：调 KB 解析参数

> KB 创建者可以修改解析配置（`g.OwnedKBOrAdmin()`）。

1. 在 KB 详情页右上角点击「⚙ 设置」 → 「**解析设置**」
2. 调整：
   - 解析器（默认 / MinerU / MarkItDown / OpenDataLoader）
   - 切片策略（按段落 / 固定窗口 / 语义切片）
   - 切分粒度、重叠区间
4. 「**保存**」→ 对新上传的文档生效；对已有文档可点「**重新解析**」

> **更多 KB 配置项**（13 个子面板）：详见 [modules/01-knowledge-base.md § 5](../modules/01-knowledge-base.md)

### 场景 D：创建您的第一个 Agent

> **目标**：写一个绑定「客服 FAQ」KB 的客服 Agent。

1. 进入 `/platform/agents`，点击右上角「**+ 新建智能体**」
2. 填写：
   - **名称**：「客服 Bot」
   - **类型**：`smart-reasoning`（推荐）
   - **系统提示**：从 `config/agent_type_presets.yaml` 自动填入推荐
3. 进入「**知识库**」标签，选择「客服 FAQ 2025」
4. 进入「**工具**」标签，按需启用：
   - `knowledge_search`（KB 检索）— **默认开**
   - `grep_chunks`（按关键词搜索块）
   - 其他工具按需
5. 进入「**对话策略**」标签，调整 temperature / max_tokens
6. 点击「**保存**」
7. 点击右上角「**测试**」按钮，在右侧对话面板测试 Agent

### 场景 E：把 Agent 标记为 Viewer 可跑

1. 在 Agent 详情页找到「**允许 Viewer 运行**」开关（`runnable_by_viewer`）
2. 打开后，Viewer 角色也能跑这个 Agent

### 场景 F：复制别人的 KB / Agent

1. 在 KB / Agent 列表，找到「⋮」菜单
2. 点击「**复制**」→ 生成「原名 的副本」
3. 您成为副本的创建者，可编辑

---

## 3. 关键页面详解

### 3.1 KB 列表（`/platform/knowledge-bases`）

| 操作 | 说明 |
|---|---|
| 「+ 新建知识库」 | Contributor+ 可见 |
| 「⋮ 复制」 | 创建副本，您成为创建者 |
| 「⋮ 启用共享」 | Owner 配置共享策略后，您可启用 |
| 「⋮ 收藏」 | 收藏到左侧「我的」 |

### 3.2 KB 详情（`/platform/knowledge-base/:kbId`）— 您作为创建者

| Tab | 操作 |
|---|---|
| 文档 | 上传、批量打标签、移动文件夹、删除（`OwnedKBOrAdmin`） |
| FAQ | 增删改条目（`OwnedKBOrAdmin`） |
| Wiki | 浏览 + 编辑 Markdown（`OwnedKBOrAdmin`），查看修订历史、回滚 |
| 设置 | 13 个子面板（解析 / 切片 / 向量库 / 存储 / 模型 / 高级 / 图谱 / 共享 / 活动审计 / 数据源） |

### 3.3 Agent 编辑器（`/platform/agents` 详情）

左侧导航分组：

| 分组 | 用途 |
|---|---|
| 基础 | 名称、描述、type、display_group、tags |
| 提示词 | system prompt + welcome message + 推荐问题 |
| 模型 | temperature / max_tokens / 模型选择 |
| 知识库 | 选择 KB + 选择具体文档 + capability 过滤 |
| 工具 | 知识检索 / 文件操作 / 数据 / MCP / Skill / 多模态 |
| 技能 | 启用 Skill 列表（`SkillsEnabled` + `SkillsSelectionMode`） |
| 沙箱 | 选择沙箱后端（Docker / E2B / Cube） |
| 对话策略 | temperature / max_tokens / follow-up |
| 问题建议 | 推荐问题管理 |

---

## 4. 创建资源的「标准流程」清单

| 步骤 | KB | Agent | Workflow |
|---|---|---|---|
| 1. 进入模块入口 | `/platform/knowledge-bases` | `/platform/agents` | `/platform/workflow` |
| 2. 点新建 | 「+ 新建知识库」 | 「+ 新建智能体」 | 「+ 新建工作流」 |
| 3. 填元信息 | 名称 / 类型 / 描述 | 名称 / type / 描述 | 名称 / 描述 |
| 4. 配置核心 | 解析 / 切片 / 向量库 | 提示词 / 工具 / KB / 沙箱 | 节点编排 / 变量 / DSL |
| 5. 保存 | 保存 | 保存 | 保存（自动保存） |
| 6. 测试 | 上传文档 → 试检索 | 「测试」按钮 → 对话 | 「运行」按钮 → 看节点输出 |
| 7. 发布（Agent / Workflow） | 标记 `runnable_by_viewer`（可选） | 「发布」 | 「发布」（冻结 DSL） |

---

## 5. 常见问题（FAQ）

### Q1：为什么我编辑不了别人的 KB？
A：您只对自己创建的 KB 有写权限（`g.OwnedKBOrAdmin()`）。请 Owner 联系 Admin 把您提升角色，或者复制那个 KB 改成自己的。

### Q2：文档上传后一直「解析中」？
A：可能 DocReader 服务异常。请让 Admin 检查：
- DocReader gRPC 服务是否启动（端口 50051）
- Redis / Postgres 连接是否正常
- 在 `docker logs WeKnora-docreader` 看解析日志
详见 [modules/01-knowledge-base.md § 4 排错](../modules/01-knowledge-base.md)。

### Q3：我能修改工作区的全局模型配置吗？
A：不能。模型配置是 **Admin 限定**（`g.Admin()`）。请联系 Admin。

### Q4：为什么我新建 Agent 时某些工具看不到？
A：某些工具受 `agent_type_presets.yaml` 的 allowlist 控制。换 `agent_type` 会自动切换工具集。

### Q5：怎么把 Agent / KB 共享给其他工作区？
A：跨工作区共享由 Owner 启用后，您才能看到共享设置。详见 [roles/04-owner.md § 4](./roles/04-owner.md)。

---

## 6. 何时升级到 Admin

如果您的工作越来越大，建议联系 Owner 升级到 **Admin**：

| 场景 | 需要 Admin |
|---|---|
| 您开始负责整个部门的 KB 治理 | ✅ |
| 您要接入新的 LLM / 向量库 / IM 通道 | ✅ |
| 您要处理待审批用户 | ✅ |
| 您要管理 MCP 服务 / Skill 安装 | ✅ |
| 您需要查看审计日志 | ✅ |

---

## 7. 进一步阅读

- [modules/01-knowledge-base.md](../modules/01-knowledge-base.md) — 知识库完整功能
- [modules/02-agent.md](../modules/02-agent.md) — 智能体完整功能
- [modules/03-workflow.md](../modules/03-workflow.md) — 工作流完整功能
- [modules/04-session-chat.md](../modules/04-session-chat.md) — 会话与测试
- [modules/07-wiki.md](../modules/07-wiki.md) — Wiki 模式编辑
- [README.md § 角色矩阵](../README.md#角色矩阵速查)