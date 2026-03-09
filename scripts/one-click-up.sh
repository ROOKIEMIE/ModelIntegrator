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

if [ ! -f .env ]; then
  cp resources/docker/compose.example.env .env
  echo "[INFO] 已生成 .env（来自 resources/docker/compose.example.env）"
fi

if grep -q "/opt/modelintegrator/resource/" .env || grep -q "\./resource/" .env; then
  sed -i 's|/opt/modelintegrator/resource/|/opt/modelintegrator/resources/|g' .env
  sed -i 's|\./resource/|./resources/|g' .env
  echo "[INFO] 已自动迁移 .env 中旧 resource 路径到 resources"
fi

mkdir -p resources/config resources/models resources/download-cache/hf resources/download-cache/aria2-config

touch resources/config/modelintegrator.db
chmod 666 resources/config/modelintegrator.db || true

if command -v nvidia-smi >/dev/null 2>&1; then
  echo "[INFO] 检测到 nvidia-smi，CUDA 平台信息如下："
  nvidia-smi --query-gpu=name,driver_version --format=csv,noheader || true
else
  echo "[WARN] 未检测到 nvidia-smi，将按非 CUDA 平台启动"
fi

PROFILE_ARGS=()
for arg in "$@"; do
  case "$arg" in
    --addons)
      PROFILE_ARGS+=(--profile addons)
      echo "[INFO] 将一并启动 addons"
      ;;
    --download)
      PROFILE_ARGS+=(--profile download)
      echo "[INFO] 将一并启动 download 容器"
      ;;
    --vllm)
      PROFILE_ARGS+=(--profile vllm)
      echo "[INFO] 将一并启动 vLLM 运行模板容器"
      ;;
    *)
      echo "[WARN] 忽略未知参数: $arg"
      ;;
  esac
done

echo "[INFO] 校验 compose 配置..."
docker compose "${PROFILE_ARGS[@]}" config >/dev/null

echo "[INFO] 启动服务..."
docker compose "${PROFILE_ARGS[@]}" up -d --build

echo "[INFO] 启动完成，状态如下："
docker compose "${PROFILE_ARGS[@]}" ps

echo "[INFO] 访问入口: http://localhost:59081/"
