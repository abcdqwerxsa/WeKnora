# WeKnora Design Tokens 对照表（White-label 重构）

> 分支：`feature/panel-redesign-and-theming`
> 源文件：`frontend/src/assets/theme/theme.css`
> 原则：**保持 TDesign `--td-*` 变量名不变，只替换数值**——所有基于 TDesign 的组件与业务样式零破坏继承新主题。

## 1. 品牌色（Brand）

### Light 模式

原主题为高饱和品牌绿（主色 `#07c05f` 位于色阶第 4 级）；新主题采用克制专业的石墨靛蓝（主色 `#4c63c2` 位于第 6 级），色彩层级关系随主色位置重排。

| Token | 旧值（绿） | 新值（靛蓝） |
| --- | --- | --- |
| `--td-brand-color-1` | `#e9f8ec` | `#eef1fa` |
| `--td-brand-color-2` | `#09f479` | `#dce3f6` |
| `--td-brand-color-3` | `#08dd6e` | `#bac7ee` |
| `--td-brand-color-4` | `#07c05f` | `#93a5e2` |
| `--td-brand-color-5` | `#06b04d` | `#6c84d3` |
| `--td-brand-color-6` | `#049b38` | `#4c63c2` |
| `--td-brand-color-7` | `#038626` | `#3d51a6` |
| `--td-brand-color-8` | `#027218` | `#304089` |
| `--td-brand-color-9` | `#015e0d` | `#23306c` |
| `--td-brand-color-10` | `#004b05` | `#17214f` |
| `--td-brand-color` | brand-4（`#07c05f`） | brand-6（`#4c63c2`） |
| `--td-brand-color-hover` | brand-3 | brand-7 |
| `--td-brand-color-active` | brand-5 | brand-8 |
| `--td-brand-color-disabled` | `#8ce0af` | `#b9c2e2` |
| `--td-brand-color-light` | brand-1 | brand-1 |
| `--td-brand-rgb`（新增） | — | `76, 99, 194` |

### Dark 模式

原深色主题色阶反向（主绿在第 7 级）；新深色主题同样反向排布（主靛蓝在第 6 级），深色底上自动获得更高明度。

| Token | 旧值（绿·反向） | 新值（靛蓝·反向） |
| --- | --- | --- |
| `--td-brand-color-1` | `#06b04d20` | `#202746` |
| `--td-brand-color-2` | `#015e0d` | `#262f55` |
| `--td-brand-color-3` | `#027218` | `#2e3a66` |
| `--td-brand-color-4` | `#038626` | `#37477a` |
| `--td-brand-color-5` | `#049b38` | `#41548f` |
| `--td-brand-color-6` | `#06b04d` | `#5a72c7` |
| `--td-brand-color-7` | `#07c05f` | `#7a8dd6` |
| `--td-brand-color-8` | `#08dd6e` | `#98a7e2` |
| `--td-brand-color-9` | `#09f479` | `#b6c1ec` |
| `--td-brand-color-10` | `#a6fccf` | `#d5dcf6` |
| `--td-brand-color` / hover / active | brand-6 / brand-5 / brand-7 | brand-6（`#5a72c7`）/ brand-5 / brand-7 |
| `--td-brand-rgb`（新增） | — | `90, 114, 199` |

> `--td-brand-rgb` 为本次新增的 RGB 三元组变量，用于替换存量 `rgba(7,192,95,α)` 拼接：`rgba(var(--td-brand-rgb), 0.08)`。

## 2. 中性色（Neutral / Gray）

原为纯中性灰（无色相）；新为带靛蓝色相的石板灰阶（slate），与品牌色同源，界面整体更协调。

| Token | 旧值 | 新值（Light） | 新值（Dark） |
| --- | --- | --- | --- |
| `--td-gray-color-1` | `#f3f3f3` | `#f6f7f9` | `#101114` |
| `--td-gray-color-2` | `#eeeeee` | `#eef0f3` | `#16181c` |
| `--td-gray-color-3` | `#e7e7e7` | `#e3e6ea` | `#1b1f24` |
| `--td-gray-color-4` | `#dcdcdc` | `#d5d9df` | `#23272e` |
| `--td-gray-color-5` | `#c5c5c5` | `#b8bdc6` | `#2e333b` |
| `--td-gray-color-6` | `#a6a6a6` | `#979ea9` | `#3d434c` |
| `--td-gray-color-7` | `#8b8b8b` | `#7b828d` | `#565d68` |
| `--td-gray-color-8` | `#777777` | `#626975` | `#757c88` |
| `--td-gray-color-9` | `#5e5e5e` | `#4c525d` | `#98a0ab` |
| `--td-gray-color-10` | `#4b4b4b` | `#3a3f48` | `#b3bac4` |
| `--td-gray-color-11` | `#383838` | `#2b2f37` | `#c6ccd4` |
| `--td-gray-color-12` | `#2c2c2c` | `#22252b` | `#d3d8de` |
| `--td-gray-color-13` | `#242424` | `#1a1d22` | `#e0e4e9` |
| `--td-gray-color-14` | `#181818` | `#15171b` | `#e8eaed` |

## 3. 表面色（Surfaces）

| Token | 旧（Light） | 新（Light） | 旧（Dark） | 新（Dark） |
| --- | --- | --- | --- | --- |
| `--td-bg-color-page` | gray-2（`#eee`） | `#f4f5f7` | gray-14（`#181818`） | gray-1（`#101114`） |
| `--td-bg-color-sidebar` | `#f9f9f9` | `#fbfbfc` | `#181818` | gray-2（`#16181c`） |
| `--td-bg-color-container` | `#ffffff` | `#ffffff` | gray-13（`#242424`） | gray-3（`#1b1f24`） |

防闪动与桌面端背景已同步（`frontend/index.html`）：

| 场景 | 旧值 | 新值 |
| --- | --- | --- |
| index.html 首帧背景（dark / light） | `#181818` / `#eee` | `#101114` / `#f4f5f7` |
| Wails `WindowSetBackgroundColour`（dark / light） | `24,24,24` / `238,238,238` | `16,17,20` / `244,245,247` |

## 4. 语义色（Semantic）

主值取色阶更沉的一档（Light 模式），保证浅底上的对比度；Dark 模式整体提亮一档。

| 语义 | 旧主值（Light） | 新主值（Light） | 新主值（Dark） |
| --- | --- | --- | --- |
| Success `--td-success-color` | `#00a870`（5） | `#2e8f63`（5） | `#3fa97a`（6） |
| Warning `--td-warning-color` | `#ed7b2f`（5） | `#c17a17`（5） | `#d89a3d`（6） |
| Error `--td-error-color` | `#e34d59`（6） | `#c94f4f`（5） | `#dd6b6b`（6） |
| Info `--td-info-color` | TDesign 默认 `#0052d9` | `#3b7fc4`（5） | `#5d97d4`（6） |

各级色阶（1–10）与 hover/active/focus/disabled 派生均已按同一明度节奏重排，详见 `theme.css`。

## 5. 字体（Typography）

| Token | 旧值 | 新值 |
| --- | --- | --- |
| `--app-font-family` | `"TencentSans", 系统栈…`（品牌定制字体，随 `assets/fonts/TencentSans.ttf` 加载，约 8.5MB） | 系统字体栈：`-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif` |

配套变更：`git rm frontend/src/assets/fonts.css` 与 `frontend/src/assets/fonts/TencentSans.ttf`；`main.ts` 移除字体样式引入。

## 6. 圆角（Radius）

整体上调一档，趋近现代 SaaS 观感（卡片 8、弹层 12）。

| Token | 旧值 | 新值 |
| --- | --- | --- |
| `--td-radius-small` | `2px` | `4px` |
| `--td-radius-default` | `3px` | `6px` |
| `--td-radius-medium` | `6px` | `8px` |
| `--td-radius-large` | `9px` | `12px` |
| `--td-radius-extraLarge` | `12px` | `16px` |
| `--td-radius-round` / `circle` | `999px` / `50%` | 不变 |

## 7. 阴影（Shadow）

由「多层大扩散」改为「低高度、低透明度」的柔和两级式，减少廉价投影感。

| Token | 旧值（摘录） | 新值（Light） |
| --- | --- | --- |
| `--td-shadow-1` | `0 1px 10px rgba(0,0,0,.05), 0 4px 5px …, 0 2px 4px -1px …` | `0 1px 2px rgba(16,18,24,.04), 0 1px 3px rgba(16,18,24,.06)` |
| `--td-shadow-2` | `0 3px 14px 2px …, 0 8px 10px 1px …, 0 5px 5px -3px …` | `0 1px 2px rgba(16,18,24,.04), 0 2px 8px rgba(16,18,24,.06)` |
| `--td-shadow-3` | `0 6px 30px 5px …, 0 16px 24px 2px …, 0 8px 10px -5px …` | `0 4px 12px rgba(16,18,24,.08), 0 12px 32px rgba(16,18,24,.1)` |

Dark 模式阴影透明度相应加深（`.2/.28/.32–.4`），详见 `theme.css`。

## 8. 遗留硬编码色的替换约定

| 旧写法 | 新写法 | 适用场景 |
| --- | --- | --- |
| `#07c05f` / `#07C05F`（CSS 与 SVG 资产） | `var(--td-brand-color)` 或资产内直接替换为 `#4C63C2` | 品牌色直引 |
| `rgba(7,192,95,α)` | `rgba(var(--td-brand-rgb), α)` | 品牌色透明叠层 |
| `#07A050` / `#0052d9`（UI 铬层） | `var(--td-brand-color)` | 链接、徽章、选中态 |
| `rgba(0,82,217,α)`（TDesign 默认蓝） | `rgba(var(--td-brand-rgb), α)` | 选中/hover 叠层 |
| TencentSans / `fonts.css` | `var(--app-font-family)` | 全局字体 |

**有意保留**（非品牌色）：

- `--td-success-color` 等语义色作为「绿色回退」的场景；
- `views/knowledge/wiki/WikiBrowser.vue` 中图谱节点分类色板（`#0052d9` 摘要 / `#2ba471` 实体 / `#e37318` 概念）——数据可视化类别色，图例与节点需保持一致。
