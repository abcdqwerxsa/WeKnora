#!/usr/bin/env bash
# scripts/build_app.sh — 单镜像 build 脚本：只构建后端 app 镜像（Dockerfile.app）
#
# 用途：相比 build_local.sh（一次 build 5 个镜像），
#       本脚本只 build app，适合「改了后端代码、只想重新出 app 镜像并 push」的工作流。
#
# 用法：
#   ./scripts/build_app.sh                                    # 默认: quay.io/jett_chen/luosa:app-v1.1, 跳过 anydoc
#   ./scripts/build_app.sh --version v1.2                     # bump 版本号（最常用，推荐）
#   ./scripts/build_app.sh --tag luosa:app-special            # 全 tag 覆盖（advanced）
#   ./scripts/build_app.sh --registry harbor.example.com --namespace myteam
#   ./scripts/build_app.sh --with-anydoc                      # 编 anydoc（office/pdf 原生解析，约 +5 分钟 / +1GB）
#   APT_MIRROR=mirrors.tuna.tsinghua.edu.cn ./scripts/build_app.sh
#   GOPROXY=https://goproxy.io,direct ./scripts/build_app.sh
#
# 关键 build-arg:
#   - WITH_ANYDOC=0（默认）/1       1 会触发 Rust 工具链下载 + 编译，首次 ~5 分钟
#   - APK_MIRROR_ARG=<mirror host>  替换 deb.debian.org（注意 ARG 名是 APK_ 不是 APT_，
#                                   Dockerfile.app 里叫 APK_MIRROR_ARG，
#                                   之前 build_local.sh 传的是 APT_MIRROR，压根没生效）
#   - GOPROXY_ARG=<proxy>           Go module 代理，默认 https://goproxy.cn,direct
#
# 产物: 本地 tag 为 luosa-app:latest（与 build_local.sh 对齐，方便 push.sh --only app 直接用）
#       同时额外打一个远程 target tag（默认 quay.io/jett_chen/luosa:app-v1.1）
#
# 与 scripts/push.sh 的衔接:
#   build 后用 ./scripts/push.sh --only app --version v1.1 推到 quay.io。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

# ---------- 默认值 ----------
REGISTRY="${REGISTRY:-quay.io}"
NAMESPACE="${NAMESPACE:-jett_chen}"
REPO="${REPO:-luosa}"
VERSION="${VERSION:-v1.1}"                              # 当前 in-flight 的 bug fix 版本
LOCAL_TAG="${LOCAL_TAG:-luosa-app:latest}"              # 与 build_local.sh 一致，方便 push.sh 接力
WITH_ANYDOC="${WITH_ANYDOC:-0}"                        # 默认跳过 Rust 工具链，省 5 分钟
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
GOSUMDB="${GOSUMDB:-off}"
APT_MIRROR="${APT_MIRROR:-mirrors.aliyun.com}"          # 与 build_local.sh 对齐；空字符串表示不改源

LOG_DIR="${LOG_DIR:-/tmp}"

# ---------- 颜色 ----------
# 默认空（无 TTY / -u 模式下 unset 会炸）；TTY 模式下覆盖为 ANSI 色码。
GREEN=''; YELLOW=''; RED=''; BLUE=''; NC=''
if [ -t 1 ]; then
    GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; BLUE='\033[0;34m'; NC='\033[0m'
fi
log_info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERR ]${NC}  $*" >&2; }
log_ok()    { echo -e "${GREEN}[OK  ]${NC}  $*"; }

# ---------- 平台 ----------
case "$(uname -m)" in
    x86_64)        PLATFORM="linux/amd64" ;;
    aarch64|arm64) PLATFORM="linux/arm64" ;;
    *) log_warn "未识别平台 $(uname -m)，默认 linux/amd64"
       PLATFORM="linux/amd64" ;;
esac

# ---------- 参数解析 ----------
REMOTE_TAG_OVERRIDE=""
while [ $# -gt 0 ]; do
    case "$1" in
        --tag)                REMOTE_TAG_OVERRIDE="$2"; shift ;;
        --registry)           REGISTRY="$2"; shift ;;
        --namespace)          NAMESPACE="$2"; shift ;;
        --repo)               REPO="$2"; shift ;;
        --version)            VERSION="$2"; shift ;;
        --with-anydoc)        WITH_ANYDOC=1 ;;
        --no-anydoc)          WITH_ANYDOC=0 ;;
        --local-tag)          LOCAL_TAG="$2"; shift ;;
        -h|--help)
            # 只显示脚本最顶部的连续注释块（文件头部 doc）。
            # 跳过 shebang、纯 `#` 行、章节分隔 `# ----`，
            # 其他注释行去掉前导 `# ` 后输出。
            awk 'NR>1 && /^#/ && !/^# ----/ && !/^#$/ {print substr($0,3)}' "$0"
            exit 0 ;;
        *) log_error "未知参数: $1"; exit 1 ;;
    esac
    shift
done

REMOTE_TAG="${REMOTE_TAG_OVERRIDE:-${REPO}:app-${VERSION}}"
REMOTE_IMAGE="${REGISTRY}/${NAMESPACE}/${REMOTE_TAG}"

# ---------- 预检 ----------
command -v docker >/dev/null || { log_error "docker 未安装"; exit 1; }
docker info >/dev/null 2>&1 || { log_error "docker 服务未运行"; exit 1; }
docker buildx version >/dev/null 2>&1 || { log_error "buildx 不可用"; exit 1; }

# ---------- 版本信息 ----------
VERSION_PKG=$(cat VERSION 2>/dev/null | tr -d '\n\r' || echo "unknown")
COMMIT_ID=$(git -C "$PROJECT_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
GO_VERSION=$(go version 2>/dev/null | awk '{print $3}' || echo "unknown")

log_info "目标镜像:    ${REMOTE_IMAGE}"
log_info "本地 tag:    ${LOCAL_TAG}"
log_info "平台:        ${PLATFORM}"
log_info "VERSION:     ${VERSION_PKG}  COMMIT: ${COMMIT_ID}  GO: ${GO_VERSION}"
log_info "WITH_ANYDOC: ${WITH_ANYDOC}  (0=跳过 Rust, 1=编 anydoc 引擎)"
log_info "GOPROXY:     ${GOPROXY}"
log_info "APT 镜像源:  ${APT_MIRROR:-<默认 deb.debian.org>}"
echo

# ---------- 执行 build ----------
log_file="${LOG_DIR}/luosa-build-app.log"
log_info "开始 build（log: ${log_file}）..."

if ! docker buildx build \
    --load \
    --platform "$PLATFORM" \
    --build-arg VERSION_ARG="$VERSION_PKG" \
    --build-arg COMMIT_ID_ARG="$COMMIT_ID" \
    --build-arg BUILD_TIME_ARG="$BUILD_TIME" \
    --build-arg GO_VERSION_ARG="$GO_VERSION" \
    --build-arg GOPROXY_ARG="$GOPROXY" \
    --build-arg GOSUMDB_ARG="$GOSUMDB" \
    --build-arg WITH_ANYDOC="$WITH_ANYDOC" \
    --build-arg APK_MIRROR_ARG="$APT_MIRROR" \
    -f docker/Dockerfile.app \
    -t "$LOCAL_TAG" \
    -t "$REMOTE_IMAGE" \
    . > "$log_file" 2>&1; then
    log_error "build 失败，请查看 log:"
    tail -30 "$log_file" | sed 's/^/  /'
    exit 1
fi

# ---------- 结果 ----------
size_local=$(docker images --format '{{.Size}}' "$LOCAL_TAG" | head -1)
size_remote=$(docker images --format '{{.Size}}' "$REMOTE_IMAGE" | head -1)
log_ok "build 完成"
echo
log_info "本地镜像:"
echo "    ${LOCAL_TAG}         (${size_local})"
echo "    ${REMOTE_IMAGE}  (${size_remote})"
echo
log_info "下一步推送:"
echo "    docker push ${REMOTE_IMAGE}"
echo "    # 或用 scripts/push.sh（自动批量打 tag + push）:"
echo "    ./scripts/push.sh --only app --version ${VERSION}"
echo
log_info "部署时指定版本:"
echo "    LUOSA_VERSION=${VERSION} docker compose -f docker-compose.yml up -d app"