#!/usr/bin/env bash
# scripts/save-official.sh — 打包"客户机拉不到"的 Docker Hub 公共镜像到 tar
#
# 用途：客户机访问不了 docker.io（防火墙），但开发机有镜像。把开发机的镜像
#      save 成 tar，scp 给客户 docker load。
#
# 打包内容：
#   - minio/minio:RELEASE.2025-09-07T16-13-09Z    (minio + langfuse-minio 共用)
#   - langfuse/langfuse:3                          (langfuse-web)
#   - langfuse/langfuse-worker:3
#   - clickhouse/clickhouse-server:24.8            (langfuse-clickhouse)
#
# 不打包（拉得到 / 不需要）：
#   - paradedb/paradedb, redis, qdrant, neo4j, searxng  （daocloud 有）
#   - dex  （daocloud 没有，本机也没有，跳过）

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

LUOSA_VERSION="${LUOSA_VERSION:-v1.0}"
OUTPUT="${OUTPUT:-/tmp/luosa-official-images-${LUOSA_VERSION}.tar}"

# 镜像清单：local-source → target-name-for-docreader-compose
# 客户机 docker load 后，tag 跟原镜像一致（不用重新 tag）
IMAGES=(
    "minio/minio:RELEASE.2025-09-07T16-13-09Z"
    "langfuse/langfuse:3"
    "langfuse/langfuse-worker:3"
    "clickhouse/clickhouse-server:24.8"
)

if [ -t 1 ]; then
    GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
else
    GREEN=''; YELLOW=''; BLUE=''; NC=''
fi
log_info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_ok()    { echo -e "${GREEN}[OK  ]${NC}  $*"; }

# 检查本地是否有这些镜像
missing=()
for img in "${IMAGES[@]}"; do
    if docker image inspect "$img" >/dev/null 2>&1; then
        log_ok "本地有: $img"
    else
        log_warn "本地缺: $img"
        missing+=("$img")
    fi
done

if [ ${#missing[@]} -gt 0 ]; then
    log_warn "缺一些镜像："
    for img in "${missing[@]}"; do
        echo "  - $img"
    done
    log_warn "先 docker pull 这些镜像，再跑本脚本"
    exit 1
fi

# 打包
log_info "打包镜像到 $OUTPUT ..."
docker save -o "$OUTPUT" "${IMAGES[@]}"

size=$(du -h "$OUTPUT" | awk '{print $1}')
log_ok "打包完成: $OUTPUT ($size)"

echo
echo "=== 使用方法 ==="
echo "# 1. 上传到客户机"
echo "scp $OUTPUT user@server:/tmp/"
echo
echo "# 2. 在客户机 docker load"
echo "ssh user@server 'docker load -i /tmp/${OUTPUT##*/}'"
echo
echo "# 3. 客户机跑 deploy.sh（不再需要从公网拉这些镜像）"
echo "ssh user@server 'cd /opt/luosa/luosa-deploy-${LUOSA_VERSION}-staging && bash scripts/deploy.sh'"