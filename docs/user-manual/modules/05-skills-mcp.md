# 模块参考 · 技能与 MCP（Skills / MCP）

> **管理权限**：Admin+（`g.Admin()`）用于安装工作区级；Contributor 在 Agent 内启用

---

## 1. Skills（技能）

### 1.1 概念

**Skill** = 一个可被 Agent 调用的能力单元（一个目录 + `SKILL.md` + 可选脚本）。

### 1.2 SKILL.md 格式

```markdown
---
name: my-skill              # kebab-case，≤ 64 字符
description: 一句话描述
metadata:
  type: builtin | tenant
---

# Skill 说明

详细描述 Agent 何时使用、如何使用。

## 工具列表
- `tool_name`：用途

## 脚本
```bash
python scripts/run.py <args>
```
```

### 1.3 命名规则

- **kebab-case**
- 长度 ≤ 64 字符
- 仅含 `[A-Za-z0-9_-]`

### 1.4 安装源

| 源 | 说明 |
|---|---|
| **ClawHub** | 官方市场 |
| **SkillHub** | 社区市场 |
| **Git** | 直接 Git URL |
| **Zip** | 上传 zip 包 |

### 1.5 安装范围

- **工作区级**（Admin 操作）：所有成员可用
- **个人级**（自己）：仅自己可用

### 1.6 安装流程

1. `/platform/settings?section=skills`
2. 「**+ 安装技能**」→ 选择源
3. 配置：来源 URL / 上传文件、安装路径（工作区 / 个人）、环境变量（可选）
4. 安装过程显示进度 → 完成
5. 在 Agent 编辑器 → Skill 标签中启用

### 1.7 在 Agent 中使用

- **Agent 编辑器** → 「**技能**」标签
- 配置 `SkillsEnabled`：启用哪些 Skill
- 配置 `SkillsSelectionMode`：
  - `all`：全部启用
  - `whitelist`：白名单
  - `mention`：在对话中 `@Skill_name` 启用

### 1.8 脚本执行（沙箱）

- Skill 脚本在 **sandbox 容器**中执行
- Docker 镜像预装：`python-docx` / `openpyxl` / `pandas` / …
- 沙箱后端：Docker（默认）/ E2B / Cube
- 网络策略：默认限制出网
- TTL：会话级生命周期

### 1.9 维护操作

| 操作 | 说明 |
|---|---|
| 更新 | 重新安装（覆盖） |
| 卸载 | 解除 Skill 安装 |
| 回收运行 | 清理超时 / 异常进程 |
| 停止运行 | 终止正在运行的 Skill |

### 1.10 内置 Skills（`skills/preloaded/`）

| Skill | 用途 |
|---|---|
| `citation-generator` | 生成引用 |
| `data-processor` | 数据处理 |
| `doc-coauthoring` | 文档协作 |
| `document-analyzer` | 文档分析 |
| `office-template-fill` | Office 模板填充 |
| `openmaic-classroom` | 课堂场景 |

---

## 2. MCP（Model Context Protocol）

### 2.1 概念

**MCP** 是 Anthropic 提出的开放协议，让 Agent 调用外部工具。MCP 服务器暴露一组「工具」，Agent 通过统一协议调用。

### 2.2 架构

```
LuoSA App（Agent）
    ↓ MCP 协议（stdio / SSE / HTTP）
MCP Server（独立服务 / 第三方）
    ↓
外部系统（数据库 / SaaS API / …）
```

### 2.3 配置 MCP 服务（Admin）

1. `/platform/settings?section=mcp`
2. 「**+ 新建 MCP 服务**」
3. 配置：

| 字段 | 说明 |
|---|---|
| **名称** | 显示名 |
| **传输协议** | stdio / SSE / HTTP |
| **端点** | URL 或命令 |
| **认证** | None / API Key / OAuth2 |
| **凭证** | API Key / OAuth secret |

4. 点击「**测试连接**」→ 拉取工具列表
5. 保存 → 在 Agent 编辑器中可用

### 2.4 OAuth2 中途授权

- Agent 在对话中调用需要授权的工具
- 自动触发 OAuth flow（mid-conversation）
- 用户看到授权弹窗 → 同意 → 继续调用

### 2.5 工具审批

- **模式**：手动允许 AI 干预
- Agent 调用 MCP 工具前弹出审批框
- 用户批准 / 拒绝

### 2.6 在 Agent 中使用 MCP

- Agent 编辑器 → 「**工具**」标签
- 启用 MCP 服务（pinned）
- Agent 自动获得 MCP 工具列表

---

## 3. 沙箱（Sandbox）

Skill 脚本 + Shell 执行的隔离环境。

### 3.1 后端

| 后端 | 适用 |
|---|---|
| **Docker**（默认 / 集群） | 本地 / 自建集群 |
| **E2B** | 云端托管 |
| **Cube** | 第三方托管 |

### 3.2 配置（Admin）

1. `/platform/settings?section=sandbox`
2. 选后端 → 填凭证 / socket / endpoint
3. 配置：`network_policy`（默认限制出网）/ `ttl`（会话沙箱 TTL）/ `cpu_limit` / `memory_limit`
4. 保存

### 3.3 集群模式

多节点 Docker 集群，支持负载均衡与故障转移。详见 [`docs/sandbox-cluster.md`](../../sandbox-cluster.md)。

---

## 4. 关键不变式

1. **Skill 名称 kebab-case 且唯一**。
2. **工作区 Skill 由 Admin 安装，个人 Skill 自己安装**。
3. **沙箱默认限制出网**。
4. **MCP OAuth 中途授权需要用户确认**。
5. **共享 Agent 的写工具被过滤**——包括 Skill 文件写、Skill 脚本执行（视配置）。

---

## 5. 进一步阅读

- [`docs/BUILTIN_MCP_SERVICES.md`](../../BUILTIN_MCP_SERVICES.md) — 内置 MCP 服务
- [`docs/MCP功能使用说明.md`](../../MCP功能使用说明.md) — MCP 功能说明
- [`docs/sandbox-protocol.md`](../../sandbox-protocol.md) — 沙箱协议
- [`docs/sandbox-cluster.md`](../../sandbox-cluster.md) — 沙箱集群
- [modules/02-agent.md § 6](./02-agent.md) — Agent 中 Skill / MCP 启用
- [modules/03-workflow.md § 6](./03-workflow.md) — 工作流中的 MCP Tool 节点
- [roles/03-admin.md § 3](../roles/03-admin.md) — Admin 视角