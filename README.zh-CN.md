# Local LLM Control Plane（Controller）

- 中文（当前）
- [English](./README.en.md)

Local LLM Control Plane 是一个本地多节点 LLM 控制平面，用于统一管理 Linux 服务器与局域网 Mac mini 上的模型运行时。

## 当前实现摘要

- 控制平面后端（Go）+ 统一 REST API + 静态 Web 控制台
- Web UI 顶层双页签：`Runtime` / `Download`
  - `Runtime` 页签：包含子页签 `List` / `Template`
    - `List`：左侧节点列表 + 右侧模型列表
    - `Template`：Runtime Template 列表与自定义模板校验/注册
  - `Download` 页签：当前空白预留页
- 节点列表与模型状态已联动：
  - 节点卡片显示 Runtime 数量、已装载模型数、Runtime 状态摘要
  - 模型标签页显示每个节点已装载模型数（`Main (N)` / `Sub1 (N)`）
- 模型列表支持按节点标签切换，默认显示第一个节点
- Docker Compose 编排（`nginx` 网关 + `controller`，其余组件作为 `addons` / `download` / `vllm` profile）

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
- 运行时模板扩展：
  - 内置模板：`docker-generic`、`vllm-openai`
  - 支持用户提交自定义模板并进行后端校验与注册（Runtime -> Template）
  - Docker/Portainer 模型动作前会校验模板绑定是否合法

- Docker/Portainer 适配器已接入真实容器编排调用（Docker Engine API / Portainer Docker Proxy）
- 下载容器支持（`download` profile）：
  - `hf-downloader`
  - `aria2-downloader`
- vLLM 运行模板容器（`vllm` profile）：
  - `vllm-runtime`（NVIDIA GPU，未来用于模型实例化）
- 下载功能当前仅面向“主节点且具备 docker 能力”的部署场景
- 提供一键启动脚本

## 从 0 开始部署

目标目录：`/tank/docker_data/model_control_plane`

```bash
sudo mkdir -p /tank/docker_data/model_control_plane
sudo chown -R $USER:$USER /tank/docker_data/model_control_plane
rsync -a --delete /home/whoami/Dev/model-control-plane/ /tank/docker_data/model_control_plane/
cd /tank/docker_data/model_control_plane
```

```bash
cp resources/docker/compose.example.env .env
mkdir -p resources/config resources/models
touch resources/config/controller.db
chmod 666 resources/config/controller.db
```

```bash
# 启动核心（推荐）
./scripts/one-click-up.sh

# 启动核心 + addons
./scripts/one-click-up.sh --addons

# 启动核心 + 下载容器
./scripts/one-click-up.sh --download

# 启动核心 + addons + 下载容器
./scripts/one-click-up.sh --addons --download

# 启动核心 + vLLM 模板容器
./scripts/one-click-up.sh --vllm

# 启动核心 + addons + 下载容器 + vLLM 模板容器
./scripts/one-click-up.sh --addons --download --vllm
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
- 控制平面配置：`resources/config/config.example.yaml`
- Compose 环境变量：`resources/docker/compose.example.env`
- 网关配置：`resources/nginx/nginx.example.conf`
- 前端：`resources/web/index.html` / `app.css` / `app.js`
- 一键脚本：`scripts/one-click-up.sh` / `scripts/one-click-down.sh`
- 变更日志：`doc/LOG.md`

## 关键配置项

- `MCP_EXTERNAL_PORT`：外部网关端口（默认 `59081`）
- `MCP_SQLITE_PATH`：SQLite 文件路径
- `MCP_MODEL_DIR_HOST`：宿主机模型目录（默认 `./resources/models`）
- `MCP_MODEL_ROOT_DIR`：容器内模型目录（默认 `/opt/controller/models`）
- `MCP_LMSTUDIO_ENDPOINT`：LM Studio 地址
- `MCP_LMSTUDIO_CACHE_ENABLED`：是否启用 LM Studio 模型缓存
- `MCP_LMSTUDIO_CACHE_REFRESH_SECONDS`：缓存刷新间隔秒数
- `MCP_DOCKER_ENDPOINT`：Docker endpoint（用于 docker runtime 与 GPU 探测回退）
- `MCP_GPU_PROBE_IMAGE`：Docker GPU 探针镜像（可选，默认 `nvidia/cuda:12.4.1-base-ubuntu22.04`）
- `MCP_HF_CACHE_DIR`：HF 下载缓存目录（download profile）
- `MCP_ARIA2_RPC_SECRET`：aria2 RPC 密钥（download profile）
- `MCP_ARIA2_RPC_PORT`：aria2 RPC 端口（download profile）
- `MCP_ARIA2_LISTEN_PORT`：aria2 下载监听端口（download profile）
- `MCP_VLLM_EXTERNAL_PORT`：vLLM 对外端口（vllm profile）
- `MCP_VLLM_MODEL`：vLLM 默认模型（支持 Hugging Face repo id 或本地目录）
- `MCP_VLLM_SERVED_MODEL_NAME`：vLLM 对外服务模型名
- `MCP_VLLM_GPU_MEMORY_UTILIZATION`：vLLM GPU 显存占用上限
- `MCP_VLLM_MAX_MODEL_LEN`：vLLM 最大上下文长度
- `HUGGING_FACE_HUB_TOKEN`：访问私有/gated 模型用 token（可选）

### SQLite 持久化说明（控制平面状态）

- 控制平面会将关键状态持久化到 `MCP_SQLITE_PATH` 指定的 SQLite 文件：
  - agent 注册信息、最近心跳、能力上报结果
  - node 基础信息与最近聚合状态（含分类/能力分级/能力来源/agent 状态）
  - model 基础注册信息与最近状态
  - runtime 最小关联状态（状态/能力/动作/最近探测时间）
- 推荐将 `resources/config/controller.db` 挂载到持久卷，避免容器重建后丢失状态。
- controller 重启后会从 SQLite 回填关键状态，并继续以“内存缓存 + SQLite 持久化”模式运行。

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

## Download 功能说明（当前阶段）

- Download 页面当前为空白预留页（后续用于模型下载任务编排 UI）。
- 容器下载能力通过 compose `download` profile 启用。
- `hf-downloader` 可直接下载 vLLM 所需的 Hugging Face 模型权重（如 safetensors）。
- `aria2-downloader` 用于直链文件下载（如模型分片或镜像文件）。
- 该能力当前仅在“主节点且存在 docker runtime”的部署场景下支持。
- 若后续扩展到非 docker 平台，将按平台能力单独设计下载路径与适配器。

## vLLM 容器说明（新增）

- compose 提供 `vllm` profile 下的 `vllm-runtime` 模板容器。
- 仅适用于“主节点 + Docker + NVIDIA Container Toolkit”场景。
- 默认会将模型下载/读取目录指向 `./resources/models`，并复用 HF cache。

## 运行时模板扩展（新增）

- 配置文件可直接提供模板：`runtime_templates: []`。
- 后端会先校验模板，再允许注册为可用模板。
- 本地扫描的 docker 模型默认绑定模板：`docker-generic`（可在模型 metadata 中覆盖 `runtime_template_id`）。
- 控制台入口：`Runtime` 页签下 `Template` 子页签。

接口：

- `GET /api/v1/runtime-templates`：列出所有模板（builtin/config/custom）。
- `POST /api/v1/runtime-templates/validate`：校验模板定义。
- `POST /api/v1/runtime-templates`：注册模板（校验通过后写入内存注册表）。

示例请求：

```json
{
  "id": "vllm-qwen-7b",
  "name": "vLLM Qwen 7B",
  "runtime_type": "docker",
  "image": "vllm/vllm-openai:latest",
  "command": ["--host", "0.0.0.0", "--port", "8000", "--model", "Qwen/Qwen2.5-7B-Instruct"],
  "volumes": ["./resources/models:/models", "./resources/download-cache/hf:/data/hf-cache"],
  "ports": ["58000:8000"],
  "env": {"HF_HOME": "/data/hf-cache"},
  "needs_gpu": true
}
```

## 路线说明（已确认）

- Docker/Portainer 适配器：先聚焦 NVIDIA GPU 场景，后续扩展其他硬件。
- SQLite：已接入 agent/node/model/runtime 关键状态持久化，后续将补充迁移工具与动作审计表。

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
- `GET /api/v1/runtime-templates`
- `POST /api/v1/runtime-templates/validate`
- `POST /api/v1/runtime-templates`
