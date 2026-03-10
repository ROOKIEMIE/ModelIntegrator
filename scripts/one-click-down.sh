#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if ! command -v docker >/dev/null 2>&1; then
  echo "[ERROR] docker 未安装或不在 PATH"
  exit 1
fi

if ! docker compose version >/dev/null 2>&1; then
  echo "[ERROR] docker compose 不可用"
  exit 1
fi

echo "[INFO] 检查并清理 controller 管理的模型容器..."
mapfile -t MODEL_CONTAINERS < <(
  {
    docker ps -aq --filter label=com.controller.managed=true
    docker ps -aq --filter label=com.modelintegrator.managed=true
  } | awk 'NF' | sort -u
)
if [ "${#MODEL_CONTAINERS[@]}" -gt 0 ]; then
  docker rm -f "${MODEL_CONTAINERS[@]}" >/dev/null
  echo "[INFO] 已清理 ${#MODEL_CONTAINERS[@]} 个模型容器"
else
  echo "[INFO] 未发现需清理的模型容器"
fi

echo "[INFO] 停止控制平面服务..."
docker compose down
