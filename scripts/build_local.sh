#!/usr/bin/env bash
# scripts/build_local.sh — 本地构建 WeKnora 5 个镜像，tag 用 luosa-* 前缀
#
# 用途：实验环境本机构建，不推 registry（与 scripts/build_images.sh 的 wechatopenai/* 不同）
#
# 用法：
#   ./scripts/build_local.sh                   # 全部 build，tag: luosa-<name>:latest
#   ./scripts/build_local.sh app frontend      # 只 build 指定镜像
#   IMAGE_TAG=v1.2.3 ./scripts/build_local.sh  # 自定义 tag
#   IMAGE_PREFIX=mytest- ./scripts/build_local.sh  # 自定义前缀
#
# 构建产物：
#   luosa-app:latest            — Go 后端（Dockerfile.app）
#   luosa-frontend:latest       — Vue3 前端（frontend/Dockerfile）
#   luosa-docreader:latest      — Python gRPC 文档解析（Dockerfile.docreader）
#   luosa-sandbox:latest        — Skills 沙箱
#   luosa-sandbox:main          — 兼容 sandbox.go 的 DefaultDockerImage（默认拉 :main）
#   luosa-sandbox:latest-cube   — Cube 变体（含 envd，固定 linux/amd64）
#   luosa-sandbox:main-cube
#   luosa-mcp:latest            — MCP Server（compose 里没 image，需要单独 build）
#
# 失败处理：任一镜像失败立即退出，log 在 /tmp/luosa-build-<name>.log

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

# ---------- 默认值 ----------
export IMAGE_PREFIX="${IMAGE_PREFIX:-luosa-}"
export IMAGE_TAG="${IMAGE_TAG:-latest}"
LOG_DIR="${LOG_DIR:-/tmp}"

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

# ---------- 预检 ----------
command -v docker >/dev/null || { log_error "docker 未安装"; exit 1; }
docker info >/dev/null 2>&1 || { log_error "docker 服务未运行"; exit 1; }

# ---------- 平台探测 ----------
case "$(uname -m)" in
    x86_64)        PLATFORM="linux/amd64"; TARGETARCH="amd64" ;;
    aarch64|arm64) PLATFORM="linux/arm64"; TARGETARCH="arm64" ;;
    *) log_warn "未识别平台 $(uname -m)，默认 linux/amd64"
       PLATFORM="linux/amd64"; TARGETARCH="amd64" ;;
esac
log_info "平台: $PLATFORM"

# ---------- 版本信息（注入到 app 镜像 build-arg）----------
VERSION=$(cat VERSION 2>/dev/null | tr -d '\n\r' || echo "unknown")
COMMIT_ID=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
GO_VERSION=$(go version 2>/dev/null || echo "unknown")
log_info "VERSION=$VERSION  COMMIT=$COMMIT_ID"

# ---------- 选择目标 ----------
TARGETS=("$@")
if [ ${#TARGETS[@]} -eq 0 ]; then
    TARGETS=(app frontend docreader sandbox mcp)
fi
log_info "目标: ${TARGETS[*]}   前缀: $IMAGE_PREFIX   tag: $IMAGE_TAG"

# ---------- build 单个镜像 ----------
build_image() {
    local name="$1" dockerfile="$2" context="$3"
    shift 3
    local target_arg=() tag_args=()
    while [ $# -gt 0 ]; do
        case "$1" in
            --target)        target_arg=( --target "$2" ); shift ;;
            --target=*)      target_arg=( --target "${1#--target=}" ) ;;
            *)               tag_args+=( -t "$1" ) ;;
        esac
        shift
    done

    log_info "build ${name}  (${dockerfile})"
    local log="${LOG_DIR}/luosa-build-${name}.log"
    # Dockerfile.app 定义的 ARG 名是 APK_MIRROR_ARG（不是 APT_MIRROR），
    # 之前用 APT_MIRROR 一直没生效——镜像源替换是死代码。
    if docker buildx build \
        --load \
        --platform "$PLATFORM" \
        --build-arg VERSION_ARG="$VERSION" \
        --build-arg COMMIT_ID_ARG="$COMMIT_ID" \
        --build-arg BUILD_TIME_ARG="$BUILD_TIME" \
        --build-arg GO_VERSION_ARG="$GO_VERSION" \
        --build-arg GOPROXY_ARG="${GOPROXY:-https://goproxy.cn,direct}" \
        --build-arg GOSUMDB_ARG="${GOSUMDB:-off}" \
        --build-arg WITH_ANYDOC="${WITH_ANYDOC:-1}" \
        --build-arg APK_MIRROR_ARG="${APT_MIRROR:-}" \
        -f "$dockerfile" \
        "${target_arg[@]}" \
        "${tag_args[@]}" \
        "$context" > "$log" 2>&1; then
        local size
        size=$(docker images --format '{{.Size}}' "${IMAGE_PREFIX}${name}:${IMAGE_TAG}" | head -1)
        log_ok "${name} ✓  ($size)  log: $log"
    else
        log_error "${name} ✗  log: $log"
        echo "  --- 最后 30 行 log ---"
        tail -30 "$log" | sed 's/^/  /'
        return 1
    fi
}

# ---------- 跑 ----------
for t in "${TARGETS[@]}"; do
    case "$t" in
        app)
            build_image "app" "docker/Dockerfile.app" "." \
                "${IMAGE_PREFIX}app:${IMAGE_TAG}"
            ;;
        frontend)
            # 前端需要先 build dist
            log_info "build frontend dist (调 build_frontend_dist.sh)"
            if ! VITE_IS_DOCKER=true VITE_FRONTEND_COMMIT="$COMMIT_ID" \
                "$SCRIPT_DIR/build_frontend_dist.sh" > "${LOG_DIR}/luosa-build-frontend-dist.log" 2>&1; then
                log_error "build_frontend_dist.sh 失败"
                tail -30 "${LOG_DIR}/luosa-build-frontend-dist.log" | sed 's/^/  /'
                exit 1
            fi
            build_image "frontend" "frontend/Dockerfile" "frontend/" \
                "${IMAGE_PREFIX}frontend:${IMAGE_TAG}"
            ;;
        docreader)
            build_image "docreader" "docker/Dockerfile.docreader" "." \
                "${IMAGE_PREFIX}docreader:${IMAGE_TAG}"
            ;;
        sandbox)
            # sandbox 同时打 :latest 和 :main（sandbox.go 默认拉 :main）
            build_image "sandbox" "docker/Dockerfile.sandbox" "." \
                "${IMAGE_PREFIX}sandbox:${IMAGE_TAG}" \
                "${IMAGE_PREFIX}sandbox:main" \
                --target sandbox
            # Cube 变体（含 envd，固定 amd64）
            log_info "build sandbox cube variant (linux/amd64)"
            local_log="${LOG_DIR}/luosa-build-sandbox-cube.log"
            if docker buildx build \
                --load \
                --platform linux/amd64 \
                -f docker/Dockerfile.sandbox \
                --target cube \
                -t "${IMAGE_PREFIX}sandbox:${IMAGE_TAG}-cube" \
                -t "${IMAGE_PREFIX}sandbox:main-cube" \
                . > "$local_log" 2>&1; then
                log_ok "sandbox-cube ✓  log: $local_log"
            else
                log_error "sandbox-cube ✗  log: $local_log"
                tail -30 "$local_log" | sed 's/^/  /'
                exit 1
            fi
            ;;
        mcp)
            build_image "mcp" "mcp-server/Dockerfile" "mcp-server/" \
                "${IMAGE_PREFIX}mcp:${IMAGE_TAG}"
            ;;
        *)
            log_warn "未知目标: $t （跳过）"
            ;;
    esac
done

# ---------- 汇总 ----------
echo
log_ok "=== 构建完成 ==="
docker images --format "table {{.Repository}}:{{.Tag}}\t{{.Size}}\t{{.CreatedSince}}" \
    | (read -r header; echo "$header"; grep "^${IMAGE_PREFIX}" | sort)
