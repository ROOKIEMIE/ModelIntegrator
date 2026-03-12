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

echo "[INFO] 停止控制平面服务（包含 local-agent/addons/download/vllm profile）..."
docker compose --profile local-agent --profile addons --profile download --profile vllm down --remove-orphans || true
docker compose down --remove-orphans || true

echo "[INFO] 检查并回收遗留 local-agent 容器..."
if docker ps -aq --filter name=^controller-local-agent$ | grep -q .; then
  docker rm -f controller-local-agent >/dev/null || true
  echo "[INFO] 已回收 controller-local-agent"
else
  echo "[INFO] 未发现遗留 local-agent 容器"
fi

echo "[INFO] 检查并清理 controller 管理的网络资源..."
mapfile -t MANAGED_NETWORKS < <(
  {
    docker network ls -q --filter label=com.controller.managed=true
    docker network ls -q --filter label=com.modelintegrator.managed=true
    docker network ls -q --filter name=^controller_mcp_net$
  } | awk 'NF' | sort -u
)
if [ "${#MANAGED_NETWORKS[@]}" -gt 0 ]; then
  for net_id in "${MANAGED_NETWORKS[@]}"; do
    if docker network rm "$net_id" >/dev/null 2>&1; then
      echo "[INFO] 已清理网络: $net_id"
    else
      echo "[WARN] 网络暂不可清理（可能仍在使用）: $net_id"
    fi
  done
else
  echo "[INFO] 未发现需清理的网络资源"
fi
