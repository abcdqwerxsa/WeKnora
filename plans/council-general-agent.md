# Council Memo — WeKnora 演进为 WorkBuddy 式通用智能体平台可行性

> 决策备忘录。由 Parent 会话作为唯一合成者产出；advisor 只读、互不可见、无 peer 对话。

## 一、问题与范围

- **问题**：领导要求把 WeKnora 做成 WorkBuddy 式通用型智能体——自然语言任务 → agent 自主规划执行 → 支持代码开发（写/跑代码）与产物生成（文件/报告/应用工程）。能否做？怎么走？风险何在？
- **范围**：架构可行性 + 复用优先演进路径；证据 = 当前代码库。
- **非目标**：RAG 质量评审、采购 vs 自研、完整排期、任何代码修改。

## 二、结论（Recommendation）

**能做，且远比"从零造平台"乐观——WorkBuddy 式能力约七到八成已在库中。** 但关键判断与直觉相反：

> **WorkBuddy 式产品是 agent-first，不是 canvas-first。** 主轴不是工作流画布，而是已有的端到端链路：ReAct 引擎（executeLoop，迭代预算可配至 unlimited，含 todo_write 规划、compaction 上下文压缩）→ 会话级沙箱（Docker/E2B/Cube 三后端统一抽象，shell_exec + read/write/edit_sandbox_file）→ ArtifactCollector 自动把 `/workspace/output` 排水为可下载 MessageArtifact。工作流引擎（Code/Agent 节点、checkpoint、发布快照、run_workflow 工具）是**互补能力而非必经之路**。

两位 advisor 独立得出同一结论、同一缺口排序（一致性 2/2）。最大缺口不在引擎，在**产品层与长任务生命周期**。

### 演进路径（三步，两位 advisor 路径合并去重）

**Phase A — 任务工作区产品化（最大杠杆、最短路径，后端近零新增）**
- 现有 agent 会话启用 shell+文件工具组合（已存在，能力门控自动注册）
- 打包「代码开发」preset skill：write_sandbox_file + shell_exec + 迭代修错提示词
- 沙箱镜像补 git/构建工具链（⚠️ 现有 docker 模板目录不保证含 git——Parent 已核 `docker_template_catalog.go`，只列标准模板镜像与 skill 快照）
- 前端补「任务工作区」视图：文件树 + 制品画廊 + 终端流（复用 `GET /api/v1/sessions/:id/artifacts` 与 artifacts SSE 事件）——**这是主要工作量所在**

**Phase B — 长任务基建（最大架构缺口）**
- 现状：agent Execute 绑定 HTTP 请求生命周期（SSE 同步），沙箱随会话销毁（`session.go:712 DestroySession`），仅 `session_sandbox_pin.go`（214 行）雏形
- 需新增：后台任务模型（异步队列 + 中途状态持久化），可反哺 workflow 的 checkpoint 与异步 run 模式

**Phase C — 工具面补齐（纯增量注册）**
- git 工具、浏览器自动化（headless browser，现仅 web_fetch——对标 Manus 最明显的工具缺口）、预览服务
- 成本护栏：租户级 token/时长/沙箱配额（现为 unlimited 迭代 + 无预算闸门）

### Files（若按此路径执行）

- 后端：`internal/application/service/session_sandbox_pin.go`（扩展长任务）、`internal/handler/session/agent_stream_handler.go`（回合生命周期）、`internal/agent/tools/definitions.go`（新工具注册）、`internal/sandbox/docker_template_catalog.go` + cube 模板（镜像工具链）
- 前端：`frontend/src/views/`（任务工作区视图）、`frontend/src/composables/useChatStreamHandler.ts`（artifacts 展示已有）、`frontend/src/api/chat/index.ts`
- 复用不动：`internal/agent/engine.go`、`internal/sandbox/` 全套、`artifact_collector.go`、`internal/agent/workflow/`

## 三、反馈处置（Accepted / Rejected / 裁决）

| 反馈 | 来源 | 处置 | 理由 |
|---|---|---|---|
| 「能做，基础已在库」+ 复用优先路径 | oracle, reviewer | ✅ 接受 | 双方独立以同一批一手代码证据（engine.go / capabilities.go / artifact_collector.go / nodes/code.go）支撑 |
| ⚠️「ArtifactCollector 可能只覆盖 skill 回合，MVP 工作量被低估」 | oracle（challengeClaim） | ❌ 证伪（对结论是利好） | Parent 裁决：`agent_stream_handler.go:696` 挂在**每回合**完成路径，no-op 条件仅为"未接线/无沙箱/无文件"；`shell_exec.go:230` 工具提示引导模型写 `/workspace/output`。oracle 自己的 changeMyMind 条款即为此情形："MVP 第一步消失，结论更强" |
| 「制品下载仅在 skill 链路验证，shell 写文件需端到端验证」 | reviewer（challengeClaim） | ✅ 已解答 | 同上，回合级收集覆盖 shell 路径；建议保留一条端到端冒烟验证 |
| 「镜像默认含 git/构建链」未验证 | oracle（assumption） | ⚠️ 部分证伪 | 模板目录不保证；Phase A 必须含镜像工作，排期勿低估 |
| 「docker 后端即可上线」之挑战 | reviewer | ➖ 部分接受 | 事实内核（单机一容器一会话、吞吐上限低）接受为风险；但无人主张"docker 即可上线"，双方均列为 owner decision（见下） |
| 「shell_exec 无 approval gate」 | oracle | ✅ 接受 | Parent 复核 grep 零命中；破坏性命令在会话沙箱内不可逆（产物目录除外），`internal/agent/approval/` 基建可扩展 |
| 「plans/workflow-improvements.md 已滞后于代码（Code/Agent 节点实际已落地）」 | oracle, reviewer | ✅ 接受 | `nodes/code.go`+`code_test.go`、`nodes/agent.go`、`run_workflow.go` 均在库；后续排期勿被该文档误导 |
| 「长任务需后台执行队列，工作量在集成层非引擎」 | reviewer, oracle | ✅ 接受（双方一致） | Phase B 立项依据 |

## 四、Owner Decisions（advisor 证据无法裁决，需领导/产品拍板）

1. **目标形态**：agent-first（聊天入口+技能+沙箱，两员推荐）还是 canvas-first（以画布为主轴交付）——决定资源投向。
2. **MVP 产物范围**：先做报告/文档/数据产物（现有链路近乎零增量）还是直接对标代码工程（需镜像+git+长任务投入）。
3. **沙箱生产后端**：单机 Docker（内网试点）vs Cube/E2B 集群（多租户规模），及每会话配额/空闲回收策略。
4. **代码任务的模型选型与评测**：内网可用模型是否胜任代码开发——架构就绪但模型不达标时产品仍会被判"做不到"，建议先用真实任务跑模型评测再承诺交付。
5. **长任务执行模型**：接受"会话即任务"（现状复用）还是投入后台队列+中途持久化（新基建）。
6. **是否引入浏览器自动化**：对标 Manus 最明显工具缺口，涉及新依赖与安全面扩大。

## 五、置信度与改变决策的条件

- **置信度：高**（两位 advisor 均自报 high；可行性结论建立在端到端一手代码证据上，非转述）。
- **会改变决策的条件**：
  1. 领导对「代码开发」的真实期待是 **IDE 级体验**（多文件工程、断点调试、协作）→ 这是另一个量级的产品，三步路径不足以覆盖，需重新对齐期望。
  2. 目标含**公网 SaaS 多租户** → 安全面结论需重审（现有 egress 白名单与租户沙箱配置面向内网私有化设计）。
  3. 内网**无集群后端**且只能单机 Docker → 沙箱密度成硬约束，Phase B/C 优先级重排。

## 六、运行记录（Evidence & Run IDs）

- **Roster**：无 `council-*` profile → 按协议回退填充 `oracle` + `reviewer`，两员齐备（**非降级模式**）。
  - `advisor-oracle` — oracle — **fork 上下文（context-aware，继承本会话）** — run `fe9dd6ae-bfd6-4776-922a-a3ec4f47ccb6`
  - `advisor-reviewer` — reviewer — 正常 profile 上下文（runtime-default）— run `4c952771-60a3-4c14-afa7-51931ee2bd1b`
- **Passes**：Pass 1（独立报告，结构化契约，<600 词，只读，无 peer 可见性）— workflow `52486dda-523a-4feb-8452-b034cffba2e3`，mission `d81c6d43`。**Pass 2 跳过**：唯一实质争议（collector 触发范围）经 Parent 一手证据裁决，剩余分歧均为 owner decisions/外部因素，收敛成立（pass cap 2 未触顶）。
- **Pass 1 计数**：完成 2/2；推荐一致 2/2；剩余实质争议 0。
- **Parent 侧裁决证据**（合成者一手核验，非 advisor 转述）：
  - `internal/handler/session/agent_stream_handler.go:696-712`（回合级 Collect）
  - `internal/application/service/artifact_collector.go:83-160`（turn completion 契约 + per-turn resolver）
  - `internal/agent/tools/shell_exec.go:230`（/workspace/output 引导）
  - `internal/sandbox/docker_template_catalog.go`（模板目录不含工具链保证）
  - `internal/application/service/session_sandbox_pin.go`（214 行，长任务雏形）
  - `internal/agent/workflow/compile.go:120-124,360-364`（多终端已支持——旧计划文档再次滞后）

## 七、给领导的一句话

**可以做，而且地基已经打好七成：引擎、沙箱、产物链路、工具体系都在，缺的是“任务工作区”的产品壳、长任务的跑法、和几个补齐工具。** 真正要先定的不是技术方案，而是上面第 1、2、4 条产品决策——尤其“代码开发”要做到什么深度，这决定了这是三个月的功能迭代还是另一个量级的产品。

## 八、执行记录（Plan 批准后）

已执行的不受 owner decisions 阻塞的 Phase A 切片：

1. ✅ **代码开发 preset skill**：新增 `skills/preloaded/code-developer/SKILL.md`（纯指导、零脚本；工具名与 `/workspace/output` 约定均对照源码核实）。文件系统自动发现，无需注册。
2. ✅ **计划文档勘误**：`plans/workflow-improvements.md` 顶部加“进度勘误”块——Code/Agent/MCP Tool 节点、`run_workflow` 工具、多终端约束放宽均已在库，后续排期以代码为准。
3. ✅ **产物链路冒烟（测试层）**：`go test ./internal/agent/skills/`、`go test ./internal/handler/session/ -run 'Artifact|Sandbox'`、`go test ./internal/application/service/ -run 'ArtifactCollector'` 全绿。

**审查记录**：reviewer（run `7a1e0dd9`，fresh）结论 OK with notes——主体全部核验通过（frontmatter 合规、六个工具名精确、/workspace/output 与超时表述、勘误块五条+两条附加断言均与代码一致）；1×P1（skill 教模型做 shell_exec 黑名单拒绝的后台启动）+ 2×P2（路径风格与工具描述相左、裸 pip 假设偏强）。三处已全部修复（P2 在同行同段，顺手修比记账便宜）：服务类验收改为自检脚本内启停；路径改为相对 /workspace 表述；pip 改 `python3 -m pip install --user`。修复后聚焦复审已后台启动（run `06d83b3d`）。**复审结果：三条全部 PASS，无新引入问题，审查闭环。**

4. ✅ **前端任务工作区（TaskWorkspaceDrawer）落地**：
   - **后端寻址根因修复**：修改 `GetSessionArtifacts` 查询返回 `[]types.SessionArtifact`（携带 `message_id` 和消息内 `index`），解决此前会话级产物列表缺少消息归属导致无法直接驱动下载端点的问题。
   - **会话级任务工作区抽屉**：新增 `frontend/src/views/chat/components/TaskWorkspaceDrawer.vue`，包含“产物”与“执行记录”双 Tab。产物列表复用 `DocumentPreview` 在线预览及 `downloadArtifact` 下载能力；执行记录自动聚合会话各轮助理消息的工具执行流（`agentEventStream`）。
   - **入口与挂载**：在 `frontend/src/views/chat/index.vue` 挂载抽屉，并在顶部右上方与 ChatHeader 标题栏对称放置圆形工作区入口按钮。
   - **全语言与构建自检**：四国语言（zh-CN, en-US, ko-KR, ru-RU）补齐 `taskWorkspace` 键；`check-i18n`、`vue-tsc --build`、`npm run build` 及后端单元测试全量通过。
   - **审查与缺陷闭环**：Reviewer 首轮（`eb35c4cc`）提出 2×P1（切会话/关抽屉未重置预览状态导致幽灵预览或 404；桌面端右侧引用面板展开遮挡入口按钮）+ 1×P2（预览头下载 loading 绑定 key 错误）。三处已全部采用最小 diff 修复并加入平滑动画过渡；聚焦复审（`5f9ee72b`）结果：三条全部 **PASS**，Merge verdict: **OK**，审查链闭环。

门控未动（待后续决策）：沙箱镜像 git/构建工具链、Phase B 长任务基建（异步队列/持久化）、Phase C 工具面（浏览器自动化等）。
