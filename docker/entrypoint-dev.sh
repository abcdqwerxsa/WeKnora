#!/usr/bin/env bash
# LuoSA 通用开发镜像入口
#
# 通过 SERVICE_ROLE 决定启动什么:
#   frontend  → vite dev (端口 5173,HMR 自动启用)
#   app       → air     (Go 热重载,监听 :8080)
#   docreader → uvicorn (Python 文档解析,监听 :5005)
#   mcp-server→ mcp dev (按实际入口)
#
# 镜像特点:
#   - 单镜像多角色,容器之间共享 toolchain 层,服务器只存一份
#   - 改了 /workspace 里的代码自动 hot reload(前端 HMR / 后端 air)

set -e

ROLE="${SERVICE_ROLE:-app}"
WORKSPACE="${WORKSPACE_DIR:-/workspace}"

echo "[luosa-dev] entrypoint starting"
echo "[luosa-dev] SERVICE_ROLE=$ROLE  WORKSPACE=$WORKSPACE"

cd "$WORKSPACE"

case "$ROLE" in
  frontend)
    echo "[luosa-dev] starting vite dev server (HMR)"
    cd frontend
    if [ ! -d node_modules ]; then
      echo "[luosa-dev] installing frontend deps via pnpm"
      pnpm install --prefer-offline
    fi
    exec pnpm dev --host 0.0.0.0 --port 5173
    ;;

  app)
    echo "[luosa-dev] starting air (Go hot reload)"
    # air 配置由 compose volume 注入,放在 /workspace/.air.toml
    # build cmd 指 cmd/server,产物放 /tmp/server
    exec air -c .air.toml
    ;;

  docreader)
    echo "[luosa-dev] starting docreader (uvicorn --reload)"
    cd docreader
    if [ -f pyproject.toml ] && [ ! -d .venv ]; then
      echo "[luosa-dev] installing docreader deps"
      pip3 install --no-cache-dir --break-system-packages -e . || true
    fi
    exec uvicorn main:app --host 0.0.0.0 --port 5005 --reload
    ;;

  mcp-server)
    echo "[luosa-dev] starting mcp-server dev mode"
    cd mcp-server
    exec pnpm dev
    ;;

  *)
    echo "[luosa-dev] unknown SERVICE_ROLE='$ROLE' — falling back to bash"
    exec bash
    ;;
esac
