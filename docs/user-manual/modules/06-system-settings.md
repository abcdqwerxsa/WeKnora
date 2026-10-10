# 模块参考 · 系统设置（Settings）

> **入口**：`/platform/settings`（统一模态）
> 通过 `?section=` 切换所有配置
> **对应前端**：`frontend/src/views/settings/Settings.vue`（统一壳）+ 各子组件
> **配置文件**：`config/settingsAccess.ts`（最低角色表）

---

## 1. 设置模态导航

```
设置模态（左侧导航）
├── 通用                  (viewer)
├── 模型                  (admin)
├── Ollama                (admin)
├── 网络搜索              (admin)
├── 聊天历史              (admin)
├── 向量库                (admin)
├── 解析器                (admin)
├── 存储                  (admin)
├── 沙箱                  (admin)
├── Skills                (admin)
├── MCP                   (admin)
├── 记忆                  (admin)
├── Env Var               (viewer)
├── 工作区信息            (viewer 可看，编辑 owner)
├── 成员                  (viewer 可看，操作 owner)
├── 我的记忆              (管理员)
├── 个人资料              (viewer)
├── 审计日志              (admin)
├── 集成                  (viewer)
│   ├── API Key Playground
│   └── IM 落地页
└── 待审批用户            (admin)
```

---

## 2. 通用设置（`section=general`）

| 项 | 说明 |
|---|---|
| UI 偏好 | 主题 / 语言 / 默认 KB |
| 默认 Agent | 全工作区默认 Agent（可被单个对话覆盖） |
| 通知偏好 | 邀请 / 审批通知 |

---

## 3. 模型（`section=models`）

> 详见 [roles/03-admin.md § 3.1](../roles/03-admin.md)

支持：`OpenAI / Azure OpenAI / Anthropic / DeepSeek / Qwen / Zhipu / Hunyuan / Doubao / Gemini / MiniMax / NVIDIA / Novita / SiliconFlow / OpenRouter / Requesty / LiteLLM / Ollama`

| 类型 | 用途 |
|---|---|
| Chat | LLM 对话 |
| Embedding | 向量嵌入 |
| Rerank | 重排 |
| VLM | 视觉语言模型 |
| ASR | 语音识别 |

详细清单：`config/builtin_models.yaml` + [`docs/BUILTIN_MODELS.md`](../../BUILTIN_MODELS.md)。

### 3.1 创建模型

| 字段 | 说明 |
|---|---|
| 提供商 | 见上 |
| 模型名 | API 实际名称 |
| API Key / Endpoint | 凭证 |
| 类型 | Chat / Embedding / Rerank / VLM / ASR |

### 3.2 测试

- 「**测试连接**」→ 验证可达
- 显示延迟 / 模型元数据

### 3.3 使用详情

- 按模型看调用次数、token 用量
- 用于成本分析

---

## 4. Ollama（`section=ollama`）

- 本地 Ollama 服务配置
- 填入 endpoint（如 `http://localhost:11434`）
- 自动发现本地模型

---

## 5. 向量库（`section=vectorstore`）

> 详见 [`docs/使用其他向量数据库.md`](../../使用其他向量数据库.md)

| 后端 | 适用 |
|---|---|
| **pgvector**（默认） | 与 Postgres 一起，混合检索（pgvector + BM25） |
| **Qdrant** | 独立向量库 |
| **Milvus** | 大规模场景 |
| **Weaviate** | 多模态场景 |

填入连接信息 → 测试 → 保存。

---

## 6. 存储后端（`section=storage`）

> 对象存储（MinIO / S3 / COS / OSS）

| 后端 | 适用 |
|---|---|
| **MinIO**（默认） | 自托管 |
| **AWS S3** | 云端 |
| **腾讯 COS** | 腾讯云 |
| **阿里 OSS** | 阿里云 |

---

## 7. 解析引擎（`section=parser`）

- DocReader 服务连接（gRPC 端口 50051）
- 高级解析：MinerU endpoint（`MINERU_ENDPOINT`）
- 默认解析器选择

---

## 8. 网络搜索（`section=websearch`）

> 见 [`docs/添加新的网络搜索引擎.md`](../../添加新的网络搜索引擎.md)

支持：Bing / Tavily / Google / DuckDuckGo 等

填 API Key → 选择默认引擎。

---

## 9. 聊天历史（`section=chathistory`）

- 保留策略（天数 / 永久）
- 软删除 vs 硬删除
- 跨租户隔离

---

## 10. 沙箱（`section=sandbox`）

详见 [modules/05-skills-mcp.md § 3](./05-skills-mcp.md)。

---

## 11. Skills（`section=skills`）

详见 [modules/05-skills-mcp.md § 1](./05-skills-mcp.md)。

---

## 12. MCP（`section=mcp`）

详见 [modules/05-skills-mcp.md § 2](./05-skills-mcp.md)。

---

## 13. 长期记忆（`section=memory` + `section=mymemory`）

| 配置 | 说明 |
|---|---|
| 抽取策略 | 自动抽取哪些类（fact / preference / interest …） |
| 用户确认 | 抽取后是否需要用户确认 |
| 保留期 | 记忆有效期 |

`mymemory` 是个人级记忆查看 / 编辑。

---

## 14. Env Var（`section=envvars`）

- 工作区级环境变量
- 在 Skill 脚本中可访问
- 个人级 vs 工作区级

---

## 15. 工作区信息（`section=tenant`）

详见 [roles/04-owner.md § 5](../roles/04-owner.md)。

---

## 16. 成员（`section=members`）

详见 [roles/04-owner.md § 2-4](../roles/04-owner.md)。

---

## 17. 个人资料（`section=userprofile`）

- 头像 / 显示名
- 密码修改（8-32 字符）
- UI 偏好

---

## 18. 审计日志（`section=audit-log`）

- Admin+ 可看
- 详见 [roles/03-admin.md § 4](../roles/03-admin.md)

---

## 19. 集成（`section=integrations`）

> 子项：
> - API Key Playground（API Key 测试）
> - IM 落地页（10 个平台）

### 19.1 API Key Playground

- 创建 API Key → 测试调用
- 显示请求 / 响应

### 19.2 IM 集成

- 飞书 / 企微 / Lark / QQBot / Slack / Telegram / 钉钉 / Mattermost / 微信 / 云之家
- 详见 [`docs/IM集成开发文档.md`](../../IM集成开发文档.md)

---

## 20. 待审批用户（`section=pending-users`）

详见 [roles/03-admin.md § 2](../roles/03-admin.md)。

---

## 21. 关键配置 API

| Method | 路径 | 用途 |
|---|---|---|
| GET | `/system/settings` | 列设置 |
| GET | `/system/settings/:key` | 读单条 |
| POST/PUT | `/system/settings/:key` | 写单条 |
| DELETE | `/system/settings/:key` | 复位 |

> 来源：`system_setting.go` —— 强类型注册表（int / string / bool / string_list），Redis pub/sub 跨副本同步（`LUOSA_REDIS_NAMESPACE` 隔离多部署）。

---

## 22. 配置组织的最低视角表

> 来源：`frontend/src/config/settingsAccess.ts`

| section | 最低角色 |
|---|---|
| `general` | viewer |
| `ollama` | admin |
| `models` | admin |
| `websearch` | admin |
| `chathistory` | admin |
| `vectorstore` | admin |
| `parser` | admin |
| `storage` | admin |
| `sandbox` | admin |
| `skills` | admin |
| `mcp` | admin |
| `memory` | admin |
| `tenant` | viewer（编辑 owner） |
| `members` | viewer（操作 owner） |
| `userprofile` | viewer |
| `envvars` | viewer |
| `pending-users` | admin |
| `audit-log` | admin |
| `integrations` | viewer |

---

## 23. 快捷入口最低视角（`SETTINGS_MANAGEMENT_SHORTCUT_MIN_ROLE`）

> 设置菜单的「管理快捷方式」（在用户菜单）要求更高：

| 快捷方式 | 最低角色 |
|---|---|
| `members` | owner |
| `models` | admin |
| `skills` | admin |

---

## 24. 进一步阅读

- [roles/03-admin.md](../roles/03-admin.md) — Admin 完整流程
- [roles/04-owner.md](../roles/04-owner.md) — Owner 完整流程
- [`docs/配置参考手册`](../../config-reference) — 完整配置项说明