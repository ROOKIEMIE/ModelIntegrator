# ModelIntegrator（MVP）

- 中文（当前）
- [English](./README.en.md)

ModelIntegrator 是一个本地多节点 LLM 控制平面，用于统一管理 Linux 服务器与局域网 Mac mini 上的模型运行时。

## 项目定位

ModelIntegrator 的定位是“本地多节点 LLM 控制平面”，核心目标是统一管理：

- 主节点上的 Docker 化模型服务
- 从节点上的 LM Studio / Ollama / vLLM 等运行时
- 需要深度纳管的节点（通过 agent）
- 模型注册、节点注册、运行时注册、状态展示、调度与操作入口

当前以 Linux Server + Docker Compose 作为主部署形态，后续向 `controller + agent + external runtimes` 稳定演进。

## 当前系统形态（2026-03）

- 控制面仍由单一二进制 `src/cmd/model-integrator` 承担主要职责
- 已具备 Web 管理界面、REST API、节点/模型管理、运行时模板、部分调度与动作互斥能力
- 已接入 LM Studio 与 Docker/Portainer 适配器
- 可通过 Compose profile 扩展下载容器与 vLLM 模板容器

## 目标系统形态（已定稿）

系统按三层结构演进：

1. 中央控制器（controller）
- 部署在主节点
- 负责 Web UI、REST API、注册表、调度、审计、统一认证
- 继续以 Docker Compose 为核心部署方式

2. 节点 agent（可选）
- 只部署在需要深度纳管的节点
- 负责本机资源快照、本地目录扫描、本机 Docker 管理、本机下载、运行时桥接、可选 fit 分析
- 不是第二个控制台，而是轻量本地执行单元

3. 外部运行时
- LM Studio / Ollama / vLLM / OpenAI-compatible 服务
- 按能力分级接入，不强行统一成同深度控制模式

## 节点分类（已定稿）

系统统一采用四类节点：

- `Central Node`：主控制节点，运行 controller，可同时承载本地 runtime
- `Managed Node`：安装 agent，通过 agent 暴露增强能力
- `Runtime-only Node`：未安装 agent，仅暴露运行时 API
- `Offline / Catalog Node`：仅登记模型/模板/元信息，不直接承担运行

## 节点能力分级（已定稿）

系统明确采用分级能力模型，而不是统一资源真相：

- `runtime_managed`
- 典型：LM Studio、Ollama
- 以运行时反馈和动作结果为主（如 load 成功/失败）

- `service_observed`
- 典型：vLLM 等服务
- 主要获得在线与指标观测，不保证完整生命周期控制

- `agent_managed`
- 安装 agent 的节点
- 可提供资源快照、Docker 管理、目录扫描、下载、fit 分析等增强能力

## 混合节点管理（已定稿）

- 节点是宿主，runtime 才是能力单元
- 一个节点可同时挂载多个 runtime（Docker / LM Studio / Ollama / vLLM / fit provider）
- 调度与操作目标应尽量落在“节点上的某个 runtime”，而不是只落在节点抽象

## 前端展示策略（已定稿）

前端将从“单层节点列表”演进为“节点 + runtime + capability”模型：

- 节点卡片：名称、分类、在线状态、agent 状态、能力级别、最后心跳、能力来源
- 节点内 runtime 列表：类型、endpoint、在线状态、能力集（list/load/unload/pull/metrics/docker-manage/download/fit）、当前可执行动作
- 资源与能力展示必须体现分级，不伪装成所有节点同等可控

## Agent 部署形态（已定稿）

agent 必须双形态部署：

- 原生二进制（优先）：适配 macOS / Windows / Linux，适合无容器环境节点
- 容器版（可选）：适配 Linux + Docker/Podman，便于本机容器能力接入

agent 不应被设计为必须依赖 Docker 才能运行。

## llmfit 集成方案（已定稿）

llmfit 在本项目中的定位是 agent 的可选本地能力模块，不做源码级嵌入：

- 主路径：agent 托管 llmfit 二进制并优先启动 `llmfit serve`
- 通信方式：agent 通过 loopback HTTP 调用 llmfit
- 生命周期：agent 启停时联动管理 llmfit 子进程
- 兜底路径：必要时保留 CLI fallback

llmfit 主要用于 fit 分析、system profile、模型推荐与可运行性评估。

## 建议目录结构与演进方向

本轮已按“温和演进”落位双二进制与分层占位，目标结构如下：

```text
src/cmd/
  model-integrator/   # 当前主入口（兼容保留）
  controller/         # 新增占位：未来中央控制器入口
  agent/              # 新增占位：未来节点 agent 入口

src/pkg/
  config/
  model/
  registry/
  runtime/            # 新增占位
  adapter/
  agent/              # 新增占位
  controller/         # 新增占位
  fit/                # 新增占位（llmfit 集成边界）
  capability/         # 新增占位（能力分级模型）
  scheduler/
  telemetry/          # 新增占位
```

过渡策略：

- 现有功能继续由 `src/cmd/model-integrator` 承载，不进行大爆炸重构
- 新增目录先用于边界声明与模块归位，后续按功能逐步迁移
- 节点 `id` 语义继续保持现状，不在本轮推翻

## 当前已实现与后续规划

已实现（本轮前）：

- 控制平面后端（Go）+ 统一 REST API + 静态 Web 控制台
- Runtime/Template 页签与运行时模板校验注册机制
- LM Studio 与 Docker/Portainer 适配器接入
- 节点与模型联动展示、模型动作并发保护、基础健康与状态链路

后续规划（下一阶段优先）：

- 将 controller 主入口从 `model-integrator` 渐进迁移到 `src/cmd/controller`
- 开始实现 agent 与 controller 的注册/心跳/能力上报基础协议
- 将节点与 runtime 的能力分级模型写入 API 与前端数据结构
- 在 agent 内落位 llmfit managed serve（含本地 HTTP 调用与进程托管）

## 当前实现摘要（已实现能力）

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
- Docker Compose 编排（`nginx` 网关 + `model-integrator`，其余组件作为 `addons` / `download` / `vllm` profile）

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

目标目录：`/tank/docker_data/model_intergrator`

```bash
sudo mkdir -p /tank/docker_data/model_intergrator
sudo chown -R $USER:$USER /tank/docker_data/model_intergrator
rsync -a --delete /home/whoami/Dev/ModelIntegrator/ /tank/docker_data/model_intergrator/
cd /tank/docker_data/model_intergrator
```

```bash
cp resources/docker/compose.example.env .env
mkdir -p resources/config resources/models
touch resources/config/modelintegrator.db
chmod 666 resources/config/modelintegrator.db
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
- 架构决策与历史变更日志：`doc/LOG.md`

## 关键配置项

- `MCP_EXTERNAL_PORT`：外部网关端口（默认 `59081`）
- `MCP_SQLITE_PATH`：SQLite 文件路径
- `MCP_MODEL_DIR_HOST`：宿主机模型目录（默认 `./resources/models`）
- `MCP_MODEL_ROOT_DIR`：容器内模型目录（默认 `/opt/modelintegrator/models`）
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

## nodes 配置说明（重要）

- `nodes` 中的 `id` 不再要求手动维护，系统会按顺序自动生成。
- 第 1 个节点自动为主节点：`Main`（`id=node-main`）。
- 第 2..N 个节点自动为从节点：`Sub1/Sub2...`（`id=node-sub-1/node-sub-2...`）。
- 建议在配置中使用 `description` 描述节点含义。
- 说明：`Main/Sub` 是当前配置生成与 UI 展示别名；架构分层上的正式分类以 `Central/Managed/Runtime-only/Offline` 为准。

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
- SQLite：下一阶段接入模型/节点状态持久化读写（当前仅路径预备）。

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
