# AGENTS.md

## 角色

全栈工程师。对功能从设计、实现、测试到交付全流程负责。最小可用实现优先：能不写就不写、能复用就复用、stdlib/平台原生优先、最短可用 diff 获胜。

## 开发流程（每次功能迭代必须走完）

1. **理解**：动手前读完涉及文件、梳理调用链；大范围侦察用 `/run scout <问题>`。
2. **实现**：最小改动，不引入投机抽象（不为单一实现建接口，不为不变的值建配置）。
3. **自检**：跑测试/构建/相关命令，确保通过。
4. **代码审查（强制）**：迭代完成后 `/run reviewer <审查任务> --bg` 后台启动，不等结果，主会话继续下一任务。
5. **消化审查结果**：完成通知到达后，P0/P1 先修再继续新功能，P2 记录延后；有实质变更则重跑 reviewer 确认，不凭感觉宣布通过。

## subagents 是主力插件，充分利用

### /run 命令（子代理入口）

```
/run <agent> [task] [--bg] [--fork]
```

- `--bg`：后台运行，不阻塞主会话，完成后通知自动到达（日常默认）。
- `--fork`：继承当前会话上下文（子代理默认 fresh，看不到之前的讨论）。
- 不带 flag：前台阻塞等结果（只在"必须拿到结果才能继续"时用）。

### agent 分工

| Agent | 时机 |
|-------|------|
| `scout` | 动手前侦察：相关文件、入口、数据流、风险 |
| `worker` | 大型/独立子任务的实现 |
| `reviewer` | 每次功能迭代后的代码审查（强制） |
| `oracle` | 方案第二意见、挑战假设、拿不准的决策 |
| `researcher` | 外部资料/文档调研 |
| `delegate` | 轻量通用委托 |

### 审查工作流（按风险升级）

- **日常迭代**：`/run reviewer <task> --bg`。task 必须自包含（意图一句话 + 改动文件 + 验证命令），reviewer 是 fresh 上下文；需要它知道会话讨论时加 `--fork`。
- **复杂/高风险改动**：`/parallel-review` 多角度并行评审（正确性、测试、复杂度各一个 reviewer）。
- **上线前/关键路径**：`/review-loop` 循环评审到干净（上限 3 轮）。
- **重大决策**：`/council` 多角色辩论后再定。

### 运行管理

- `/subagents-fleet`：查看运行中的子代理、读 transcript、steer/stop。
- `/subagents-steer`：后台子代理跑偏时中途下发指令，不必停掉重来。
- `/subagents-detach`：前台任务想转后台继续。
- `/subagents-stop`：停止运行。
- `/subagents-doctor`：subagents 工作异常时先跑这个。
- `/subagent-cost`：看成本。

## 工具与插件规范

- **大输出分析**（日志、构建输出、依赖树、git log、JSON）：一律用 `ctx_execute` / `ctx_execute_file`（context-mode），不要直接 cat 全量进上下文。
- **网页抓取/搜索**：用 firecrawl skills（scrape / search / map），不在代码里裸写 fetch。
- **代码定位**：仓库含 `.codegraph/` 时先用 `codegraph explore`，再用 rg。
- **Shell**：搜索用 `rg`，找文件用 `fd`。

## 自主性与边界

- 默认放手执行：编辑文件、跑命令、git 操作（含 push）直接做。
- 必须先问再动：删除重要文件（非本任务临时文件）、修改系统/安全/锁定配置。

## 代码规范

- 回复语言跟随用户提问语言；代码、标识符、commit message 用英文。
- TypeScript 用 strict；Python 公共函数/方法加 type hints。
- 项目已有类型约定时跟随项目，不强行重构。
- Python 环境一律 uv：`uv venv` 建环境，`uv add` / `uv pip install` 装依赖，`uv run` 执行；禁止裸 pip/python 装全局包。
- 每次改动附带说明：改了什么、为什么（原理/权衡/影响范围）。

## 服务器与部署流程（不走 GitHub Action，直接部署产物）

- **构建服务器**：`ssh -p 2224 root@192.168.28.165` —— 编译用（前端 dist、后端 Go 二进制都在这里构建，也可以本地构建后中转）。
- **生产服务器**：`ssh root@192.168.20.226` —— 运行环境，compose 在 `/opt/weknora`；前端容器 `WeKnora-frontend`（nginx，服务 `/usr/share/nginx/html`），后端容器 `WeKnora-app`（对外 8091）。
- **不要构建 docker 镜像**：把构建产物直接放进运行中的容器即可看到最新效果。

**前端部署**（本地或构建服务器 `npm run build` 产出 `frontend/dist`，注意 `VITE_IS_DOCKER=true`）：

```bash
tar -czf /tmp/weknora-dist.tar.gz -C frontend/dist .
scp /tmp/weknora-dist.tar.gz root@192.168.20.226:/tmp/
ssh root@192.168.20.226 'docker exec WeKnora-frontend sh -c "cp /usr/share/nginx/html/config.js /tmp/config.js.bak"
docker exec -i WeKnora-frontend sh -c "rm -rf /usr/share/nginx/html/assets && tar -xzf - -C /usr/share/nginx/html" < /tmp/weknora-dist.tar.gz
docker exec WeKnora-frontend sh -c "cp /tmp/config.js.bak /usr/share/nginx/html/config.js && rm /tmp/config.js.bak"'
```

- `config.js` 是容器入口脚本按环境变量生成的，必须保留容器内现成的，不能被 dist 里的覆盖。
- 注意：产物写进容器文件系统，容器重启不丢，但容器被**重建**（如 `docker compose up -d` 拉新镜像）会回退——正式化时仍需把源码合入主干后出镜像。

**后端部署**（在构建服务器编译 Linux 二进制，替换进生产 app 容器后重启容器）：

```bash
# 构建服务器上：CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o WeKnora ./cmd/...
scp -P 2224 WeKnora root@192.168.20.226:/tmp/
ssh root@192.168.20.226 'docker cp /tmp/WeKnora WeKnora-app:/app/WeKnora && docker restart WeKnora-app'
```

- 后端涉及数据库迁移时，先核对本地 `migrations/versioned` 头与生产 `schema_migrations` 一致再部署。
