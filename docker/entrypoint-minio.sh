#!/bin/sh
# MinIO 容器启动 wrapper:
# 1) 后台启动 minio server
# 2) 等 /minio/health/live 200
# 3) 用内置 mc 幂等创建业务桶
# 4) 把 SIGTERM 转发到 minio 子进程,前台 wait 让容器生命周期跟 minio 一致
set -e

BUCKET="${MINIO_BUCKET_NAME:-luosa-files}"
USER="${MINIO_ROOT_USER:-minioadmin}"
PASS="${MINIO_ROOT_PASSWORD:-devminio}"

# 把容器停止信号转发给后台的 minio
trap 'echo "[minio-init] received signal, stopping minio"; kill -TERM "$MINIO_PID" 2>/dev/null || true' TERM INT

echo "[minio-init] starting minio server in background"
minio server /data --console-address ":9001" &
MINIO_PID=$!

# 等 minio 健康
ready=""
for i in $(seq 1 30); do
  if curl -sf "http://localhost:9000/minio/health/live" -o /dev/null 2>&1; then
    ready=yes
    break
  fi
  sleep 1
done

if [ -z "$ready" ]; then
  echo "[minio-init] minio did not become healthy in 30s, aborting"
  kill -TERM "$MINIO_PID" 2>/dev/null || true
  exit 1
fi

echo "[minio-init] bootstrapping bucket: ${BUCKET}"
mc alias set local http://localhost:9000 "$USER" "$PASS"
mc mb --ignore-existing "local/${BUCKET}"

echo "[minio-init] bucket ready, waiting for minio (PID $MINIO_PID)"
# 容器 PID 1 = 本 shell; 后台 minio 是其直接子进程. wait 让容器生命周期绑到 minio 上.
wait "$MINIO_PID"
