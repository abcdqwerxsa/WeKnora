# 模块参考 · Wiki 模式

> **创建 KB 权限**：Contributor+（KB 类型选 Wiki）
> **编辑权限**：KB 创建者

---

## 1. 概念

**Wiki 模式** = 把分散的文档**自动蒸馏**成结构化、可互链、可回滚的 Markdown 知识库。

```
原始文档（PDF / Word / Markdown …）
    ↓
wiki_ingest（并发摄取）
    ├─ 行级 claim（不重不漏）
    ├─ per-slug 锁
    ├─ 去重
    ├─ 分类法
    └─ 引用生成
    ↓
Wiki Page（Markdown + 互链 + 修订）
    ↓
Wiki 浏览器（前端）
    ├─ 目录树
    ├─ 修订历史 + 行级 diff
    └─ 一键回滚
```

---

## 2. 创建 Wiki 类型 KB

1. 进入 `/platform/knowledge-bases` → 「**+ 新建知识库**」
2. 类型选 **Wiki**
3. 选择默认 Markdown 模板（可选）
4. 创建后自动跳转到 Wiki 浏览器

---

## 3. 自动摄取（Agent Wiki Mode）

### 3.1 触发

- 手动：在 Wiki 浏览器 → 「**摄取**」按钮
- 自动：绑定 Agent → Agent 在回答时把知识点 push 到 Wiki

### 3.2 摄取流程

```
1. 行级 claim（防并发重复）
2. per-slug 锁
3. 解析内容（拆分段落）
4. 去重（slug identity "wiki:identity:"）
5. 分类法
6. 引用生成
7. 自动链接（识别 KB 内的相关 page 自动加链）
8. 内容质量检查
9. 写入（最大 32 KB 内容上限）
```

### 3.3 摄取源

- KB 内文档（Document 类型 KB）
- 外部数据源（Feishu Wiki / GitLab / Notion / Yuque）
- 手动上传

### 3.4 批量摄取

- 单批可摄取 40K 文档 KB
- 异步任务调度
- 失败重试 + 续传

---

## 4. 手动编辑

### 4.1 创建页面

1. Wiki 浏览器 → 「**+ 新页面**」
2. 填写：
   - **slug**（URL 友好，如 `product-overview`）
   - **标题**
   - **正文**（Markdown）
   - **类型**（page / index / redirect）
3. 保存

### 4.2 编辑页面

1. 点击页面 → 「**编辑**」
2. 修改 Markdown
3. 保存 → 自动生成新修订

### 4.3 文件夹操作

- 新建文件夹
- 移动页面
- 删除文件夹（连带页面）

---

## 5. 修订历史

### 5.1 查看修订

1. 在 Wiki 页面 → 「**修订历史**」
2. 显示所有历史版本（时间、作者、commit message）
3. 点击版本 → 行级 diff
4. 「**回滚到此版本**」→ 一键恢复

### 5.2 修订 API

| Method | 路径 | 用途 |
|---|---|---|
| GET | `/wiki/pages/:slug/revisions` | 列表 |
| GET | `/wiki/pages/:slug/revisions/:rev_id` | 单个版本 |
| POST | `/wiki/pages/:slug/revisions/:rev_id/revert` | 回滚 |

---

## 6. 知识图谱（Wiki + Graph）

Wiki 模式 + Knowledge Graph 启用后自动构建：

- **节点**：每个 Wiki 页面
- **边**：页面间的链接（自动 linkify + 手动）

### 6.1 交互式图谱

- Wiki 浏览器 → 「**图谱**」Tab
- 力导向图，可拖拽
- 节点大小 = 引用次数
- 边粗细 = 关联强度

---

## 7. Wiki 与 Agent 协作

### 7.1 Wiki Mode Agent

- Agent 类型选 Wiki 模式 → 启用 `wiki_*` 工具
- Agent 在回答中可引用、写入 Wiki 页面（需要 sandbox）

### 7.2 wiki_* 工具

| 工具 | 作用 |
|---|---|
| `wiki_read_page` | 读 Wiki 页面 |
| `wiki_search` | 搜索 Wiki |
| `wiki_list_pages` | 列出页面 |
| `wiki_write_page` | 写页面（需权限） |
| `wiki_get_graph` | 取知识图谱 |

---

## 8. 共享 Wiki

- KB 共享给 Organization 时
- `OrgMemberRole.editor` 可编辑 Wiki 页面
- `OrgMemberRole.viewer` 只读

---

## 9. 内容质量检查

自动检查：

- 链接是否失效
- 标题层级是否规范
- 内容是否过长

显示在页面顶部警告。

---

## 10. 关键不变式

1. **Wiki 页面 32 KB 内容上限**——超长内容会拆页。
2. **slug identity 用 `wiki:identity:` 前缀**——确保唯一。
3. **per-slug 锁防并发写**。
4. **行级 claim 不重不漏**。
5. **修订可恢复**——保留完整历史。

---

## 11. 典型场景

### 场景 A：把客服 KB 升级为 Wiki

1. 现有 KB 是 Document 类型
2. 创建新 KB 类型 Wiki
3. 启用「**摄取**」→ 选择原 KB → 触发摄取任务
4. 自动生成结构化页面 + 互链
5. 手动调整 + 修订

### 场景 B：构建研发知识库

1. 创建 Wiki KB「研发手册」
2. 上传 Markdown / DOCX / 链接 GitLab
3. 自动摄取 + 分类（前端 / 后端 / 运维 / …）
4. Agent 在回答中自动 push 知识点
5. 知识图谱可视化

### 场景 C：跨部门协作 Wiki

1. Owner 创建 Organization
2. Wiki KB 共享给组织
3. 编辑者跨工作区协作
4. 修订历史可追溯

---

## 12. 进一步阅读

- [`docs/KnowledgeGraph.md`](../../KnowledgeGraph.md) — 知识图谱完整文档
- [`docs/开启知识图谱功能.md`](../../开启知识图谱功能.md) — 启用图谱
- [modules/01-knowledge-base.md § 5](./01-knowledge-base.md) — KB 设置（GraphSettings / DataSourceSettings）
- [modules/02-agent.md § 5](./02-agent.md) — Agent 中 wiki_* 工具
- [roles/02-contributor.md](../roles/02-contributor.md) — 创建第一个 Wiki KB
- [roles/04-owner.md § 7](../roles/04-owner.md) — Wiki 跨工作区共享