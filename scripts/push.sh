#!/usr/bin/env bash
# scripts/push.sh — 把本地 luosa-* 镜像 tag 成 <registry>/<namespace>/<repo>:<service>-v<version> 然后 push
#
# 默认目标：
#   quay.io/jett_chen/luosa:<service>-v1.0
#
# 用法：
#   ./scripts/push.sh                                        # 默认 push 到 quay.io/jett_chen/luosa，v1.0
#   ./scripts/push.sh --registry harbor.example.com --namespace myteam --repo luosa --version v1.2.3
#   ./scripts/push.sh --dry-run                              # 只 tag，不 push（看 tag 后长啥样）
#   ./scripts/push.sh --only docreader                       # 只 push docreader
#
# 镜像清单（本地 → 远程 tag）：
#   luosa-app:latest                       → app-v<version>
#   luosa-frontend:latest                  → frontend-v<version>
#   luosa-sandbox:latest                   → sandbox-v<version>
#   luosa-sandbox:main                     → sandbox-main-v<version>
#   luosa-sandbox:latest-cube              → sandbox-cube-v<version>
#   luosa-sandbox:main-cube                → sandbox-main-cube-v<version>
#   luosa-mcp:latest                       → mcp-v<version>
#   wechatopenai/weknora-docreader:latest  → docreader-v<version>（复用旧镜像，不重新 build）

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# ---------- 默认值 ----------
REGISTRY="${REGISTRY:-quay.io}"
NAMESPACE="${NAMESPACE:-jett_chen}"
REPO="${REPO:-luosa}"
VERSION="${VERSION:-v1.0}"
IMAGE_PREFIX="${IMAGE_PREFIX:-luosa-}"   # 与 build_local.sh 对齐
DRY_RUN=false
ONLY=""

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

# ---------- 参数解析 ----------
while [ $# -gt 0 ]; do
    case "$1" in
        --registry)   REGISTRY="$2"; shift ;;
        --namespace)  NAMESPACE="$2"; shift ;;
        --repo)       REPO="$2"; shift ;;
        --version)    VERSION="$2"; shift ;;
        --dry-run)    DRY_RUN=true ;;
        --only)       ONLY="$2"; shift ;;
        -h|--help)
            grep -E "^# " "$0" | sed 's/^# //'
            exit 0 ;;
        *) log_error "未知参数: $1"; exit 1 ;;
    esac
    shift
done

# ---------- 镜像清单 ----------
# 每行：<本地镜像> <远程服务 tag>
ALL_IMAGES=(
    "${IMAGE_PREFIX}app:latest                       app"
    "${IMAGE_PREFIX}frontend:latest                  frontend"
    "${IMAGE_PREFIX}sandbox:latest                   sandbox"
    "${IMAGE_PREFIX}sandbox:main                     sandbox-main"
    "${IMAGE_PREFIX}sandbox:latest-cube              sandbox-cube"
    "${IMAGE_PREFIX}sandbox:main-cube                sandbox-main-cube"
    "${IMAGE_PREFIX}mcp:latest                       mcp"
    "wechatopenai/weknora-docreader:latest          docreader"
)

# 过滤 --only
IMAGES=()
if [ -n "$ONLY" ]; then
    for entry in "${ALL_IMAGES[@]}"; do
        svc=$(echo "$entry" | awk '{print $2}')
        case "$svc" in
            "$ONLY"*)   IMAGES+=("$entry") ;;
        esac
    done
    if [ ${#IMAGES[@]} -eq 0 ]; then
        log_error "未找到匹配 --only=$ONLY 的镜像"
        exit 1
    fi
else
    IMAGES=("${ALL_IMAGES[@]}")
fi

# ---------- 预检 ----------
command -v docker >/dev/null || { log_error "docker 未安装"; exit 1; }
docker info >/dev/null 2>&1 || { log_error "docker 服务未运行"; exit 1; }

REMOTE_BASE="${REGISTRY}/${NAMESPACE}/${REPO}"
log_info "目标: ${REMOTE_BASE}:<service>-${VERSION}"
log_info "镜像数: ${#IMAGES[@]}"
[ "$DRY_RUN" = true ] && log_warn "DRY-RUN 模式：只 tag 不 push"

# ---------- 检查 docker login ----------
if [ "$DRY_RUN" = false ]; then
    # docker login 会保存到 ~/.docker/config.json；通过 inspect 一下目标仓库是否已认证
    if ! docker login --check "$REGISTRY" 2>/dev/null && [ $? -ne 0 ]; then
        # 不同 docker 版本行为不同；直接尝试一个简单 pull 看认证
        if ! docker pull "${REGISTRY}/${NAMESPACE}/${REPO}:nonexistent-probe" >/dev/null 2>&1; then
            rc=$?
            # pull 不存在的镜像会失败但**已通过认证**，因为错误是 manifest not found（HTTP 401/403/404）
            # 401/403 = 未认证；404 = 已认证但镜像不存在
            # 这里仅当认证明显失败时提示
            log_warn "无法确认 docker login 状态；继续往下推，若 401 再 docker login"
        fi
    fi
fi

# ---------- 逐个处理 ----------
PUSHED=()
SKIPPED=()
FAILED=()

for entry in "${IMAGES[@]}"; do
    local_image=$(echo "$entry" | awk '{print $1}')
    service=$(echo "$entry" | awk '{print $2}')
    remote_tag="${REMOTE_BASE}:${service}-${VERSION}"

    if ! docker image inspect "$local_image" >/dev/null 2>&1; then
        log_warn "本地镜像不存在: $local_image （跳过）"
        SKIPPED+=("$local_image")
        continue
    fi

    log_info "tag: $local_image  →  $remote_tag"
    if ! docker tag "$local_image" "$remote_tag"; then
        log_error "tag 失败: $local_image"
        FAILED+=("$local_image")
        continue
    fi

    if [ "$DRY_RUN" = true ]; then
        log_ok "DRY: 已 tag (未 push) $remote_tag"
        PUSHED+=("$remote_tag")
        continue
    fi

    log_info "push: $remote_tag"
    if docker push "$remote_tag" >/tmp/luosa-push-${service}.log 2>&1; then
        size=$(docker images --format '{{.Size}}' "$remote_tag" | head -1)
        log_ok "push 完成: $remote_tag  ($size)"
        PUSHED+=("$remote_tag")
    else
        log_error "push 失败: $remote_tag  (log: /tmp/luosa-push-${service}.log)"
        tail -10 "/tmp/luosa-push-${service}.log" | sed 's/^/  /'
        FAILED+=("$remote_tag")
    fi
done

# ---------- 汇总 ----------
echo
log_ok "=== push 完成 ==="
echo "成功 ${#PUSHED[@]} / 跳过 ${#SKIPPED[@]} / 失败 ${#FAILED[@]}"
if [ ${#PUSHED[@]} -gt 0 ]; then
    echo
    echo "已 push 到 ${REGISTRY}/${NAMESPACE}/${REPO}:"
    for tag in "${PUSHED[@]}"; do
        size=$(docker images --format '{{.Size}}' "$tag" | head -1)
        printf "  %-65s %s\n" "$tag" "$size"
    done
fi
if [ ${#FAILED[@]} -gt 0 ]; then
    echo
    log_error "失败的镜像："
    for tag in "${FAILED[@]}"; do
        echo "  - $tag"
    done
    exit 1
fi