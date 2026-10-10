#!/usr/bin/env bash
# scripts/deploy.sh — 部署机上一键部署 luosa
#
# 用法（在部署机上）：
#   1. 上传 tar 包：scp luosa-deploy-v1.0.tar.gz user@server:/opt/luosa/
#   2. 解压：cd /opt/luosa && tar xzf luosa-deploy-v1.0.tar.gz
#   3. 进入解压目录：cd luosa-deploy-v1.0-staging/
#   4. 运行本脚本：./deploy.sh                   # 全量部署（front + up + 健康检查）
#      或：    ./deploy.sh --upgrade frontend   # 只升级某个 service（拉镜像 + up -d + 健康检查）
#
# 本脚本会：
#   - 检查 docker / docker compose
#   - 检查 .env（不存在则从 .env.example 复制）
#   - 自动生成 secrets 覆盖占位符
#   - docker login <registry>（交互；registry 从 .env 的 IMAGE_REGISTRY 解析，默认 ccr.ccs.tencentyun.com/jett2245）
#   - docker compose --profile full up -d
#   - 健康检查

set -euo pipefail

# ---------- 颜色 ----------
if [ -t 1 ]; then
    GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; BLUE='\033[0;34m'; NC='\033[0m'
else
    GREEN=''; YELLOW=''; RED=''; BLUE=''; NC=''
fi
log_info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERR ]${NC}  $*" >&2; }
log_ok()    { echo -e "${GREEN}[OK  ]${NC}  $*"; }

# ---------- 子命令解析 ----------
# 只支持 --upgrade <service>：只拉该 service 的镜像 + 重启，不动其它组件。
# 例如 ./deploy.sh --upgrade frontend，只升级前端；不重启 app/db/redis。
SUBCMD=""
UPGRADE_SERVICE=""
if [ "${1:-}" = "--upgrade" ]; then
    if [ -z "${2:-}" ]; then
        log_error "--upgrade 必须指定服务名（如 --upgrade frontend）"
        exit 1
    fi
    SUBCMD="upgrade"
    UPGRADE_SERVICE="$2"
    log_info "子命令: --upgrade $UPGRADE_SERVICE（只升级此服务，不动其它组件）"
fi

# ---------- 1. 预检 ----------
log_info "检查环境..."

command -v docker >/dev/null || { log_error "docker 未安装"; exit 1; }
docker info >/dev/null 2>&1 || { log_error "docker 服务未运行（systemctl start docker）"; exit 1; }
docker compose version >/dev/null 2>&1 || { log_error "docker compose 不可用（需要 Docker 23+）"; exit 1; }
command -v openssl >/dev/null || { log_error "openssl 未安装"; exit 1; }

[ -f docker-compose.yml ] || { log_error "docker-compose.yml 不在当前目录"; exit 1; }
log_ok "环境检查通过"

# ---------- 2. .env 处理 ----------
log_info "准备 .env..."

if [ ! -f .env ]; then
    if [ -f .env.example ]; then
        cp .env.example .env
        log_warn "已从 .env.example 创建 .env（接下来自动生成 secrets）"
    else
        log_error ".env 和 .env.example 都不存在"
        exit 1
    fi
fi

# 自动覆盖占位符 secrets（生产必填）
overridden=0
for var_pair in \
    "JWT_SECRET=luosa-jwt-secret:$(openssl rand -base64 48)" \
    "SYSTEM_AES_KEY=luosa-system-aes-key-32bytes!!:$(openssl rand -hex 16)" \
    "DB_PASSWORD=postgres123!@#:$(openssl rand -base64 24)" \
    "REDIS_PASSWORD=redis123!@#:$(openssl rand -base64 24)" \
    "MINIO_ACCESS_KEY_ID=your_minio_access_key:$(openssl rand -hex 12)" \
    "MINIO_SECRET_ACCESS_KEY=your_minio_secret_key:$(openssl rand -base64 32)"
do
    key="${var_pair%%:*}"
    placeholder="${var_pair#*:}"
    newval="${var_pair##*:}"
    if grep -qF "$placeholder" .env; then
        sed -i "s|^${key}=.*|${key}=${newval}|" .env
        overridden=$((overridden + 1))
    fi
done
if [ $overridden -gt 0 ]; then
    log_ok "已自动覆盖 $overridden 个 secrets 占位符"
fi

# ---------- 3. 解析 IMAGE_REGISTRY ----------
# 从 .env 读 IMAGE_REGISTRY；没设置就用 docker-compose.yml 的默认值 ccr.ccs.tencentyun.com/jett2245
# 注意 docker-compose 里写的是 ${IMAGE_REGISTRY:-ccr.ccs.tencentyun.com/jett2245}，所以这里要 fallback 到相同的值
REGISTRY_HOST=$(grep -E "^IMAGE_REGISTRY=" .env 2>/dev/null | head -1 | cut -d= -f2- | tr -d ' "' || echo "")
REGISTRY_HOST="${REGISTRY_HOST:-ccr.ccs.tencentyun.com/jett2245}"
log_info "镜像 registry: ${REGISTRY_HOST}"

# ---------- 4. 端口冲突检查 ----------
log_info "检查端口冲突..."

# 兜底：如果变量在 .env 里没设，grep 不会失败导致脚本退出
get_env_var() {
    grep -E "^${1}=" .env 2>/dev/null | head -1 | cut -d= -f2 | tr -d ' "' || echo ""
}

FRONTEND_PORT=$(get_env_var FRONTEND_PORT)
APP_PORT=$(get_env_var APP_PORT)
MCP_PORT=$(get_env_var MCP_PORT)
POSTGRES_HOST_PORT=$(get_env_var POSTGRES_HOST_PORT)
REDIS_HOST_PORT=$(get_env_var REDIS_HOST_PORT)
MINIO_PORT=$(get_env_var MINIO_PORT)

check_port() {
    local port="$1"
    local service="$2"
    if [ -z "$port" ]; then
        log_warn "${service} 端口未设置（跳过检查）"
        return
    fi
    if ss -tlnp 2>/dev/null | grep -qE ":${port} "; then
        local who=$(ss -tlnp 2>/dev/null | grep -E ":${port} " | head -1 | awk '{print $NF}')
        log_warn ":${port} (${service}) 已被占用: ${who}"
    else
        log_ok ":${port} (${service}) 空闲"
    fi
}

check_port "${FRONTEND_PORT:-80}" "frontend"
check_port "${APP_PORT:-8080}" "app"
check_port "${MCP_PORT:-8082}" "mcp"
check_port "${POSTGRES_HOST_PORT:-15432}" "postgres"
check_port "${REDIS_HOST_PORT:-16379}" "redis"
check_port "${MINIO_PORT:-9000}" "minio"

# ---------- 5. 拉镜像（如果未登录会自然报错，再提示登录）----------
PULL_ARGS=(--env-file .env --profile full)
if [ "$SUBCMD" = "upgrade" ] && [ -n "$UPGRADE_SERVICE" ]; then
    PULL_ARGS+=(pull "$UPGRADE_SERVICE")
    log_info "拉镜像（只升级 $UPGRADE_SERVICE；如未登录会提示登录 ${REGISTRY_HOST}）..."
else
    PULL_ARGS+=(pull)
    log_info "拉镜像（如果失败会提示登录 ${REGISTRY_HOST}）..."
fi

if ! docker compose "${PULL_ARGS[@]}" 2>&1 | tee /tmp/luosa-pull.log; then
    if grep -q "unauthorized\|denied\|not authorized" /tmp/luosa-pull.log; then
        log_warn "${REGISTRY_HOST} 未登录或凭证错误"
        if [ -t 0 ]; then
            docker login "$REGISTRY_HOST"
            log_info "重新拉镜像..."
            docker compose "${PULL_ARGS[@]}"
        else
            log_error "非交互模式，请先手动 'docker login ${REGISTRY_HOST}' 再重跑"
            exit 1
        fi
    else
        log_error "拉镜像失败，请看 /tmp/luosa-pull.log"
        exit 1
    fi
fi
log_ok "镜像拉取完成"

# ---------- 6. 启动 ----------
if [ "$SUBCMD" = "upgrade" ] && [ -n "$UPGRADE_SERVICE" ]; then
    log_info "重启 $UPGRADE_SERVICE..."
    docker compose --env-file .env up -d "$UPGRADE_SERVICE"
else
    log_info "启动容器..."
    docker compose --env-file .env --profile full up -d
fi

# ---------- 7. 健康检查 ----------
log_info "健康检查（等待 15 秒）..."
sleep 15

echo
log_info "=== 容器状态 ==="
docker compose --env-file .env --profile full ps

echo
log_info "=== 关键服务探活 ==="

# postgres
if docker compose --env-file .env exec -T postgres pg_isready -U "${DB_USER:-luosa}" 2>/dev/null; then
    log_ok "postgres OK"
else
    log_warn "postgres 未就绪（首次启动可能要等 1-2 分钟）"
fi

# frontend
frontend_url="http://localhost:${FRONTEND_PORT:-80}"
if curl -fsS "${frontend_url}/health" -o /dev/null 2>/dev/null; then
    log_ok "frontend OK (${frontend_url})"
else
    log_warn "frontend 未就绪（首次启动可能要等 1-2 分钟）"
    log_info "看 app 日志：docker compose logs app --tail 50"
fi

echo
log_ok "部署完成！"
echo
echo "  浏览器访问：  ${frontend_url}"
echo "  MCP 端口：    ${MCP_PORT:-8082}"
echo "  Postgres：    localhost:${POSTGRES_HOST_PORT:-15432}  (psql -h localhost -p ${POSTGRES_HOST_PORT:-15432} -U luosa)"
echo "  Redis：       localhost:${REDIS_HOST_PORT:-16379}    (redis-cli -h localhost -p ${REDIS_HOST_PORT:-16379})"
echo "  MinIO：       localhost:${MINIO_PORT:-9000}          (http://localhost:${MINIO_PORT:-9001} 控制台)"
echo
echo "  常用命令："
echo "    docker compose --env-file .env --profile full ps        # 查看状态"
echo "    docker compose --env-file .env --profile full logs -f   # 跟踪日志"
echo "    docker compose --env-file .env --profile full down      # 停止"