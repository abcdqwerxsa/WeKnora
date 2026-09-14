#!/bin/bash
set -e

# ==============================================================================
# WeKnora 远程极速构建与热部署脚本
# 作用：将本地修改代码 rsync 增量同步到目标服务器，由服务器（32核+NVMe）
#       直接在隔离容器中构建并热更替换进生产容器，0 本地 CPU/内存消耗，0 重新打镜像。
# 用法：
#   ./scripts/remote-dev-deploy.sh          # 默认前后端全量
#   ./scripts/remote-dev-deploy.sh frontend # 仅构建部署前端（约 20s）
#   ./scripts/remote-dev-deploy.sh backend  # 仅构建部署后端（约 5s）
# ==============================================================================

REMOTE_HOST="${REMOTE_HOST:-root@192.168.20.226}"
REMOTE_SRC="/data/weknora-src"
TARGET="${1:-all}"

GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

echo -e "${BLUE}==> [1/4] 增量同步本地代码至 ${REMOTE_HOST}:${REMOTE_SRC} (零本地负载)...${NC}"
ssh "$REMOTE_HOST" "mkdir -p ${REMOTE_SRC} /data/build-cache/go /data/build-cache/gocache"
rsync -avz --delete \
  --exclude='node_modules' \
  --exclude='.git' \
  --exclude='dist' \
  --exclude='*.tar.gz' \
  --exclude='.gopath' \
  --exclude='WeKnora*' \
  ./ "${REMOTE_HOST}:${REMOTE_SRC}/"

if [ "$TARGET" = "all" ] || [ "$TARGET" = "backend" ]; then
  echo -e "${BLUE}==> [2/4] 在服务器 32 核容器中极速编译 Go 后端...${NC}"
  ssh "$REMOTE_HOST" "
    docker run --rm \
      -v /data/build-cache/go:/go \
      -v /data/build-cache/gocache:/root/.cache/go-build \
      -v ${REMOTE_SRC}:/app \
      -w /app \
      -e CGO_ENABLED=1 \
      -e GOOS=linux \
      -e GOARCH=amd64 \
      -e GOPROXY=https://goproxy.cn,direct \
      weknora-go-builder \
      go build -buildvcs=false -o /app/WeKnora ./cmd/server
    
    echo '==> 替换 WeKnora-app 容器二进制并重启...'
    docker cp ${REMOTE_SRC}/WeKnora WeKnora-app:/app/WeKnora
    docker cp ${REMOTE_SRC}/skills/preloaded/. WeKnora-app:/app/skills/preloaded/
    docker cp ${REMOTE_SRC}/skills/preloaded/. WeKnora-app:/app/skills/_builtin/
    docker restart WeKnora-app
  "
fi

if [ "$TARGET" = "all" ] || [ "$TARGET" = "frontend" ]; then
  echo -e "${BLUE}==> [3/4] 在服务器容器中构建前端产物 (保留持久 node_modules 缓存)...${NC}"
  ssh "$REMOTE_HOST" "
    docker run --rm \
      -v ${REMOTE_SRC}/frontend:/app \
      -w /app \
      -e NODE_OPTIONS='--max-old-space-size=4096' \
      node:20-alpine \
      sh -c 'VITE_IS_DOCKER=true npm run build'

    echo '==> 注入 WeKnora-frontend 容器 (保留运行时 config.js)...'
    docker exec WeKnora-frontend sh -c 'cp /usr/share/nginx/html/config.js /tmp/config.js.bak'
    docker exec WeKnora-frontend sh -c 'rm -rf /usr/share/nginx/html/assets'
    docker cp ${REMOTE_SRC}/frontend/dist/. WeKnora-frontend:/usr/share/nginx/html/
    docker exec WeKnora-frontend sh -c 'cp /tmp/config.js.bak /usr/share/nginx/html/config.js && rm /tmp/config.js.bak'
  "
fi

echo -e "${GREEN}==> [4/4] 部署完成！检查容器健康状态：${NC}"
ssh "$REMOTE_HOST" 'docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" | grep WeKnora'
echo -e "${GREEN}访问地址: http://192.168.20.226${NC}"
