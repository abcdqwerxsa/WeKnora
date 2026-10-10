# 02 · 快速上手

> **目标**：5 分钟内从登录到完成第一次有意义的对话。所有角色都应能跟完。

---

## 1. 登录

### 1.1 普通登录

1. 打开浏览器访问 LuoSA 部署 URL（部署完成后由管理员提供，例如 `https://luosa.your-company.com`）。
2. 在登录页输入**用户名** + **密码**，点击「登录」。
3. 首次登录后默认进入 `/platform/creatChat`（新建对话首页）。

### 1.2 通过邀请链接登录

1. Owner 通过 [roles/04-owner.md](./roles/04-owner.md) 创建 share-link。
2. 您收到形如 `https://luosa.your-company.com/join?code=xxx` 的链接。
3. 点击链接 → 注册页（自动填入邀请码）→ 设置密码 → 加入工作区。
4. **注意**：通过公开 `/auth/register` 自注册的用户默认未审批，必须 Admin+ 批准后才能登录（详见 [roles/03-admin.md § 2 审批流程](./roles/03-admin.md)）。

### 1.3 OIDC / SSO 登录

如果管理员配置了 OIDC（详见 [modules/06-system-settings.md § 6 OIDC](./modules/06-system-settings.md)），登录页会显示「以 OIDC 登录」按钮。OIDC 用户**免审批**。

---

## 2. 主界面速览

登录后看到的是这样的结构：

```
┌─────────────────────────────────────────────────────────┐
│  [Logo]  工作区切换▼   ⌘K命令面板     🔔邀请角标  👤用户菜单 │
├──────┬──────────────────────────────────────────────────┤
│      │                                                  │
│ 导航 │              主内容区                            │
│      │                                                  │
│ KB   │    （当前页面：creatChat / chat / KB / agent /   │
│ Agent│     workflow / settings）                       │
│ 工作 │                                                  │
│ 流   │                                                  │
│ 组织 │                                                  │
│ 设置 │                                                  │
│      │                                                  │
└──────┴──────────────────────────────────────────────────┘
```

| 元素 | 用途 |
|---|---|
| **顶部 Logo** | 点击回到新建对话首页 |
| **工作区切换** | 切换到您有成员资格的其他工作区（每个工作区有独立的角色） |
| **⌘K 命令面板** | 全局搜索 KB / Action / 跳转 |
| **邀请角标** | 您收到的待接受邀请 |
| **用户菜单** | 个人资料、设置、切换工作区、登出 |
| **左侧导航** | 模块入口（KB / Agent / 工作流 / 组织 / 设置） |
| **主内容** | 当前模块的工作台 |

---

## 3. 与默认智能体对话（3 步）

> 这是所有用户（包括 Viewer）都能做的事。

### 第 1 步：进入新建对话首页

- 路径：`/platform/creatChat`（或点击顶部 Logo）
- 页面会显示**推荐问题卡片**（按内置 FAQ 标记）

### 第 2 步：选择 Agent（可选）

- 对话页顶部有一个 **Agent 选择器**（`AgentSelector.vue`）
- 第一次使用，默认是 **`builtin-quick-answer`**（直接对话，不依赖 KB）
- 如果您想测试某个 KB 的检索，可以换一个绑定了 KB 的 Agent

### 第 3 步：提问

- 在底部输入框输入问题，按 Enter 发送
- 流式输出回答（您能看到 LLM 一个个 token 出来）
- 如果 Agent 调用了 KB，会显示**引用 popover**（点击引用跳到原文片段）
- 点击右上角「参考资料」按钮可以查看完整引用列表

**试一试**：

```
提问："你好，你能做什么？"
```

默认 Agent 会自我介绍它的能力。如果工作区里有任何 KB，您可以问：

```
提问："[KB 里某篇文档标题] 的核心结论是什么？"
```

Agent 会自动检索 KB，给出带引用的回答。

---

## 4. 浏览一个知识库

> Viewer / Contributor / Admin / Owner 都能看 KB（只读 vs 编辑权限不同）。

### 第 1 步：进入知识库列表

- 路径：`/platform/knowledge-bases`
- 左侧有分组：**全部 / 我的 / 收藏 / 最近 / 各组织共享**

### 第 2 步：选择一个 KB

- 点击任意 KB 卡片，进入 KB 详情页（`/platform/knowledge-bases/:kbId`）
- KB 详情页布局：
  ```
  ┌─────────────────────────────────────────┐
  │  KB 名称    ★收藏   ⚙设置   共享        │
  ├─────────────┬───────────────────────────┤
  │  文件夹树    │  文档列表 / 卡片视图        │
  │             │                           │
  │  📁 产品     │  📄 产品白皮书 v2.docx     │
  │  📁 客服     │  📄 FAQ.md                │
  │  📁 法规     │  📊 销售数据.xlsx         │
  │             │                           │
  ├─────────────┴───────────────────────────┤
  │  标签: 文档 / FAQ / Wiki（KB 类型 Tab）   │
  └─────────────────────────────────────────┘
  ```

### 第 3 步：检索测试

- 顶部「试一试」按钮会跳转到带 KB 上下文的对话
- 您可以直接提问，Agent 会限定在这个 KB 里检索

---

## 5. 看一个智能体

> Viewer / Contributor 都能跑 Agent（受 `runnable_by_viewer` 控制）。

### 第 1 步：进入智能体列表

- 路径：`/platform/agents`
- 左侧分组：**全部 / 我的 / 收藏 / 最近 / 组织 / chat / workflow**（按 `display_group` 分 Tab）

### 第 2 步：选择一个 Agent

- **内置智能体**（所有工作区都有）：
  - `builtin-quick-answer`：默认对话
  - `builtin-doc-review-proofread-chat`：文档与招投标审查
  - `builtin-doc-generation-chat`：投标文件编写

### 第 3 步：点击 Agent 卡片 → 与它对话

- Agent 详情页会展示：描述、绑定的 KB、可用工具、对话按钮

---

## 6. 邀请收件箱

> 所有用户都能接收邀请。

- 顶部铃铛显示待接受邀请数量
- 点击进入邀请列表（`/platform/settings?section=myinvitations`）
- 操作：接受 / 拒绝 / 查看详情（来自哪个工作区、什么角色）

---

## 7. 个人资料

- 路径：`/platform/settings?section=userprofile`
- 可改：头像、显示名、密码、UI 偏好
- **修改密码要求**：8-32 字符、含字母与数字（`ValidatePassword`）
- 复杂密码开关（`complexPasswordEnabled`）可能要求更严格

---

## 8. 5 分钟清单（跟完即上手）

- [ ] 已登录，看到主界面
- [ ] 已与默认 Agent 对话一次
- [ ] 已浏览一个 KB，能看到文档列表
- [ ] 已看一个 Agent 的详情
- [ ] 已知道设置在哪里、邀请角标在哪里
- [ ] 已读 README 的角色矩阵 → 知道自己是 Viewer / Contributor / Admin / Owner 中的哪个

---

## 9. 下一步

按您的角色继续：

- **Viewer**：[roles/01-viewer.md](./roles/01-viewer.md) — 您能用平台做什么
- **Contributor**：[roles/02-contributor.md](./roles/02-contributor.md) — 创建第一个 KB 与 Agent
- **Admin**：[roles/03-admin.md](./roles/03-admin.md) — 接入 LLM、配置基础设施
- **Owner**：[roles/04-owner.md](./roles/04-owner.md) — 邀请团队成员

模块参考（按需查阅）：

- [modules/01-knowledge-base.md](./modules/01-knowledge-base.md)
- [modules/02-agent.md](./modules/02-agent.md)
- [modules/03-workflow.md](./modules/03-workflow.md)
- [modules/04-session-chat.md](./modules/04-session-chat.md)
- [modules/05-skills-mcp.md](./modules/05-skills-mcp.md)
- [modules/06-system-settings.md](./modules/06-system-settings.md)
- [modules/07-wiki.md](./modules/07-wiki.md)