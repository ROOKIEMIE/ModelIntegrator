# ModelIntegrator（MVP）

- 中文（当前）
- [English](./README.en.md)

ModelIntegrator 是一个本地多节点 LLM 控制平面，用于统一管理 Linux 服务器与局域网 Mac mini 上的模型运行时。

## 当前实现摘要

- 控制平面后端（Go）+ 统一 REST API + 静态 Web 控制台
- Web UI 双栏布局：左侧节点列表，右侧模型列表
- 节点列表与模型状态已联动：
  - 节点卡片显示 Runtime 数量、已装载模型数、Runtime 状态摘要
  - 模型标签页显示每个节点已装载模型数（`Main (N)` / `Sub1 (N)`）
- 模型列表支持按节点标签切换，默认显示第一个节点
- Docker Compose 编排（`nginx` 网关 + `model-integrator`，其余组件作为 `addons`）

- LM Studio 适配器：
  - 查询模型列表优先 `GET /api/v1/models`，兼容回退 `GET /v1/models`
  - 兼容解析 `models[] / data[] / 直接数组`，兼容 `key/display_name` 与 `id/name`
  - 支持从 `loaded_instances` 同步模型状态（`loaded/stopped`）
  - `load/unload` 优先调用 `POST /api/v1/models/load|unload`，失败时回退旧路径
  - `unload` 支持优先使用 `instance_id`（兼容新版本 LM Studio 要求）
  - `load/unload/start/stop` 前会做模型名校验与匹配
  - 可选内存缓存 + goroutine 定时刷新

- 模型刷新策略：
  - 服务启动后后台定时刷新
  - `GET /api/v1/models` 返回前会触发一次刷新

- 本地模型目录：
  - 扫描 `storage.model_root_dir` 并自动注册为本地模型（`source=local-scan`）

- 节点角色与连通性：
  - 首个节点自动归类为 `Main`，其后依次为 `Sub1/Sub2...`
  - `name` 为系统自动命名，建议将人类可读信息放在 `description`
  - 从节点在返回 `online` 前会先进行 ICMP 探测

- 节点硬件信息（当前聚焦 NVIDIA）：
  - 对“启用 docker runtime 的节点”填充平台信息
  - 优先本地 `nvidia-smi` 探测，失败时回退 Docker 探针探测
  - 其他或暂未支持硬件显示 `unknown`

- 模型动作互斥：
  - 前端有节点级动作锁
  - 后端也有节点级并发动作锁（避免绕过前端并发提交）

- 前端动作差异：
  - `backend_type=lmstudio` 的模型仅展示 `load/unload`
  - 其他后端保持 `load/unload/start/stop`

- Docker/Portainer 适配器目前仍是 placeholder
- 提供一键启动脚本

## 从 0 开始部署

目标目录：`/tank/docker_data/model_intergrator`

```bash
sudo mkdir -p /tank/docker_data/model_intergrator
sudo chown -R $USER:$USER /tank/docker_data/model_intergrator
rsync -a --delete /home/whoami/Dev/ModelIntegrator/ /tank/docker_data/model_intergrator/
cd /tank/docker_data/model_intergrator
```

```bash
cp resource/docker/compose.example.env .env
mkdir -p resource/config resource/models
touch resource/config/modelintegrator.db
chmod 666 resource/config/modelintegrator.db
```

```bash
# 启动核心（推荐）
./scripts/one-click-up.sh

# 启动核心 + addons
./scripts/one-click-up.sh --addons
```

验证：

```bash
curl -sS http://127.0.0.1:59081/healthz
curl -sS http://127.0.0.1:59081/api/v1/models
curl -sS http://127.0.0.1:59081/api/v1/nodes
```

停止：

```bash
./scripts/one-click-down.sh
```

## 关键项目文件

- 编排：`docker-compose.yml`
- 构建：`Dockerfile`
- 控制平面配置：`resource/config/config.example.yaml`
- Compose 环境变量：`resource/docker/compose.example.env`
- 网关配置：`resource/nginx/nginx.example.conf`
- 前端：`resource/web/index.html` / `app.css` / `app.js`
- 一键脚本：`scripts/one-click-up.sh` / `scripts/one-click-down.sh`
- 变更日志：`doc/LOG.md`

## 关键配置项

- `MCP_EXTERNAL_PORT`：外部网关端口（默认 `59081`）
- `MCP_SQLITE_PATH`：SQLite 文件路径
- `MCP_MODEL_DIR_HOST`：宿主机模型目录（默认 `./resource/models`）
- `MCP_MODEL_ROOT_DIR`：容器内模型目录（默认 `/opt/modelintegrator/models`）
- `MCP_LMSTUDIO_ENDPOINT`：LM Studio 地址
- `MCP_LMSTUDIO_CACHE_ENABLED`：是否启用 LM Studio 模型缓存
- `MCP_LMSTUDIO_CACHE_REFRESH_SECONDS`：缓存刷新间隔秒数
- `MCP_DOCKER_ENDPOINT`：Docker endpoint（用于 docker runtime 与 GPU 探测回退）
- `MCP_GPU_PROBE_IMAGE`：Docker GPU 探针镜像（可选，默认 `nvidia/cuda:12.4.1-base-ubuntu22.04`）

## nodes 配置说明（重要）

- `nodes` 中的 `id` 不再要求手动维护，系统会按顺序自动生成。
- 第 1 个节点自动为主节点：`Main`（`id=node-main`）。
- 第 2..N 个节点自动为从节点：`Sub1/Sub2...`（`id=node-sub-1/node-sub-2...`）。
- 建议在配置中使用 `description` 描述节点含义。

## LM Studio 兼容说明（重要）

- 模型列表：优先请求 `GET /api/v1/models`，失败回退 `GET /v1/models`。
- 模型加载：优先请求 `POST /api/v1/models/load`。
- 模型卸载：优先请求 `POST /api/v1/models/unload`，并优先使用 `instance_id`。
- 若对端版本只支持旧路径，会自动回退到 `/v1/models/*`。

## API

- `GET /healthz`
- `GET /api/v1/version`
- `GET /api/v1/nodes`
- `GET /api/v1/models`
- `GET /api/v1/models/{id}`
- `POST /api/v1/models/{id}/load`
- `POST /api/v1/models/{id}/unload`
- `POST /api/v1/models/{id}/start`
- `POST /api/v1/models/{id}/stop`
