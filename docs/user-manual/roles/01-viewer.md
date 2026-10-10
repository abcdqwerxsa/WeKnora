# 角色手册 · Viewer（访客 / 业务人员）

> **对应后端角色**：`tenant_members.role = 'viewer'`（level 10）
> **典型用户**：业务分析师、客服、市场调研、销售支持、新员工（只读访问部门资料）
> **核心心智**：**只读 + 对话 + 跑公开 Agent**。您不需要懂技术，会用浏览器提问就够了。

---

## 1. 您的能力地图

### ✅ 您能做的事

| 操作 | 入口 | 说明 |
|---|---|---|
| 浏览 / 检索所有 KB | `/platform/knowledge-bases` | 看文档列表、用 KB 试问 |
| 浏览所有 Agent | `/platform/agents` | 看 Agent 详情、描述、绑定 KB |
| 与任何已发布的 Agent 对话 | `/platform/chat/:chatid` | 流式输出、引用 popover |
| 跑 `runnable_by_viewer=true` 的 Agent | 同上 | 关键判断字段 |
| 查看工作区成员名单 | `/platform/settings?section=members` | 只读 |
| 接收邀请 / 主动加入其他工作区 | 邀请收件箱 / `/join?code=xxx` | — |
| 修改自己的个人资料 | `/platform/settings?section=userprofile` | 头像 / 密码 / 偏好 |
| 创建会话、保存对话历史 | `/platform/chat/:chatid` | 创建会话需要 `Contributor+`（`g.Contributor()`） |

> ⚠️ 一些 UI 操作（如「新建对话」）会要求 Contributor 角色。若您看到「403」或按钮隐藏，请联系管理员调整角色。

### ❌ 您不能做的事

| 操作 | 原因 / 谁能做 |
|---|---|
| 创建知识库 | Contributor+（`g.Contributor()`） |
| 创建智能体 | Contributor+（`g.Contributor()`） |
| 创建工作流 | Contributor+（`g.Contributor()`） |
| 上传文档到 KB | Contributor+ 且为 KB 的创建者（`g.OwnedKBOrAdmin()`） |
| 编辑任何 KB / Agent 配置 | Contributor 仅自己名下；Admin+ 可编辑所有 |
| 配置基础设施（模型、向量库、MCP 等） | Admin+ |
| 邀请成员 / 修改成员角色 | Owner+ |
| 删除空间 | Owner+ |

---

## 2. 典型工作流

### 场景 A：检索一个已有 KB

> **前置**：管理员已经创建并填充了一个 KB，例如「2025 销售手册」。

1. 进入 **知识库列表** `/platform/knowledge-bases`
2. 找到「2025 销售手册」，点击进入
3. 切换**文档列表 / 卡片**视图，按文件夹浏览或搜索文件名
4. 点击 KB 顶部「**试一试**」按钮 → 跳转到带 KB 上下文的对话
5. 提问，例如：「2025 年 Q3 销售额是多少？」
6. 在回答中点击引用 popover，**跳到原文位置**

### 场景 B：与 Agent 对话

1. 进入 **Agent 列表** `/platform/agents`
2. 找一个您感兴趣的 Agent（例如「客服 FAQ Bot」），点击进入
3. 点击「**开始对话**」按钮
4. 提问，回答会带引用
5. 可以**固定上下文**（pin）这条会话，反复追问

### 场景 C：接受一个邀请

1. 收到邀请邮件 / 链接 `https://.../join?code=xxx`
2. 点击链接，进入「**注册 / 接受**」页
3. 设置密码（如果还没账号），确认接受
4. 邀请方收到通知，您以指定角色加入工作区

### 场景 D：查看工作区成员

1. 进入 **设置** `/platform/settings?section=members`
2. 浏览成员名单（**只读**）
3. 如果需要联系 Owner，使用头像/邮箱

---

## 3. 关键页面详解

### 3.1 对话主界面（`/platform/chat/:chatid`）

| 区域 | 内容 |
|---|---|
| 顶部 | Agent 选择器、会话标题、参考资料按钮、设置菜单 |
| 主体 | 用户消息 / 助手消息（Markdown）、引用 popover、follow-up 建议 |
| 底部 | 输入框、上传附件、模型设置、IM 来源标识 |

**关注点**：

- **引用 popover**：每条回答下方的「[1] [2]」小标号，hover 看原文片段
- **参考资料抽屉**：查看完整引用列表（KB 名称、文档名、chunk ID）
- **附件**：可以上传临时文件（图片 / 文档），Agent 会自动读取
- **follow-up 建议**：回答末尾的「推荐下一步问题」

### 3.2 知识库详情（`/platform/knowledge-bases/:kbId`）

| 区域 | 内容 |
|---|---|
| 顶部 | KB 名、收藏、设置（只读）、共享 |
| 左侧 | 文件夹树 + 文档列表 / 卡片切换 + FAQ Tab + Wiki Tab |
| 主体 | 文档列表（解析状态、tags、来源、上传时间） |

**只读用户可见的元素**：

- 文档预览（hover 看摘要）
- 解析状态时间轴（pending / parsing / done / failed）
- 标签 / FAQ 条目
- Wiki 浏览、修订历史查看（**只读**，不能编辑）

### 3.3 Agent 列表（`/platform/agents`）

| 区域 | 内容 |
|---|---|
| Tab | 全部 / chat / workflow（按 `display_group` 分类） |
| 左侧 | 全部 / 我的 / 收藏 / 最近 / 组织 |
| 卡片 | Agent 名、描述、display_group、绑定的 KB 数、运行按钮 |

---

## 4. 常见问题（FAQ）

### Q1：为什么我看不到「新建对话」按钮？
A：创建会话需要 Contributor+ 角色。请联系工作区 Owner 提升您的角色。

### Q2：为什么我能跑某个 Agent 但跑不了另一个？
A：Agent 有一个 `runnable_by_viewer` 标记。Viewer 只能跑标记为 true 的 Agent。如果该 Agent 是您部门常用的，请联系创建者开启标记或提升您的角色。

### Q3：我能下载 Agent 引用的原文吗？
A：可以。在对话的「参考资料」抽屉中，每条引用都有 KB 名称、文档名、链接。点击 KB 名称 → 文档详情 → 下载原始文件（如 KB 启用了分享）。

### Q4：为什么我看到的是「待审批」状态？
A：自注册的用户默认未审批。请联系工作区 Admin / Owner 批准（详见 [roles/03-admin.md § 2](./roles/03-admin.md)）。

### Q5：能在移动端用吗？
A：可以。Web 控制台是响应式设计，但建议在桌面浏览器使用。移动端原生体验通过 IM 集成（飞书 / 企微 / Slack）更优。

---

## 5. 何时联系 Owner

如果出现以下情况，请联系您的 Owner：

| 情况 | 谁该处理 |
|---|---|
| 需要创建 KB / Agent | Owner 提升您为 Contributor，或 Owner 自己创建 |
| 需要修改 KB 内容 | Contributor 创建者，或 Admin |
| 需要接入新的 LLM / 工具 | Admin |
| 邀请新成员 | Owner |
| 工作区审计问题 | Admin / Owner |
| 系统级问题（登录失败、KB 不可访问） | Admin / Owner |

---

## 6. 进一步阅读

- [modules/04-session-chat.md](../modules/04-session-chat.md) — 会话与聊天完整功能
- [modules/01-knowledge-base.md § 4](../modules/01-knowledge-base.md) — 知识库浏览与检索
- [modules/02-agent.md § 3](../modules/02-agent.md) — 智能体使用
- [README.md § 角色矩阵](../README.md#角色矩阵速查) — 确认您的权限边界