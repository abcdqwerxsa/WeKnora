# 模块参考 · 知识库（Knowledge Base）

> **创建 / 编辑权限**：Contributor+ 创，自己名下可改；Admin+ 可改任意

---

## 1. 知识库的三种类型

| 类型 | 用途 | 创建后主要操作 |
|---|---|---|
| **Document** | 上传 PDF / Word / Excel / 图片 / EPUB / XMind 等 | 上传文档 → 自动解析 → 切片 → 嵌入 |
| **FAQ** | 一问一答的标准问答对 | 手动添加问答；支持 Excel 批量导入 |
| **Wiki** | 自动摄取 + 手动维护的 Markdown 知识 | Agent 自动摄取 + 手动编辑 + 修订历史 |

---

## 2. 创建知识库

路径：`/platform/knowledge-bases` → 「**+ 新建知识库**」

| 字段 | 说明 | 谁控制 |
|---|---|---|
| 名称 | KB 显示名 | Contributor |
| 类型 | Document / FAQ / Wiki | Contributor |
| 描述 | KB 范围 | Contributor |
| 嵌入模型 | 哪种 Embedding 模型 | 默认走工作区配置（Admin 设置），可逐 KB 覆盖 |
| 向量库 | pgvector / Qdrant / Milvus / Weaviate | 默认走工作区配置 |
| 存储后端 | MinIO / S3 / COS / OSS | 默认走工作区配置 |
| 共享策略 | 私有 / 工作区内 / 跨工作区（Organization） | Owner 启用后可选 |

---

## 3. KB 详情页布局

顶部：KB 名、收藏、设置、共享
左侧：文件夹树
主体：文档列表 / 卡片视图 / FAQ / Wiki Tab
底部：「+ 上传」「+ 新建文件夹」

---

## 4. 上传与解析

### 4.1 上传源

| 来源 | 说明 |
|---|---|
| **文件** | 拖拽或点击上传（PDF / Word / Excel / 图片 / EPUB / XMind / Markdown / HTML / MHTML / CSV / JSON） |
| **URL** | 粘贴 URL，自动抓取 |
| **文件夹** | 批量上传整个文件夹 |
| **数据源** | Feishu Wiki / Feishu Drive / Lark / GitLab / 腾讯 IMA / Notion / Yuque / RSS（详见 [§6](#6-数据源)） |

### 4.2 解析流程

```
上传 → 对象存储
     → 异步任务派发到 DocReader
     → 解析（PDF / DOCX / Excel / OCR / …）
     → 切片（按段落 / 固定窗口 / 语义）
     → 嵌入（Embedding 模型）
     → 写入向量库 + Postgres
     → 状态 done
```

### 4.3 支持的文档格式

PDF（含扫描 OCR）/ DOCX / Excel / PPTX / 图片（OCR）/ HTML / Markdown / EPUB / MHTML / XMind。Office 通用文档可选 MinerU / MarkItDown / OpenDataLoader 等高级解析器。

### 4.4 排错

| 现象 | 可能原因 | 操作 |
|---|---|---|
| 上传后一直「pending」 | 任务队列阻塞 / Redis 异常 | 检查 Redis；查看任务队列 |
| 一直「parsing」 | DocReader 挂 / OOM / 配置错误 | 检查 DocReader 日志 |
| 解析失败 | 文件超过 1 GB（`MAX_FILE_SIZE_MB`） | 拆分 / 调高上限 |
| 中文乱码 | 解析器没装 OCR / Tesseract 中文包 | 重新构建镜像 |
| 嵌入失败 | Embedding 模型不可达 | 测试模型连接 |

---

## 5. KB 设置子面板

KB 创建者可修改；Admin+ 可改工作区默认值。

| 面板 | 用途 |
|---|---|
| **解析器** | 选择解析器（默认 / MinerU / MarkItDown / OpenDataLoader） |
| **切片** | 切片策略（按段落 / 固定窗口 / 语义）+ 调试 |
| **嵌入模型** | 覆盖默认 Embedding |
| **向量库** | 覆盖默认向量库后端 |
| **存储** | 覆盖默认存储后端 |
| **高级** | 混合检索策略、MMR、阈值 |
| **索引进阶** | 标签过滤、graph 增强 |
| **共享** | 共享策略（工作区内 / Organization） |
| **知识图谱** | 启用图谱抽取 |
| **数据源** | 配置 + 同步日志 |
| **活动审计** | KB 内活动日志 |

---

## 6. 数据源

| 数据源 | 用途 |
|---|---|
| **Feishu Wiki / Drive** | 飞书知识库 / 云盘 |
| **Lark Wiki / Drive** | Lark（国际版） |
| **GitLab** | 仓库 Wiki / Markdown |
| **腾讯 IMA / Notion / Yuque** | 知识管理平台 |
| **RSS** | 任意 RSS 源 |

**配置步骤**：

1. KB 详情 → 设置 → 数据源 → 「**+ 添加数据源**」
2. 选 connector → 填凭证 → 测试
3. 启用同步（周期 / 手动）
4. 在「同步日志」看进度与失败

---

## 7. FAQ 维护

- **单个添加**：问答对（问、答、tags）
- **批量导入**：上传 Excel / CSV（列：question / answer / tags）
- **批量导出**：Excel（`*_faq_export.xlsx`）
- **批量打标签**：选多条 → 批量打

---

## 8. 知识图谱

启用图谱后，KB 自动抽取实体与关系；KB 详情中可看交互式知识图谱。与 Wiki 模式配合，自动构建结构化知识。

---

## 9. 标签与文件夹

- **标签**：KB 内 / 跨 KB；自动打标只从现有标签集挑匹配，不创建新标签
- **文件夹**：多层文件夹树，拖拽移动文档，KB 创建者管理

---

## 10. 共享

| 范围 | 方式 |
|---|---|
| **工作区内** | 默认所有成员可读；可选「仅自己」 |
| **跨工作区** | Owner 启用；通过 Organization；按 `OrgMemberRole` 裁剪 |

详见 [roles/04-owner.md § 7](../roles/04-owner.md)

---

## 11. 复制 / 收藏 / 删除

| 操作 | 谁可以做 | 行为 |
|---|---|---|
| 复制 | Viewer（任何人） | 生成副本，您是创建者，默认私有 |
| 收藏 | Viewer（任何人） | 加入左侧「我的」 |
| 删除 | KB 创建者 / Admin+ | 软删除；异步清理资源 |

---

## 12. 关键不变式

1. **KB 删除是异步的**——列表立即消失，资源清理在后台任务。
2. **复制 KB 自动改名**「原名 的副本」。
3. **共享 KB 的写入工具被过滤**——共享给其他工作区时，写操作工具自动隐藏（只读语义）。
4. **自动标签不创建新标签**——只从现有标签集挑匹配。

---

## 13. 进一步阅读

- [`docs/CHUNKING.md`](../../CHUNKING.md) — 切片策略
- [`docs/使用其他向量数据库.md`](../../使用其他向量数据库.md) — 接入其他向量库
- [`docs/KnowledgeGraph.md`](../../KnowledgeGraph.md) — 知识图谱
- [`docs/数据源导入开发文档.md`](../../数据源导入开发文档.md) — 数据源开发
- [roles/02-contributor.md](../roles/02-contributor.md) — Contributor 视角
- [modules/02-agent.md](./02-agent.md) — Agent 如何使用 KB