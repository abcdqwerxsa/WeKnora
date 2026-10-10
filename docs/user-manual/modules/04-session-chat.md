# 模块参考 · 会话与聊天（Session / Chat）

> **对应后端**：`internal/application/service/session*.go`
> **对应前端**：`frontend/src/views/chat/*` + `creatChat/*` + `embed/*`
> **使用权限**：所有角色（Viewer / Contributor / Admin / Owner）
> **创建会话**：需要 Contributor+（`g.Contributor()`）

---

## 1. 概念

- **会话（Session）**：一组用户消息 + Agent 回复的对话线程
- **来源（source）**：Web / API / Embed / IM（10 个平台）
- **状态**：active / archived
- **主体（principal）**：用户 / IM 用户 / API Key / Embed 访客

---

## 2. 三类入口

| 入口 | 路径 | 说明 |
|---|---|---|
| **新建对话首页** | `/platform/creatChat` | 推荐问题卡片 + 输入框 |
| **对话主界面** | `/platform/chat/:chatid` | 完整对话视图 |
| **Embed widget** | `/embed.html` | 网站嵌入（独立 HTML） |

---

## 3. 新建对话首页（`/platform/creatChat`）

| 区域 | 内容 |
|---|---|
| **顶部** | Agent 选择器（默认 `builtin-quick-answer`） |
| **主体** | 推荐问题卡片（按 `source === 'faq'` 标记） |
| **底部** | 输入框 |
| **侧边** | KB 编辑器入口（Contributor+） |

**典型交互**：

```
1. 选 Agent → 2. 提问 → 3. 看回答 → 4. 追问 → 5. 收藏会话
```

---

## 4. 对话主界面（`/platform/chat/:chatid`）

### 4.1 区域

```
┌──────────────────────────────────────────────────┐
│  Agent 选择器  | 会话标题 | 参考资料 | ⋮ 菜单       │
├──────────────────────────────────────────────────┤
│                                                  │
│  👤 用户消息（Markdown）                          │
│                                                  │
│  🤖 助手消息（Markdown）                          │
│      [1] [2] [3] ← 引用 popover                  │
│      ─── 来源：销售手册 v2.docx                   │
│                                                  │
│  --- 追问 ---                                     │
│                                                  │
│  💡 Follow-up 建议：                              │
│      → 相关问题 1                                 │
│      → 相关问题 2                                 │
│                                                  │
├──────────────────────────────────────────────────┤
│  [📎附件] 输入框                                  │
└──────────────────────────────────────────────────┘
```

### 4.2 关键元素

| 元素 | 说明 |
|---|---|
| **引用 popover** | 回答下方的 `[1] [2]`，hover 看原文片段，点击跳到 chunk 详情 |
| **参考资料抽屉** | 完整引用列表（KB / 文档名 / chunk ID / 得分） |
| **Follow-up** | 自动推荐下一步问题 |
| **附件** | 上传临时文件（图片 / 文档），自动 Agent 读取 |
| **IM 来源标识** | 显示该会话来自哪个 IM 平台 |
| **时间戳** | 大纲 |

---

## 5. 会话后台管线

### 5.1 知识问答管线（`session_knowledge_qa.go`）

```
用户提问 → RAG 检索（HybridSearch）
         → 拼装 Prompt（带检索结果）
         → LLM 生成
         → 流式输出到前端
         → 持久化到 session_messages
```

### 5.2 智能体问答管线（`session_agent_qa.go`）

```
用户提问 → CreateAgentEngine
         → ReAct 循环
           ├─ LLM 思考 → tool_call
           ├─ 工具执行 → 结果
           ├─ 再次思考 → ...
           └─ 最终回答
         → 流式输出
```

### 5.3 临时附件（`session_attachment_staging.go`）

- 会话作用域 image/doc 暂存
- 异步解析（DocReader）
- 解析后注入 Agent / KB 上下文

### 5.4 沙箱绑定（`session_sandbox_pin.go`）

- 会话级沙箱生命周期管理
- 同一会话内复用同一沙箱
- 会话结束 / 超时销毁

---

## 6. 会话主体治理（关键不变式）

> 来源：`loadSessionForRead` + `runtimeMayBypassAdminConsoleRead`

| 主体 | 能读哪些会话 |
|---|---|
| **普通用户** | 只能读自己拥有的会话（`owner_id == user.id`） |
| **Admin 控制台** | 可读所有会话（管理用） |
| **IM 用户** | 只能读自己 IM 上下文产生的会话 |
| **API Key 用户** | 只能读自己创建的（按 KB allow-list） |
| **Embed 访客** | 只能读 Embed 通道会话 |

> **这是安全边界**——IM 通道会话不能跨主体读。

---

## 7. 会话操作

| 操作 | 谁能做 | API |
|---|---|---|
| 创建会话 | Contributor+ | `CreateSession` |
| 列出会话 | 自己拥有的 | `GetSessionsByTenant` / `ListSessions` |
| 收藏 / Pin | 自己拥有的 | `SetSessionPinned` |
| 改标题 | 自己拥有的 | `GenerateTitle` / 显式改 |
| 删除会话 | 自己拥有的 / Admin+ | `DeleteSession` |
| 批量删除 | 自己拥有的 | `BatchDeleteSessions` |
| 删除全部 | 自己拥有的 | `DeleteAllSessions` |

---

## 8. 跨主体通道

### 8.1 Embed 网站 widget（`/embed.html`）

> 前端：`frontend/src/views/embed/*`，后端：`embed_channel.go` / `embed_webhook.go` / `embed_session.go`

**特点**：

- **安全模式**（推荐）：token 交换 + 速率限制
- **Publish token**：前缀 `em_`，32 字节随机
- 访客通过 widget 与 Agent 对话，会话归属于 publish token 所属 KB

**配置**（Admin / Owner）：

1. 设置 → 集成 → Embed → 配置 publish token、subdomain、域名白名单
2. 在前端页面嵌入：
   ```html
   <script src="https://your-domain/embed.js" data-token="em_xxx"></script>
   ```

> 详见 [`docs/embed-secure-mode.md`](../../embed-secure-mode.md) 与 [`docs/embed-subdomain.md`](../../embed-subdomain.md)

### 8.2 IM 集成（10 个平台）

> 详见 [`docs/IM集成开发文档.md`](../../IM集成开发文档.md)

支持：WeCom（企微）/ Feishu（飞书）/ Lark（国际版）/ QQBot / Slack / Telegram / DingTalk（钉钉）/ Mattermost / WeChat（微信）/ Yunzhijia（云之家）

**配置**：Admin → 设置 → Integrations → 选择平台 → 填凭证 → 启用

### 8.3 REST API

> 程序化调用，API Key 鉴权。详见 [modules/06-system-settings.md § 3 租户 API Key](./06-system-settings.md)

```bash
curl -X POST https://your-domain/api/v1/chat \
  -H "X-API-Key: tk_xxx" \
  -H "Content-Type: application/json" \
  -d '{"agent_id": "...", "kb_ids": [...], "message": "..."}'
```

---

## 9. 推荐问题（来源）

推荐问题分两层：

| 层 | 来源 |
|---|---|
| **KB 推荐问题** | 由 KB 内 FAQ 条目生成（`source === 'faq'`） |
| **对话推荐** | 上轮回答末尾的 follow-up 建议（由 LLM 生成） |

---

## 10. 长期记忆（Memory）

- 跨会话记住用户偏好
- 类别：profile / preference / fact / task / interest
- 自动抽取 + 用户确认（`search_memory` 工具）
- 设置：`/platform/settings?section=memory`

---

## 11. 聊天历史设置

- 路径：`/platform/settings?section=chathistory`
- 配置保留策略（天数 / 永久）
- 软删除 vs 硬删除策略
- 工作区隔离

---

## 12. 关键不变式

1. **会话是按主体隔离的**——IM 用户不能读到 Admin 控制台创建的会话。
2. **Embed 通道会话安全模式推荐**——避免 token 泄露。
3. **Admin 可在管理界面读取所有会话**（`runtimeMayBypassAdminConsoleRead`），用于运维审计。
4. **会话标题可异步生成**（`GenerateTitleAsync` + `sanitizeGeneratedTitle`）。
5. **沙箱与会话绑定**——会话结束销毁沙箱。

---

## 13. 进一步阅读

- [`docs/IM集成开发文档.md`](../../IM集成开发文档.md) — IM 集成开发文档
- [`docs/embed-secure-mode.md`](../../embed-secure-mode.md) — Embed 安全模式
- [`docs/embed-subdomain.md`](../../embed-subdomain.md) — Embed subdomain
- [modules/01-knowledge-base.md § 6](./01-knowledge-base.md) — HybridSearch 原理
- [modules/02-agent.md § 9](./02-agent.md) — Agent 运行生命周期
- [modules/06-system-settings.md § 6-7](./06-system-settings.md) — API Key / OIDC / IM