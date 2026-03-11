# Local LLM Control Plane（Controller）

- 中文（当前）
- [English](./README.en.md)

Local LLM Control Plane 是一个本地多节点 LLM 控制平面，用于统一管理 Linux 服务器与局域网 Mac mini 上的模型运行时。

## 项目定位

Local LLM Control Plane 的定位是“本地多节点 LLM 控制平面”，核心目标是统一管理：

- 主节点上的 Docker 化模型服务
- 从节点上的 LM Studio / Ollama / vLLM 等运行时
- 需要深度纳管的节点（通过 agent）
- 模型注册、节点注册、运行时注册、状态展示、调度与操作入口

当前以 Linux Server + Docker Compose 作为主部署形态，后续向 `controller + agent + external runtimes` 稳定演进。

## 当前系统形态（2026-03）

- 控制面主入口已迁移为 `src/cmd/controller`
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
  controller/         # 中央控制器入口
  agent/              # 节点 agent 入口

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

- 当前功能已由 `src/cmd/controller` 承载，保持增量迁移
- `pkg` 新增目录先用于边界声明与模块归位，后续按功能逐步迁移
- 节点 `id` 语义继续保持现状，不在本轮推翻

## 当前已实现与后续规划

已实现（本轮前）：

- 控制平面后端（Go）+ 统一 REST API + 静态 Web 控制台
- Runtime/Template 页签与运行时模板校验注册机制
- LM Studio 与 Docker/Portainer 适配器接入
- 节点与模型联动展示、模型动作并发保护、基础健康与状态链路

后续规划（下一阶段优先）：

- 已完成 controller 主入口迁移（`src/cmd/controller`）
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

## P0 新增（本次落地）

### 1. E5 embedding 样板（TEI）打通

- 增加了 `local-multilingual-e5-base` 样板配置（`resources/config/config.example.yaml`）。
- 默认绑定模板 `tei-embedding-e5-local`（TEI CPU 模板，映射端口 `58001:80`）。
- controller 可通过任务 API 启动/停止/刷新样板 runtime。
- 增加最小 embedding client：`scripts/e5_embedding_client.sh`。
- 增加场景脚本：`testsystem/scenarios/e5_embedding_smoke.sh`，可校验返回结构与向量维度。

### 2. runtime 动作任务化

- 新增任务模型（`Task`）与 SQLite 持久化表 `tasks`。
- 新增任务 API：
  - `POST /api/v1/tasks/runtime/start`
  - `POST /api/v1/tasks/runtime/stop`
  - `POST /api/v1/tasks/runtime/refresh`
  - `GET /api/v1/tasks`
  - `GET /api/v1/tasks/{id}`
- 任务状态支持：`pending/dispatched/running/success/failed/timeout/canceled`。
- 前端 `start/stop/refresh` 已改为走任务 API，可在页面查看任务执行进度与错误。

### 3. desired / observed / readiness

- 模型状态新增字段：
  - `desired_state`
  - `observed_state`
  - `readiness`
  - `health_message`
  - `last_reconciled_at`
- controller 在 `start/stop` 触发时先写 `desired_state`。
- 刷新或动作后执行 reconcile：读取容器状态并更新 observed/readiness。
- 对 TEI E5 样板增加 `/health` 探测，可识别“容器已运行但服务未 ready”。
- 当前端可直接看到 `desired/observed/readiness`。

### 4. agent 最小任务执行面

- controller <-> agent 新增最小任务协议字段（task id/type/payload/status/time/message/detail）。
- controller 新增 agent 任务接口：
  - `GET /api/v1/agents/{id}/tasks/next`
  - `POST /api/v1/agents/{id}/tasks/{taskID}/report`
  - `POST /api/v1/tasks/agent/runtime-readiness`
- agent 新增任务轮询执行循环（默认 `AGENT_TASK_POLL_SECONDS=5`）。
- 已实现真实任务类型：`agent.runtime_readiness_check`（端口连通 + HTTP health 检查）。
- 任务结果会回写 SQLite，并更新对应模型 readiness 信息。

### 5. 独立测试工具链（testsystem）

- 新增独立目录 `testsystem/`：
  - `Dockerfile`
  - `docker-compose.test.yml`
  - `scenarios/e5_embedding_smoke.sh`
  - `scripts/run_test.sh`
  - `scripts/collect_logs.sh`
  - `logs/`
  - `README.md`
- 主系统与测试系统隔离：主系统继续使用根目录 `docker-compose.yml`，测试系统使用 `testsystem/docker-compose.test.yml`。
- 测试日志按 run-id 落到挂载目录（默认 `./testsystem/logs/<run-id>/`）。

### 6. controller 测试运行能力 + 前端一键测试

- 新增 `test_runs` 持久化表与 `TestRunService`。
- 新增测试运行 API：
  - `POST /api/v1/test-runs`（仅允许预定义场景 `e5_embedding_smoke`）
  - `GET /api/v1/test-runs`
  - `GET /api/v1/test-runs/{id}`
- 前端新增“一键测试 E5”按钮：调用 `POST /api/v1/test-runs`，并展示最近测试记录与日志路径。
- 前端不会执行 shell，仅调用后端 API。

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
mkdir -p testsystem/logs
touch resources/config/controller.db
chmod 777 resources/config testsystem/logs
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
- 架构决策与历史变更日志：`doc/LOG.md`

## 关键配置项

- `MCP_EXTERNAL_PORT`：外部网关端口（默认 `59081`）
- `MCP_SQLITE_PATH`：SQLite 文件路径
- `MCP_MODEL_DIR_HOST`：宿主机模型目录（默认 `./resources/models`）
- `MCP_MODEL_ROOT_DIR`：容器内模型目录（默认 `/opt/controller/models`）
- `MCP_TEST_LOG_ROOT_HOST`：宿主机测试日志目录（默认 `./testsystem/logs`）
- `MCP_TEST_LOG_ROOT_DIR`：controller 容器内测试日志目录（默认 `/opt/controller/test-logs`）
- `MCP_LMSTUDIO_ENDPOINT`：LM Studio 地址
- `MCP_LMSTUDIO_CACHE_ENABLED`：是否启用 LM Studio 模型缓存
- `MCP_LMSTUDIO_CACHE_REFRESH_SECONDS`：缓存刷新间隔秒数
- `MCP_DOCKER_ENDPOINT`：Docker endpoint（用于 docker runtime 与 GPU 探测回退）
- `MCP_CONTAINER_HOST_ALIAS`：controller 容器访问宿主机端口的别名（默认 `host.docker.internal`）
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

## 主 compose 与测试 compose 的区别

- 主系统：`docker-compose.yml`
  - 负责 controller/nginx 及 addons/download/vllm 等运行组件。
  - 生产运行态、控制台、API 都在这里。
- 测试系统：`testsystem/docker-compose.test.yml`
  - 只负责测试 runner，不承载主业务流量。
  - 专用于预定义测试场景，输出测试报告和日志。

## 外部日志目录挂载

主 compose 中 controller 默认挂载：

```text
${MCP_TEST_LOG_ROOT_HOST:-./testsystem/logs}:${MCP_TEST_LOG_ROOT_DIR:-/opt/controller/test-logs}
```

测试 compose 中 testsystem-runner 默认挂载：

```text
${TEST_LOG_ROOT_HOST:-./testsystem/logs}:/workspace/test-logs
```

每次运行会写入独立目录：`<log-root>/<run-id>/`，包含 `run.log`、`summary.json`（和场景报告）。

## 从前端一键触发测试

1. 打开控制台 Runtime 页面。
2. 点击“任务与测试”面板中的“`一键测试 E5`”。
3. 前端调用 `POST /api/v1/test-runs`（场景固定 `e5_embedding_smoke`）。
4. 在同面板查看最近测试运行状态、摘要和日志路径。

## 2026-03-11 故障修复说明（已验证）

- 已修复 `one-click-up` 后前端偶发 `502`：
  - 根因：历史 `.env` 路径与数据库权限不兼容（旧 `modelintegrator` 路径、SQLite 目录只读）。
  - 修复：`scripts/one-click-up.sh` 增加旧路径自动迁移，并确保 `resources/config` 与 `controller.db` 可写。
- 已修复“一键运行 E5 测试”失败：
  - 根因 1：`/opt/controller/test-logs` 挂载目录权限不足，导致 `permission denied`。
  - 根因 2：容器内访问 `127.0.0.1:58001` 指向 controller 自身，而非宿主机 runtime。
  - 修复：启动脚本新增测试日志目录可写性探测；compose 新增 `host-gateway` 映射与 `MCP_CONTAINER_HOST_ALIAS`；后端对容器内 loopback endpoint 做自动改写。
- 复测结果：
  - `e5_embedding_smoke` 已可成功执行，返回维度校验通过（`dim=768`）。
  - 测试日志路径示例：`./testsystem/logs/<test-run-id>/run.log`。

## 日志排障建议

1. 先看 `summary.json` 的 `status/error`。
2. 再看 `run.log` 中失败阶段（如 start task、readiness、embedding 请求）。
3. 若 readiness 失败，优先检查模型 API 中 `desired_state/observed_state/readiness/health_message`。
4. 若 embedding 失败，使用 `scripts/e5_embedding_client.sh` 直接重放请求。
5. 若错误包含 `permission denied`（`/opt/controller/test-logs`），检查 `.env` 中 `MCP_TEST_LOG_ROOT_HOST` 指向目录是否可写。
6. 若错误包含 `127.0.0.1:58001 connect refused` 且 controller 运行在容器内，检查 `MCP_CONTAINER_HOST_ALIAS` 与 compose `extra_hosts` 是否生效。

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
- `GET /api/v1/agents`
- `POST /api/v1/agents/register`
- `POST /api/v1/agents/{id}/heartbeat`
- `POST /api/v1/agents/{id}/capabilities`

节点接口新增字段（`GET /api/v1/nodes`）：

- `classification`（controller/worker/agent-host/hybrid/unknown）
- `capability_tier`（tier-0/tier-1/tier-2/tier-3/unknown）
- `capability_source`（static/runtime/agent-reported/merged/unknown）
- `agent.status`、`agent.last_heartbeat_at`
- `runtimes[].status`、`runtimes[].capabilities`、`runtimes[].actions`、`runtimes[].last_seen_at`
