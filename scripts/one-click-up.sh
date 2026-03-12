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

if grep -q "/opt/controller/resource/" .env \
  || grep -q "/opt/modelintegrator/resource/" .env \
  || grep -q "/opt/modelintegrator/resources/" .env \
  || grep -q "\./resource/" .env; then
  sed -i 's|/opt/controller/resource/|/opt/controller/resources/|g' .env
  sed -i 's|/opt/modelintegrator/resource/|/opt/controller/resources/|g' .env
  sed -i 's|/opt/modelintegrator/resources/|/opt/controller/resources/|g' .env
  sed -i 's|/opt/modelintegrator/models|/opt/controller/models|g' .env
  sed -i 's|\./resource/|./resources/|g' .env
  # 兼容历史默认 sqlite 文件名。
  sed -i 's|modelintegrator\.db|controller.db|g' .env
  echo "[INFO] 已自动迁移 .env 中旧路径到当前 controller/resources 目录"
fi

if grep -q "/opt/modelintegrator/models" .env; then
  sed -i 's|/opt/modelintegrator/models|/opt/controller/models|g' .env
  echo "[INFO] 已自动迁移 .env 中旧模型目录到 /opt/controller/models"
fi

if [ -S /var/run/docker.sock ]; then
  DOCKER_SOCK_GID=""
  if stat -c '%g' /var/run/docker.sock >/dev/null 2>&1; then
    DOCKER_SOCK_GID="$(stat -c '%g' /var/run/docker.sock)"
  elif stat -f '%g' /var/run/docker.sock >/dev/null 2>&1; then
    DOCKER_SOCK_GID="$(stat -f '%g' /var/run/docker.sock)"
  fi

  if [ -n "$DOCKER_SOCK_GID" ]; then
    if grep -q '^MCP_DOCKER_GID=' .env; then
      sed -i "s/^MCP_DOCKER_GID=.*/MCP_DOCKER_GID=${DOCKER_SOCK_GID}/" .env
    else
      echo "MCP_DOCKER_GID=${DOCKER_SOCK_GID}" >> .env
    fi
    export MCP_DOCKER_GID="$DOCKER_SOCK_GID"
    echo "[INFO] 已设置 MCP_DOCKER_GID=${DOCKER_SOCK_GID}（来自 /var/run/docker.sock）"
  else
    echo "[WARN] 无法识别 /var/run/docker.sock 的 GID，将使用 compose 默认值"
  fi
else
  echo "[WARN] 未找到 /var/run/docker.sock，跳过 MCP_DOCKER_GID 自动设置"
fi

mkdir -p resources/config resources/models resources/download-cache/hf resources/download-cache/aria2-config

touch resources/config/controller.db
chmod 666 resources/config/controller.db || true
# sqlite 需要在目录内创建 journal/wal，目录本身也需要可写权限。
chmod 777 resources/config || true

TEST_LOG_HOST_DIR="$(grep -E '^MCP_TEST_LOG_ROOT_HOST=' .env | tail -n1 | cut -d'=' -f2- || true)"
if [ -z "${TEST_LOG_HOST_DIR}" ]; then
  TEST_LOG_HOST_DIR="./testsystem/logs"
fi
if ! mkdir -p "${TEST_LOG_HOST_DIR}"; then
  echo "[ERROR] 无法创建测试日志目录: ${TEST_LOG_HOST_DIR}"
  echo "[ERROR] 请检查 MCP_TEST_LOG_ROOT_HOST 是否指向当前用户可写路径"
  exit 1
fi
# 测试运行由容器内 app 用户写入，宿主目录需对其他用户可写。
if ! chmod 777 "${TEST_LOG_HOST_DIR}"; then
  echo "[WARN] 无法修改测试日志目录权限为 777: ${TEST_LOG_HOST_DIR}"
fi
TEST_LOG_PROBE="${TEST_LOG_HOST_DIR}/.mcp-write-check.$$"
if ! touch "${TEST_LOG_PROBE}" 2>/dev/null; then
  echo "[ERROR] 测试日志目录不可写: ${TEST_LOG_HOST_DIR}"
  echo "[ERROR] 请确保宿主机目录对容器内 app 用户可写，或改用可写目录作为 MCP_TEST_LOG_ROOT_HOST"
  exit 1
fi
rm -f "${TEST_LOG_PROBE}" || true

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
    --local-agent)
      PROFILE_ARGS+=(--profile local-agent)
      echo "[INFO] 将一并启动 controller 节点本机 agent（local-agent profile）"
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
