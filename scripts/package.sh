#!/usr/bin/env bash
# scripts/package.sh — 把"部署需要的最小文件集"打包
#
# 用法：
#   ./scripts/package.sh                              # 用默认配置
#   LUOSA_VERSION=v1.1 ./scripts/package.sh           # 自定义版本号
#   OUTPUT=/tmp/my.tar.gz ./scripts/package.sh        # 自定义输出路径
#
# 打包内容：
#   docker-compose.yml       （image 指向 quay.io/jett_chen/luosa）
#   .env（或从 .env.example 复制）
#   config/config.yaml       （bind mount 必需）
#   config/prompt_templates/（业务配置，按需）
#   skills/preloaded/        （自定义 skill，按需）
#   scripts/build_local.sh   （构建脚本，方便）
#   scripts/push.sh          （推送脚本，方便）
#
# 输出：
#   /tmp/luosa-deploy-<version>.tar.gz

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

# ---------- 参数 ----------
LUOSA_VERSION="${LUOSA_VERSION:-v1.0}"
PKG_NAME="luosa-deploy-${LUOSA_VERSION}"
STAGING="/tmp/${PKG_NAME}-staging"
OUTPUT="${OUTPUT:-/tmp/${PKG_NAME}.tar.gz}"

# ---------- 颜色 ----------
if [ -t 1 ]; then
    GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
else
    GREEN=''; YELLOW=''; BLUE=''; NC=''
fi
log_info() { echo -e "${BLUE}[INFO]${NC}  $*"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_ok()   { echo -e "${GREEN}[OK  ]${NC}  $*"; }

# ---------- 清理 staging ----------
rm -rf "$STAGING"
mkdir -p "$STAGING"

# ---------- 必须文件 ----------
log_info "打包必须文件"

# docker-compose.yml
if [ -f docker-compose.yml ]; then
    cp docker-compose.yml "$STAGING/"
    log_ok "  docker-compose.yml"
else
    log_error "docker-compose.yml 不存在"
    exit 1
fi

# .env 或 .env.example
if [ -f .env ]; then
    cp .env "$STAGING/.env"
    log_ok "  .env (已存在)"
else
    if [ -f .env.example ]; then
        cp .env.example "$STAGING/.env"
        log_warn "  .env 不存在 → 从 .env.example 复制（生产部署前请改 secrets）"
    else
        log_error ".env 和 .env.example 都不存在"
        exit 1
    fi
fi

# config/config.yaml（硬编码 bind mount 必需）
mkdir -p "$STAGING/config"
if [ -f config/config.yaml ]; then
    cp config/config.yaml "$STAGING/config/"
    log_ok "  config/config.yaml"
else
    log_warn "  config/config.yaml 不存在（部署会失败，请创建后重试）"
fi

# ---------- 可选文件 ----------
log_info "打包可选文件"

if [ -d config/prompt_templates ]; then
    cp -r config/prompt_templates "$STAGING/config/"
    log_ok "  config/prompt_templates/"
else
    log_warn "  config/prompt_templates/ 不存在（跳过）"
fi

if [ -f config/builtin_agents.yaml ]; then
    cp config/builtin_agents.yaml "$STAGING/config/" 2>/dev/null || true
    log_ok "  config/builtin_agents.yaml"
fi

if [ -f config/builtin_models.yaml ]; then
    cp config/builtin_models.yaml "$STAGING/config/" 2>/dev/null || true
    log_ok "  config/builtin_models.yaml"
fi

# skills/preloaded/（硬编码 bind mount，必带 —— 哪怕空目录）
if [ -d skills/preloaded ]; then
    cp -r skills/preloaded "$STAGING/skills/"
    log_ok "  skills/preloaded/ ($(find skills/preloaded -name 'SKILL.md' | wc -l) 个 Skill)"
else
    mkdir -p "$STAGING/skills/preloaded"
    log_warn "  skills/preloaded/ 不存在（创建空目录占位）"
fi

# docker/searxng/（searxng 服务 bind mount 必需）
mkdir -p "$STAGING/docker"
if [ -d docker/searxng ]; then
    cp -r docker/searxng "$STAGING/docker/"
    log_ok "  docker/searxng/ (searxng 配置，bind mount 必需)"
else
    log_warn "  docker/searxng/ 不存在（searxng-init 会失败，searxng 服务起不来）"
fi

# scripts/（只打包客户机用的脚本）
# 开发脚本（build_local.sh / push.sh / package.sh）不打包，避免客户误用
for f in deploy.sh; do
    if [ -f "scripts/$f" ]; then
        mkdir -p "$STAGING/scripts"
        cp "scripts/$f" "$STAGING/scripts/"
        chmod +x "$STAGING/scripts/$f"
        log_ok "  scripts/$f"
    fi
done

# ---------- 打包 ----------
log_info "压缩中..."
tar czf "$OUTPUT" -C "$(dirname "$STAGING")" "$(basename "$STAGING")"

# ---------- 输出 ----------
rm -rf "$STAGING"
size=$(du -h "$OUTPUT" | awk '{print $1}')
log_ok "打包完成: $OUTPUT ($size)"
echo
echo "内容清单："
tar tzf "$OUTPUT" | sort | head -30
echo "  ..."
echo
echo "=== 部署步骤 ==="
echo "  1. 上传：  scp $OUTPUT user@server:/opt/luosa/"
echo "  2. 解压：  ssh user@server 'cd /opt/luosa && tar xzf luosa-deploy-${LUOSA_VERSION}.tar.gz'"
echo "  3. 编辑 .env（改 secrets + 端口）"
echo "  4. 启动：  docker compose --env-file .env --profile full up -d"
echo "  5. 验证：  curl http://localhost:<端口>/health"