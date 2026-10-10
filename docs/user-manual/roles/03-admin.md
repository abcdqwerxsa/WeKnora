# 角色手册 · Admin（租户管理员）

> **对应后端角色**：`tenant_members.role = 'admin'`（level 30）
> **典型用户**：IT 运维、平台管理员（公司内部）、算法工程师、DevOps
> **核心心智**：**您是「基础设施运维者」**。配置模型 / 向量库 / MCP / IM / Skill / 沙箱 / 解析器 / 存储，批准待审批用户，查看审计，但不能邀请成员或改空间配置。

---

## 1. 您的能力地图

### ✅ 您能做的事

> **Admin 是所有 Contributor 能力的超集**，加上：

| 大类 | 操作 |
|---|---|
| **模型管理** | 创建 / 更新 / 删除 LLM、Embedding、Rerank、VLM、ASR 模型；测试连接 |
| **向量库** | 配置 Qdrant / pgvector / Weaviate / Milvus 等 |
| **存储后端** | 配置 MinIO / S3 / 腾讯 COS / 阿里 OSS |
| **解析器** | 选择 DocReader 解析模式、MinerU 端点 |
| **MCP 服务** | 创建 / 测试 / 配置 OAuth / 工具列表 |
| **Skill 安装** | 从 ClawHub / SkillHub / Git / Zip 安装工作区 Skill |
| **沙箱** | 配置 Docker / E2B / Cube 后端、网络策略、TTL |
| **网络搜索** | 配置 Web 搜索引擎（Bing / Tavily / Google …） |
| **长期记忆** | 配置 Memory 抽取策略、用户确认流程 |
| **聊天历史** | 配置会话保留策略 |
| **IM 集成** | 配置飞书 / 企微 / Slack / Telegram / Lark / 钉钉 / QQBot / Mattermost / 微信 / 云之家 |
| **Embed 通道** | 配置网站嵌入 widget |
| **待审批用户** | 批准 / 拒绝待审批用户 |
| **审计日志** | 查看工作区审计日志（`Admin()` 门） |
| **评估** | 跑评估数据集、查看 BLEU / ROUGE / Recall hit rate |
| **任务队列** | 查看失败任务、DLQ、worker pool 状态 |
| **环境变量** | 设置工作区 Env Var |
| **存储配额** | 应用默认存储配额（`ApplyDefaultStorageQuota`） |

### ⚠️ 部分能做（条件性）

| 操作 | 条件 |
|---|---|
| 编辑他人 KB / Agent / Workflow | `g.OwnedKBOrAdmin()` 直接覆盖 |

### ❌ 您不能做的事

| 操作 | 谁能做 |
|---|---|
| 邀请 / 移除 / 修改成员 | Owner+（`g.Owner()`） |
| 修改空间配置（空间名、quota、`is_joinable`、API Key） | Owner+ |
| 删除工作区 / 转让所有权 | Owner+ |

---

## 2. 待审批用户流程（高频操作）

> 自注册用户默认 `is_approved=false`。Admin+ 在「待审批」中处理。

### 第 1 步：进入待审批队列

- 路径：`/platform/settings?section=pending-users`（Admin+ 可见）
- 或 `frontend/src/views/system/PendingUsers.vue`

### 第 2 步：审查申请

每条申请显示：

| 字段 | 说明 |
|---|---|
| 用户名 | 注册时填写 |
| 邮箱 | 联系用 |
| 注册时间 | 时间线 |
| 申请附言（可选） | 用户填写的理由 |

### 第 3 步：批准 / 拒绝

- 点击「**批准**」→ 用户登录状态解锁，自动获得默认角色（contributor）
- 点击「**拒绝**」→ 用户被禁用

> 后端 API：
> - `GET  /api/v1/system/admin/pending-users`
> - `POST /api/v1/system/admin/users/:id/approve`
> - `POST /api/v1/system/admin/users/:id/reject`

---

## 3. 配置基础设施的标准流程

### 场景 A：接入新的 LLM（OpenAI / DeepSeek / Claude / 国产模型…）

1. 进入 `/platform/settings?section=models`
2. 点击「**+ 新建模型**」
3. 选择：
   - **提供商**：OpenAI / Azure OpenAI / Anthropic / DeepSeek / Qwen / Zhipu / Hunyuan / Doubao / Gemini / MiniMax / NVIDIA / Novita / SiliconFlow / OpenRouter / Requesty / LiteLLM / Ollama（`BUILTIN_MODELS.md`）
   - **类型**：Chat / Embedding / Rerank / VLM / ASR
4. 填入凭证（API Key / endpoint / model name）
5. 点击「**测试连接**」→ 验证可用
6. 保存 → 选择**作用范围**：
   - 全工作区默认
   - 特定 KB 覆盖
   - 特定 Agent / Workflow 覆盖
7. 在「使用详情」中查看消耗

> 详细模型清单见 `config/builtin_models.yaml` + `docs/BUILTIN_MODELS.md`。

### 场景 B：配置向量库

1. `/platform/settings?section=vectorstore`
2. 选择后端：pgvector（默认）/ Qdrant / Milvus / Weaviate
3. 填入连接信息
4. 测试 → 保存
5. KB 创建时默认使用工作区配置；可逐 KB 覆盖（详见 [modules/06 § 5](../modules/06-system-settings.md)）

### 场景 C：添加 MCP 服务

1. `/platform/settings?section=mcp`
2. 「**+ 新建 MCP 服务**」→ 选择：
   - **传输协议**：stdio / SSE / HTTP
   - **认证**：None / API Key / OAuth2
3. 填入端点 / 凭证
4. 点击「**测试连接**」→ 拉取工具列表
5. 保存 → 在 Agent 编辑器中可用

### 场景 D：安装工作区 Skill

1. `/platform/settings?section=skills`
2. 「**+ 安装技能**」→ 选择源：
   - **ClawHub**：官方市场
   - **SkillHub**：社区市场
   - **Git**：直接 Git URL
   - **Zip**：上传 zip 包
3. 选择安装路径（工作区级 vs 个人级）
4. 安装过程显示进度 → 完成
5. 在 Agent 编辑器中可用（`SkillsEnabled` + `SkillsSelectionMode`）

### 场景 E：配置沙箱

1. `/platform/settings?section=sandbox`
2. 选择后端：**Docker**（默认 / 集群）/ **E2B** / **Cube**
3. 填入 Docker socket / E2B API Key / Cube endpoint
4. 设置网络策略（默认限制出网）
5. 设置 TTL / 资源限制（CPU / 内存）
6. 保存 → Agent 调用 `shell_exec` / Skill 脚本时使用

### 场景 F：配置 IM 通道

1. `/platform/settings?section=integrations`
2. 选择平台：WeCom / Feishu / Lark（国际版）/ QQBot / Slack / Telegram / DingTalk / Mattermost / WeChat / Yunzhijia
3. 按各平台指南填入 App ID / Secret / Webhook
4. 测试 → 启用
5. 用户在 IM 中可直接与 Agent 对话

> IM 集成开发细节详见 `docs/IM集成开发文档.md`。

### 场景 G：配置数据源

> 把外部系统（飞书 Wiki / GitLab / Notion / Yuque / RSS / 腾讯 IMA 等）的文档自动同步到 KB。

1. KB 详情 → 设置 → 数据源
2. 「**+ 添加数据源**」
3. 选择 connector（Feishu Wiki / Feishu Drive / Lark / GitLab / 腾讯 IMA / Notion / Yuque / RSS）
4. 填入凭证 + 同步路径
5. 「**测试**」→ 「**启用**」→ 周期同步（可看同步日志）

---

## 4. 审计日志

- 路径：`/platform/settings?section=audit-log`（Admin+）
- 记录：
  - 成员变更（邀请、接受、拒绝、改角色、移除）
  - RBAC 拒绝事件（1 分钟滑动窗口去重）
  - 关键资源变更（KB 删除、Agent 发布、API Key 创建）
- 用途：合规审计、安全调查

---

## 5. 评估（Evaluation）

- 设置页 → 评估
- 创建评估数据集（QA 对 + 期望答案）
- 跑端到端评估：BLEU / ROUGE / Recall hit rate
- 对比不同 KB / 模型 / Agent 配置的效果

---

## 6. 任务队列与 Worker Pool

- Admin 可查看本工作区的失败任务、DLQ、worker pool 状态、per-model 并发
- 处置操作：失败任务重试 / 转 DLQ；DLQ 强制重跑 / 删除
- 详见 [`docs/worker-pool-governance.md`](../../worker-pool-governance.md)

---

## 7. 关键页面详解

### 7.1 设置模态（统一入口）

`/platform/settings` 通过 `?section=` 切换。所有 section + 最低角色：

| section | 最低角色 | 说明 |
|---|---|---|
| `general` | viewer | 通用偏好 |
| `models` | **admin** | LLM / Embedding / Rerank / VLM |
| `ollama` | admin | 本地 Ollama |
| `websearch` | admin | Web 搜索 |
| `chathistory` | admin | 聊天历史 |
| `vectorstore` | admin | 向量库 |
| `parser` | admin | 文档解析引擎 |
| `storage` | admin | 存储后端 |
| `sandbox` | admin | 沙箱 |
| `skills` | admin | Skills 工作区安装 |
| `mcp` | admin | MCP 服务 |
| `memory` | admin | 长期记忆 |
| `tenant` | viewer | 工作区信息（**Owner 才能编辑**） |
| `members` | viewer（快捷入口 owner） | 成员管理（**Owner 才能操作**） |
| `userprofile` | viewer | 个人资料 |
| `envvars` | viewer | 环境变量 |
| `pending-users` | **admin** | 待审批用户 |
| `audit-log` | admin | 审计日志 |
| `integrations` | viewer | 集成（API Key Playground + IM 落地页） |

---

## 8. 常见问题（FAQ）

### Q1：为什么我点不动「成员」管理？
A：成员管理是 **Owner 限定**（`g.Owner()`）。请联系 Owner 处理。

### Q2：我能调整 KB 的解析参数吗？
A：可以，作为 KB 创建者直接调（`OwnedKBOrAdmin`）；或者作为 Admin 修改工作区默认解析引擎。

### Q3：审批完用户后，他还需要做什么？
A：不需要。批准后用户状态解锁，下一次登录即可进入工作区。

### Q4：MCP 服务的 OAuth2 中途授权怎么处理？
A：在 MCP 设置里配置 OAuth2 后，Agent 在对话中调用需要授权的工具时，会自动触发 OAuth flow（mid-conversation），用户会看到授权弹窗。

---

## 9. 何时升级到 Owner

如果您负责整个工作区的治理（不仅是基础设施），请联系当前 Owner 升级：

| 场景 | 需要 Owner |
|---|---|
| 您要管理团队成员 | ✅ |
| 您要修改空间名 / 配额 | ✅ |
| 您要管理租户 API Key | ✅ |
| 您要删除 / 归档空间 | ✅ |

---

## 10. 进一步阅读

- [modules/05-skills-mcp.md](../modules/05-skills-mcp.md) — Skills 与 MCP 完整功能
- [modules/06-system-settings.md](../modules/06-system-settings.md) — 系统设置完整索引
- [`docs/BUILTIN_MODELS.md`](../../BUILTIN_MODELS.md) — 内置模型清单
- [`docs/IM集成开发文档.md`](../../IM集成开发文档.md) — IM 集成完整文档
- [`docs/数据源导入开发文档.md`](../../数据源导入开发文档.md) — 数据源开发文档
- [README.md § 角色矩阵](../README.md#角色矩阵速查)