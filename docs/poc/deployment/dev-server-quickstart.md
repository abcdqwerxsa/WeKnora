# LuoSA 服务器开发模式（1 个通用镜像 + 改了代码即时生效）

> 适用场景：你在开发机上改代码，希望服务器上 **几秒内** 就能看到效果，不用每次 `docker build` + `docker compose up`。  
> 不适用于：生产环境（用 `docker-compose.yml` 的瘦镜像）。

## 1. 设计

把现有的 4 个生产镜像（ui / app / sandbox / docreader）换成 **1 个开发镜像 `luosa-dev:1.0`**，里面装了：
- Go 1.26 + air（后端 hot reload，~3 秒重编译）
- Node 22 + pnpm + Vite（前端 HMR，保存即刷新）
- Python 3.10/3.11（docreader / sandbox）
- uvicorn（Python 文档解析 `--reload`）

同一个镜像跑 4 个容器，每个容器靠 `SERVICE_ROLE` 环境变量决定启动什么进程：

| SERVICE_ROLE | 进程 | 端口 |
|--------------|------|------|
| `frontend` | `pnpm dev --host 0.0.0.0 --port 5173` | 5173 |
| `app` | `air` (Go 热重载) | 8080 |
| `docreader` | `uvicorn main:app --reload` | 5005 |
| `mcp-server` | `pnpm dev` | 按需 |

源码通过 bind mount 挂进容器，改完保存 → HMR/air/uvicorn 各自处理。

## 2. 一次性配置

### 服务器端

```bash
# 把代码 clone 到服务器（例如 /opt/luosa）
cd /opt/luosa
git clone <你的 fork> .

# 写 .env（开发模式默认账号即可，不要用线上强密码）
cat > .env <<EOF
FRONTEND_PORT=5173
APP_PORT=8080
DB_USERNAME=luosa
DB_PASSWORD=devpassword
DB_DATABASE=luosa
REDIS_PASSWORD=devredis
MINIO_ACCESS_KEY=minioadmin
MINIO_SECRET_KEY=devminio
EOF

# 首次构建通用镜像(只构建一次,后续除非改 Dockerfile.dev 否则不用再 build)
docker build -f docker/Dockerfile.dev -t luosa-dev:1.0 .

# 起所有服务
docker compose -f docker-compose.dev-server.yml up -d

# 看启动日志,确认 4 个 LuoSA 容器都 healthy
docker compose -f docker-compose.dev-server.yml ps
docker compose -f docker-compose.dev-server.yml logs -f app frontend
```

### 本地开发机

```bash
# 在本地 clone 同一份代码
git clone <你的 fork> ~/luosa

# 本地编辑后,推送到 fork 远端
cd ~/luosa
git add -A && git commit -m "feat: xxx" && git push origin main
```

## 3. 日常开发流程

### 改了前端代码（`.vue` / `.ts` / `.scss`）

**完全不需要在服务器做任何事** —— Vite HMR 会通过 bind mount 自动感知文件变化，浏览器刷新即可看到效果。

### 改了后端 Go 代码（`.go`）

`air` 在容器里监听文件变化，**自动重新编译并重启进程**，~3-5 秒生效。看日志：

```bash
docker compose -f docker-compose.dev-server.yml logs -f app
```

### 改了 docreader / sandbox Python 代码

`uvicorn --reload` 自动重启，~1-2 秒生效：

```bash
docker compose -f docker-compose.dev-server.yml logs -f docreader
```

### 改了依赖（`package.json` / `go.mod` / `pyproject.toml`）

依赖变更需要重新安装：

```bash
# 前端:删掉容器里的 node_modules,重装
docker compose -f docker-compose.dev-server.yml exec frontend rm -rf node_modules pnpm-lock.yaml
docker compose -f docker-compose.dev-server.yml restart frontend

# 后端:重启会触发 go mod download
docker compose -f docker-compose.dev-server.yml restart app

# docreader:重启会触发 pip install -e .
docker compose -f docker-compose.dev-server.yml restart docreader
```

### 改了 `Dockerfile.dev` / `entrypoint-dev.sh` / `.air.toml`

需要重新 build 通用镜像：

```bash
docker build -f docker/Dockerfile.dev -t luosa-dev:1.0 .
docker compose -f docker-compose.dev-server.yml up -d --force-recreate
```

## 4. 与生产 compose 的关系

| 用途 | 文件 |
|------|------|
| **生产部署** | `docker-compose.yml`（瘦镜像，运行时无工具链） |
| **本地开发** | `docker-compose.dev.yml`（基础设施 docker，app/frontend 在本机跑） |
| **服务器开发**（本文档） | `docker-compose.dev-server.yml`（1 个通用镜像 + bind mount） |

服务器开发模式 **不能** 用于给客户部署的镜像——那个必须用生产 compose + 我们 CI 推到 GHCR/Docker Hub 的瘦镜像。

## 5. 常见问题

### Q1：改了前端代码浏览器没自动刷新？

- 确认浏览器开了 Vite 的 HMR 客户端（DevTools Console 应该有 `[vite] connected`）
- 如果 bind mount 的 inode 变化太快（比如编辑器在保存前做了 tmp 文件再 rename），Vite 偶尔会卡。手动 `docker compose restart frontend` 兜底

### Q2：air 一直在重编译但失败？

- 看 `docker compose logs app` 里的 `build-errors.log`
- 修好代码后保存，air 自动重试

### Q3：服务器磁盘不够？

通用开发镜像约 2.5GB。如果磁盘紧张：
- 改完代码可以临时 `docker compose -f docker-compose.dev-server.yml stop` 停掉服务
- 不需要删镜像，下次 `up -d` 直接复用

### Q4：能不能用 remote container / VSCode devcontainer？

可以。把 `Dockerfile.dev` 作为 devcontainer 的 image build，VSCode 会自动 attach 到容器，IDE 体验更原生。本文档的 bind mount 配置已经兼容。

## 6. 验证清单

部署完跑一遍：

- [ ] 浏览器打开 `http://<服务器IP>:5173`，看到 LuoSA 登录页
- [ ] 登录 → 进聊天页 → 发条消息，能收到回复（说明 app + qdrant + postgres 都 OK）
- [ ] 在本地改一个 `.vue` 文件（比如改个文案）→ 保存 → 浏览器**无需刷新**即可看到变化
- [ ] 在本地改一个 `.go` 文件（比如改个 log 输出）→ 保存 → 看 `docker compose logs app` 出现 `air` 重启信息 → ~5 秒后发消息能验证到改动
- [ ] 浏览器打开 `http://<服务器IP>:9001`，MinIO 控制台能登录（说明对象存储 OK）
- [ ] 上传一个 PDF 到知识库，能成功入库（说明 docreader + embedding 模型 OK）

全部勾完 = 服务器开发模式部署成功。
