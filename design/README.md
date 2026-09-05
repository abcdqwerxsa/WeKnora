# WeKnora 面板重设计 · 设计规范（Pencil）

> 设计源文件：`design/panel-redesign.pen`（Pencil MCP 可直接打开编辑）
> 已验证导出图：`design/exports/verified/`
> Token 落地对照：`design/TOKENS.md`

## 1. 设计定位与原则

- **White-label**：移除原厂特征（品牌绿、定制字体、默认蓝），替换为克制的石墨靛蓝企业级视觉。
- **只重设计，不重造**：所有屏幕 1:1 对照现有面板的真实信息架构（IA）、字段、文案与状态枚举——不新增、不虚构功能。
- **Token 驱动**：设计稿中的所有颜色均映射到 `frontend/src/assets/theme/theme.css` 中的 `--td-*` 变量，双主题（Light/Dark）同源。

## 2. 色彩体系（摘要）

| 角色 | Light | Dark | 用途 |
| --- | --- | --- | --- |
| Brand 主色 | `#4c63c2`（brand-6） | `#5a72c7` | 主按钮、激活态、链接、选中 |
| Brand 叠层 | `rgba(76,99,194,α)` | `rgba(90,114,199,α)` | 选中底色、hover 洗色 |
| 页面底 | `#f4f5f7` | `#101114` | 主区背景、防闪动色 |
| 侧栏底 | `#fbfbfc` | `#16181c` | 侧边栏 |
| 容器底 | `#ffffff` | `#1b1f24` | 卡片、表格、弹层 |
| Success / Warning / Error / Info | `#2e8f63` / `#c17a17` / `#c94f4f` / `#3b7fc4` | 各提亮一档 | 状态语义 |

完整色阶与新旧映射见 `TOKENS.md`。

## 3. 形状与层级

- 圆角：输入/小件 4–6，行项与卡片 8，弹层 12，胶囊 999。
- 阴影：三级柔和阴影（`--td-shadow-1/2/3`），卡片 resting 用 shadow-1；hover 用品牌色低透明投影（`0 4px 12px rgba(var(--td-brand-rgb), 0.12)`）。
- 分隔：1px `--td-component-stroke`；分区靠背景色阶差（page → sidebar → container）而非重线框。

## 4. 组件规范（.pen 内可复用组件）

| 组件 | 规格 | 状态 |
| --- | --- | --- |
| Nav Item | 208×34，radius 8，icon 18 + label 14px/500 | 默认：透明+次要文字；hover：容器 hover 底；**激活：brand-1 洗底 + 品牌色文字** |
| Session Row | 高 30–32，radius 8 | hover 淡灰；激活 brand-1 洗底 + 品牌色文字；悬浮显示 ⋯ 菜单 |
| Status Tag | 小号 TDesign tag（light-outline 变体） | primary（解析中/生成摘要中）/ success（已完成）/ danger（失败）/ warning（已取消/草稿） |
| 用户菜单 | 底部固定：头像 + 姓名 + 「空间 · 角色」 + ⋯ | — |

## 5. 屏幕清单（与真实路由一一对应）

| 导出图 | 对应路由/视图 | 覆盖的真实功能 |
| --- | --- | --- |
| `01-login-light.png` | `/login`（Login.vue） | 语言切换、产品特性轮播、邮箱+密码登录、创建账户；OIDC 按钮保留为条件渲染（仅部署方配置 SSO 时出现） |
| `02-kb-list-light.png` | `/platform/knowledge-bases`（KnowledgeBaseList.vue） | 空间切换、四项主导航、我的对话（今天/近 7 天分组）、置顶/我创建的分组、KB 卡片（Wiki 徽章/收藏/来源） |
| `03-kb-documents-light.png` | `/platform/knowledge-bases/:kbId`（KnowledgeBase.vue + DocumentListView.vue） | 面包屑（文档/Wiki/图谱 tab）、六项筛选、视图切换、添加文档、8 列文档表、解析状态全枚举（解析中/已完成/生成摘要中/失败/已取消/草稿） |
| `04-chat-light.png` | `/platform/chat/:id` | 资源 chips、@ 提及、RAG 检索时间线（问题理解→检索完成→生成中）、引用文档卡（N 个片段）、回答+操作工具条、追问 chips、输入坞（快速问答/联网/图片/附件/模型选择） |
| `05-kb-list-dark.png` | 同 02，Dark 模式 | 深色主题验证 |

## 6. 设计稿结构（.pen 文件内）

- `01 · Tokens`：色板 / 圆角 / 阴影看板（Design Token 快速取值区）
- 组件：`Nav Item`、`S · Session Row`、`S · Status Tag`
- `02 · 登录`（0,1100）· `03 · 知识库列表`（1560,1100）· `04 · 文档列表`（3120,1100）· `05 · 对话`（4680,1100）均为 Light
- `03D · 知识库列表 · Dark`（6240,1100）：Dark 变体验证屏

## 7. 代码落地映射

| 设计层 | 代码层 |
| --- | --- |
| Design Tokens | `frontend/src/assets/theme/theme.css`（保持 `--td-*` 变量名） |
| 防闪动/桌面背景 | `frontend/index.html` 内联脚本 + Wails `WindowSetBackgroundColour` |
| Nav Item / Session Row 规范 | `frontend/src/components/menu.vue` |
| 弹层/菜单规范 | `frontend/src/components/ChatHeader.vue` 及 `assets/dropdown-menu.less` |
| KB 卡片流 | `frontend/src/views/knowledge/KnowledgeBaseList.vue` |
| 文档表 | `frontend/src/views/knowledge/components/DocumentListView.vue`（TDesign tag theme 继承语义 Token） |
| 登录页渐变 | `frontend/src/views/auth/Login.vue`（靛蓝 225° 渐变 + 白色 alpha 覆盖层） |
