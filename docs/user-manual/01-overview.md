# 01 · 平台概览

> 本文是**所有角色必读**。先理解 LuoSA 能做什么，再去读对应角色的章节。

---

## 1. LuoSA 是什么

LuoSA 是一个**企业级 RAG + Agent 框架**。它的核心是把以下四件事串成一个可自托管的开源平台：

```
文档解析 → 知识库 → Agent 推理 → 工作流编排
```

| 能力 | 通俗解释 |
|---|---|
| **RAG 检索** | 把您公司的文档丢进去，用户提问时从文档里找最相关的段落给 LLM 看 |
| **ReAct Agent** | LLM 不光答，还能看 LLM 自己调用工具（查 KB / 跑脚本 / 调 MCP / 检索数据库） |
| **工作流编排** | 把多个 LLM 调用、检索、代码、Agent 节点拼成可视化 DAG，按发布版本运行 |
| **Skills / MCP** | 给 Agent 装上预置技能和外部 MCP 服务，让它能写脚本、操作第三方系统 |

LuoSA 不是一个「聊天机器人」。它是一套让企业把**私有知识 + 业务流程**接进 LLM 的完整方案。

---

## 2. 核心能力地图

### 2.1 三类核心使用方式

| 入口 | 说明 | 适合谁 |
|---|---|---|
| **Web 控制台** | 浏览器访问主界面（Vue3 SPA），所有功能都有 UI | 所有角色 |
| **REST / gRPC API** | 程序化访问，LuoSA 提供 API Key 主体鉴权 | 集成开发者 |
| **IM 渠道** | 飞书 / 企微 / Slack / Telegram 等 10 个平台 | 业务人员对话 |

### 2.2 七大功能模块

| 模块 | 一句话 | 主要路径 |
|---|---|---|
| **知识库（KB）** | 上传文档、解析、切片、检索 | `/platform/knowledge-bases` |
| **智能体（Agent）** | 单轮 ReAct 推理 + 工具调用 | `/platform/agents` |
| **工作流（Workflow）** | 多节点编排（Dify-parity） | `/platform/workflow` |
| **会话（Session）** | 聊天主界面 + 历史 + IM 接入 | `/platform/chat/:chatid` |
| **技能（Skills）** | 预置脚本 + 沙箱执行 | 设置 → Skills |
| **MCP 服务器** | 接入外部 MCP 协议服务 | 设置 → MCP |
| **Wiki 模式** | 把 KB 蒸馏成结构化、可互链的 Markdown 知识库 | KB 详情 → Wiki 标签 |
| **长期记忆（Memory）** | 跨会话记忆用户偏好 / 事实 | 设置 → 记忆 |

### 2.3 三种知识库类型

| 类型 | 用途 | 特点 |
|---|---|---|
| **Document（文档库）** | 上传 PDF / Word / Excel / 图片 / HTML / EPUB / XMind 等 | 自动解析 + 切片 + 检索 |
| **FAQ（问答库）** | 一问一答的标准问答对 | 精确匹配优先 |
| **Wiki（Wiki 库）** | 自动摄取 + 手动维护的 Markdown 知识 | 修订历史 + 知识图谱 |

---

## 3. 典型场景

| 场景 | 涉及模块 |
|---|---|
| **客服智能问答**：用户提问 → Agent 自动检索 FAQ / 文档库 → 给出引用了原文的回答 | KB + Agent + Session |
| **招投标文档审查**：上传招标文件 → Agent 调四维度审查 + 招投标对比 → 输出修订后 docx | KB + Agent + Skills + Sandbox |
| **投标文件编写**：分析招标文件评分项 → 生成应答 → 转 docx | KB + Agent + Skills |
| **跨部门 Wiki 沉淀**：把分散的文档自动蒸馏成结构化、可搜索的知识库 | KB (Wiki 模式) + Agent |
| **多步骤业务流程自动化**：客服工单 → 调 LLM 分类 → 查 KB → 调 HTTP → 写回数据库 | Workflow |
| **企业 IM 接入**：把 KB 与 Agent 接入飞书 / 企微 / Slack，对员工直接对话 | KB + Agent + IM |
| **网站嵌入**：在您的产品网站嵌入聊天 widget，提供基于私有 KB 的客服 | KB + Agent + Embed Channel |

---

## 4. 部署形态

LuoSA 通过 `docker-compose.yml` 一键拉起完整基础设施：

```
┌──────────────────────────────────────────────────────┐
│  浏览器 / IM / API                                 │
└──────────────────┬───────────────────────────────────┘
                   ▼
┌──────────────────────────────────────────────────────┐
│  LuoSA App（Go + Vue3）── 端口 8080                │
└──┬──────┬──────┬──────┬──────┬──────┬───────────────┘
   ▼      ▼      ▼      ▼      ▼      ▼
 Postgres  Redis  MinIO  DocReader  Sandbox  MCP-Server
 (元数据)  (队列) (对象)  (解析 gRPC) (执行)  (Node)
   ▲                                                
   └── pgvector + BM25（混合检索）                    
```

部署脚本详见 `AGENTS.md` 与 `docs/poc/deployment/`。

---

## 5. 登录入口与首屏

- **URL**：`https://your-domain/`
- **首次启动**（Lite 模式）：会自动初始化首个 Owner 账户，请按页面提示操作。
- **登录后默认页**：`/platform/creatChat`（新建对话首页），显示推荐问题与可用 KB。

**主界面五大区域**：

| 区域 | 说明 |
|---|---|
| 顶部导航 | 工作区切换、用户菜单、设置入口、邀请角标 |
| 左侧栏 | KB / Agent / Workflow / 组织 / 集成的导航入口 |
| 主内容 | 当前选中模块的工作台 |
| 右下 | 全局命令面板（`Ctrl+K`） |
| 设置模态 | 统一入口，通过 `?section=` 参数切换所有配置 |

---

## 6. 关键概念（先认识这些）

| 概念 | 解释 |
|---|---|
| **工作区（Tenant）** | LuoSA 的资源隔离单位。一个用户在多个工作区可有不同角色 |
| **角色（Role）** | Viewer / Contributor / Admin / Owner 四级，决定可执行的操作 |
| **组织（Organization）** | 跨工作区的 KB 共享载体，使用独立的 OrgMemberRole |
| **知识库（KB）** | 一组文档 + FAQ + Wiki 页面的集合，是检索的最小单元 |
| **智能体（Agent）** | 一个可对话的 AI 实体，绑定 KB + 工具 + 系统提示 |
| **工作流（Workflow）** | 一个可视化的节点 DAG，按发布版本执行 |
| **会话（Session）** | 一组用户消息 + Agent 回复的对话线程 |
| **API Key** | 程序化访问的鉴权主体，携带 capability 列表 |

---

## 7. 与角色相关的能力边界

> **本节是最重要的「阅读指南」**——请直接跳到对应角色的章节：

- **您是业务人员？** → [roles/01-viewer.md](./roles/01-viewer.md)
- **您要建立自己部门的 KB？** → [roles/02-contributor.md](./roles/02-contributor.md)
- **您负责公司平台 / IT？** → [roles/03-admin.md](./roles/03-admin.md)
- **您是部门主管 / 团队负责人？** → [roles/04-owner.md](./roles/04-owner.md)

---

## 8. 阅读建议

1. 第一次使用：先读 [02 快速上手](./02-quickstart.md) 把主界面跑通。
2. 理解权限：再看 [README § 角色矩阵](./README.md#角色矩阵速查) 确认您所在的角色。
3. 按角色阅读：[roles/](./roles/) 下选择您对应的角色文件。
4. 深入功能：需要时查阅 [modules/](./modules/) 下的具体模块文档。