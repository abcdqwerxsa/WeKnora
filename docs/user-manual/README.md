# LuoSA 用户手册

欢迎使用 **LuoSA** —— 企业级 RAG + Agent 平台。本手册面向不同角色的用户，按**权限分层**组织内容：先看总览明确能力边界，再按您所属角色阅读对应章节。

> LuoSA 把「文档解析 → 知识库 → Agent 推理 → 工作流编排」做成可自托管的开源平台。后端 Go，前端 Vue3，文档解析 Python gRPC（DocReader），MCP 服务器 Node。

---

## 手册目录

### 通用（所有角色必读）

- [01 平台概览](./01-overview.md) — LuoSA 是什么、能做什么、典型使用场景
- [02 快速上手](./02-quickstart.md) — 5 分钟从登录到第一次对话

### 按角色分章（核心交付 —— 按您的角色阅读）

| 角色 | 文档 | 一句话定位 |
|---|---|---|
| Viewer（访客 / 业务人员） | [roles/01-viewer.md](./roles/01-viewer.md) | 只读、对话、跑公开 Agent |
| Contributor（编辑） | [roles/02-contributor.md](./roles/02-contributor.md) | 创建知识库与智能体 |
| Admin（租户管理员） | [roles/03-admin.md](./roles/03-admin.md) | 配置租户基础设施 |
| Owner（所有者） | [roles/04-owner.md](./roles/04-owner.md) | 管理成员与空间 |

### 功能模块参考（按需查阅）

| 模块 | 文档 | 适用范围 |
|---|---|---|
| 知识库（KB） | [modules/01-knowledge-base.md](./modules/01-knowledge-base.md) | Contributor+ |
| 智能体（Agent） | [modules/02-agent.md](./modules/02-agent.md) | Contributor+ |
| 工作流（Workflow） | [modules/03-workflow.md](./modules/03-workflow.md) | Contributor+ |
| 会话与聊天（Session / Chat） | [modules/04-session-chat.md](./modules/04-session-chat.md) | 所有角色 |
| 技能与 MCP（Skills / MCP） | [modules/05-skills-mcp.md](./modules/05-skills-mcp.md) | Admin+ |
| 系统设置（Settings） | [modules/06-system-settings.md](./modules/06-system-settings.md) | 视 section 而定 |
| Wiki 模式 | [modules/07-wiki.md](./modules/07-wiki.md) | Contributor+ |

---

## 角色矩阵（速查）

LuoSA 使用「**角色 → 路由门**」强矩阵模式（`internal/router/rbac.go`）。租户内 4 级角色。下表每一行都对应一个后端中间件强制门。

| 能力 | Viewer | Contributor | Admin | Owner |
|---|:---:|:---:|:---:|:---:|
| 浏览 / 检索 KB 与 Agent | ✅ | ✅ | ✅ | ✅ |
| 聊天检索（RAG / Agent） | ✅ | ✅ | ✅ | ✅ |
| 运行 `runnable_by_viewer=true` 的 Agent | ✅ | ✅ | ✅ | ✅ |
| 接收邀请 / 主动加入空间 | ✅ | ✅ | ✅ | ✅ |
| 创建 KB / Agent（自己名下） | ❌ | ✅ | ✅ | ✅ |
| 编辑他人 KB / Agent | ❌ | ❌ | ✅ | ✅ |
| 配置基础设施¹ | ❌ | ❌ | ✅ | ✅ |
| 批准待审批用户 | ❌ | ❌ | ✅ | ✅ |
| 查看审计日志 | ❌ | ❌ | ✅ | ✅ |
| 邀请 / 移除 / 修改成员 | ❌ | ❌ | ❌ | ✅ |
| 修改空间配置 / 租户 API Key | ❌ | ❌ | ❌ | ✅ |
| 转让所有权 / 删除空间 | ❌ | ❌ | ❌ | ✅ |

¹ 基础设施 = 模型 / 向量库 / MCP / 沙箱 / Skill / 解析器 / 存储 / IM 通道 / 长期记忆 / Web 搜索。

> ⚠️ **重要**：本手册中所有「角色能做什么」均与后端中间件 `RequireRole / RequireOwnershipOrRole` 严格对齐。**前端 UI 仅作展示，最终授权以后端为准**——前端按钮可见并不等于一定能成功调用接口。

---

## 角色权限详解

### Viewer（访客）—— 阅读者
**最低权限角色。只能读，不能创建。**

- **典型用户**：业务分析师、客服、市场调研、销售支持、新员工（只读访问部门资料）。
- **典型场景**：检索已有知识库、与 Bot 对话、跑已经发布的 Agent、查看成员名单与审计日志。
- **不能做的事**：创建 KB / Agent、修改任何配置、上传文档、邀请成员。
- 详见 [roles/01-viewer.md](./roles/01-viewer.md)。

### Contributor（编辑）—— 创作者
**可创建资源并编辑自己创建的资源。**

- **典型用户**：领域专家、产品经理、运营、HR（写自己负责的 KB）。
- **典型场景**：建立某个产品线的 KB、写一个客服 FAQ Agent、上传并解析文档、配置 KB 级别的解析/切片/向量库。
- **不能做的事**：编辑他人资源、修改基础设施、邀请成员、配置模型/MCP/IM。
- 详见 [roles/02-contributor.md](./roles/02-contributor.md)。

### Admin（租户管理员）—— 基础设施运维
**管理租户的所有基础设施配置。**

- **典型用户**：IT 运维、平台管理员（公司内部）、算法工程师。
- **典型场景**：接入 LLM API、配置向量库、配置 IM 集成、管理 MCP 服务、批准待审批用户、查看审计日志。
- **不能做的事**：邀请成员、修改空间配置、管理租户 API Key、删除空间。
- 详见 [roles/03-admin.md](./roles/03-admin.md)。

### Owner（所有者）—— 空间负责人
**完全控制租户，包括成员与空间配置。**

- **典型用户**：团队负责人、部门主管、空间创建者。
- **典型场景**：邀请新成员、修改成员角色、创建 share-link、管理租户 API Key、修改空间名与配置、删除空间。
- 详见 [roles/04-owner.md](./roles/04-owner.md)。

---

## 跨租户共享与 API Key

LuoSA 除了人类用户，还有两个独立的鉴权维度：

- **跨工作区（Organization）**：把 KB 共享给组织时按 `OrgMemberRole`（admin / editor / viewer）裁剪权限。这是**与租户角色独立**的第二层 RBAC，由 `internal/types/organization.go` 定义。
- **API Key 主体**：不属于人类用户，携带 `APIKeyCapability` 列表与 KB allow-list。默认拒绝未在 `apiKeyRouteAuthorizer` 注册的路由（声明式 + 默认拒绝）。详见 [modules/06-system-settings.md](./modules/06-system-settings.md)。

---

## 关键不变式（手册通则）

1. **最后一位 Owner 不可降级**：尝试把最后一位 active Owner 改成非 Owner 会返回 `409 + ErrLastOwner`。
2. **孤儿租户自愈**：当租户无任何 active 成员、用户登录的是自己的 home tenant 时，自动晋升为 Owner（迁移 000043）。
3. **公开注册默认未审批**：自注册用户默认 `is_approved=false`，必须由 Admin+ 在「待审批」队列中批准后才能登录；OIDC 用户免审批。
4. **Owner 不能退出自己创建的空间**（如果自己是最后一位 Owner）。
5. **共享 Agent 的写入工具被过滤**：跨组织共享的 Agent 会自动过滤写操作工具，确保只读语义。

---

## 阅读路径建议

| 您是谁 | 推荐阅读顺序 |
|---|---|
| 第一次使用 / 业务人员 | 01 → 02 → roles/01-viewer |
| 团队内创建者 | 01 → 02 → roles/02-contributor → modules/01 + 02 |
| 租户管理员 / IT | 01 → 02 → roles/03-admin → modules/06 |
| 空间 Owner | 01 → roles/04-owner |

---

## 反馈与修订

- 仓库 `docs/user-manual/` 目录下编辑对应 Markdown 文件。
- 角色权限矩阵与代码强对齐，**修改前请先确认后端中间件（`internal/router/rbac.go` 与 `internal/middleware/rbac.go`）已同步**。
- 提交前跑一遍 `make lint` 与 `make test`。