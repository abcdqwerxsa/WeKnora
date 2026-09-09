#!/usr/bin/env bash
# docreader 独立开发镜像入口
# 唯一职责:启动 uvicorn --reload 监听 docreader/ 下 .py 变化

set -e
export PYTHONPATH="/workspace:${PYTHONPATH:-}"

echo "[docreader-dev] starting uvicorn with --reload"
cd /workspace/docreader
exec uvicorn docreader.main:app --host 0.0.0.0 --port 5005 --reload
