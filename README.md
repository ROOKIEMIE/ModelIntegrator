# Local LLM Control Plane（Controller）

- [中文（默认）](./README.md)
- [中文镜像](./README.zh-CN.md)
- [English](./README.en.md)

## README 文档说明和跳转

本 README 已精简为快速入口文档，仅保留部署与日常使用最需要的信息。

详细内容已迁移到以下文档：

- 完整架构与功能说明：[`doc/Schema.md`](./doc/Schema.md)
- 变更日志与演进记录：[`doc/LOG.md`](./doc/LOG.md)
- 分层研究索引：[`doc/Research-Index.md`](./doc/Research-Index.md)
- 增强路线图（A/B/C/D/E）：[`doc/Enhancement-Roadmap.md`](./doc/Enhancement-Roadmap.md)
- Layer 1 研究：[`doc/Research-L1-Serving-and-Dynamic-Loading.md`](./doc/Research-L1-Serving-and-Dynamic-Loading.md)
- Layer 2 研究：[`doc/Research-L2-Scheduling-ColdStart-and-GPU-Pooling.md`](./doc/Research-L2-Scheduling-ColdStart-and-GPU-Pooling.md)
- Layer 3 研究：[`doc/Research-L3-Routing-Cascade-and-MultiModel-Orchestration.md`](./doc/Research-L3-Routing-Cascade-and-MultiModel-Orchestration.md)

原 README 中的架构设计、系统形态、能力分级、功能细节、排障建议等章节，现统一收敛到 `doc/Schema.md`，并按“前半架构设计 / 后半能力与排障”重排。

## 简要描述

Local LLM Control Plane 是一个本地多节点 LLM 控制平面，采用 `controller + agent + managed node` 架构，用于统一管理模型、节点、运行时和任务。

## 项目定位

- 面向本地/局域网环境的多节点模型控制平面
- 短期定位：模型资源调度系统；长期演进：复杂任务的多模型编排运行时
- controller 负责统一 API、状态、调度与编排
- agent 负责节点本地动作执行与结果回传
- 支持 Docker/Portainer/LM Studio 等运行时接入
- 支持 Web 控制台 + REST API + SQLite 持久化

## 从 0 开始部署

目标目录（示例）：`/tank/docker_data/model_control_plane`

```bash
sudo mkdir -p /tank/docker_data/model_control_plane
sudo chown -R $USER:$USER /tank/docker_data/model_control_plane
rsync -a --delete /home/whoami/Dev/model-control-plane/ /tank/docker_data/model_control_plane/
cd /tank/docker_data/model_control_plane
```

初始化：

```bash
cp resources/docker/compose.example.env .env
mkdir -p resources/config resources/models testsystem/logs
touch resources/config/controller.db
chmod 777 resources/config testsystem/logs
chmod 666 resources/config/controller.db
```

启动：

```bash
# 核心（推荐，默认启用 controller node local-agent）
./scripts/one-click-up.sh

# 显式启用本机 agent（等价于默认行为）
./scripts/one-click-up.sh --local-agent

# 关闭本机 agent（仅用于兼容排障，回退 controller direct/self-check）
./scripts/one-click-up.sh --no-local-agent

# 核心 + addons
./scripts/one-click-up.sh --addons

# 核心 + 下载容器
./scripts/one-click-up.sh --download

# 核心 + vLLM 模板容器
./scripts/one-click-up.sh --vllm
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

- 编排文件：`docker-compose.yml`
- 镜像构建：`Dockerfile`
- 控制平面配置：`resources/config/config.example.yaml`
- Compose 环境变量：`resources/docker/compose.example.env`
- 网关配置：`resources/nginx/nginx.example.conf`
- 前端：`resources/web/index.html` / `resources/web/app.css` / `resources/web/app.js`
- 一键脚本：`scripts/one-click-up.sh` / `scripts/one-click-down.sh`
- 架构说明：`doc/Schema.md`
- 变更日志：`doc/LOG.md`
- 研究索引：`doc/Research-Index.md`
- 增强路线图：`doc/Enhancement-Roadmap.md`

## 关键配置项

- `MCP_EXTERNAL_PORT`：外部网关端口（默认 `59081`）
- `MCP_SQLITE_PATH`：SQLite 文件路径
- `MCP_MODEL_DIR_HOST`：宿主机模型目录（默认 `./resources/models`）
- `MCP_MODEL_ROOT_DIR`：容器内模型目录（默认 `/opt/controller/models`）
- `MCP_TEST_LOG_ROOT_HOST`：宿主机测试日志目录（默认 `./testsystem/logs`）
- `MCP_TEST_LOG_ROOT_DIR`：容器内测试日志目录（默认 `/opt/controller/test-logs`）
- `MCP_LMSTUDIO_ENDPOINT`：LM Studio 地址
- `MCP_DOCKER_ENDPOINT`：Docker endpoint
- `MCP_CONTAINER_HOST_ALIAS`：容器访问宿主机别名（默认 `host.docker.internal`）
- `MCP_VLLM_EXTERNAL_PORT`：vLLM 对外端口
- `MCP_VLLM_MODEL`：vLLM 默认模型
- `HUGGING_FACE_HUB_TOKEN`：私有/受限模型访问 token（可选）

## API

基础接口：

- `GET /healthz`
- `GET /api/v1/version`

节点与模型：

- `GET /api/v1/nodes`
- `GET /api/v1/models`
- `GET /api/v1/models/{id}`
- `POST /api/v1/models/{id}/load`
- `POST /api/v1/models/{id}/unload`
- `POST /api/v1/models/{id}/start`
- `POST /api/v1/models/{id}/stop`

运行时模板：

- `GET /api/v1/runtime-templates`
- `POST /api/v1/runtime-templates/validate`
- `POST /api/v1/runtime-templates`

运行对象（instance-first）：

- `GET /api/v1/runtime-bindings`
- `GET /api/v1/runtime-instances`
- `GET /api/v1/runtime-instances/{id}`
- `GET /api/v1/runtime-instances/{id}/tasks`
- `GET /api/v1/runtime-instances/{id}/summary`

agent 与任务：

- `GET /api/v1/agents`
- `POST /api/v1/agents/register`
- `POST /api/v1/agents/{id}/heartbeat`
- `POST /api/v1/agents/{id}/capabilities`
- `GET /api/v1/agents/{id}/tasks/next`
- `POST /api/v1/agents/{id}/tasks/{taskID}/report`
- `GET /api/v1/tasks`
- `GET /api/v1/tasks/{id}`
- `POST /api/v1/tasks/runtime/start`
- `POST /api/v1/tasks/runtime/stop`
- `POST /api/v1/tasks/runtime/restart`
- `POST /api/v1/tasks/runtime/refresh`
- `POST /api/v1/tasks/agent/runtime-readiness`
- `POST /api/v1/tasks/agent/node-local`

节点执行类 agent task（通过 `POST /api/v1/tasks/agent/node-local` 提交）：

- `agent.runtime_precheck`
- `agent.resource_snapshot`
- `agent.docker_inspect`
- `agent.docker_start_container`
- `agent.docker_stop_container`

测试运行：

- `GET /api/v1/test-runs`
- `GET /api/v1/test-runs/{id}`
- `POST /api/v1/test-runs`

脚本化 smoke（local-agent 路径）：

- `scripts/controller_api_smoke.sh <base_url> [token] [agent_id] [model_id]`
- `testsystem/scenarios/local_agent_execution_smoke.sh`

阶段 A 收口（当前状态）：

- agent 结果先回写 `RuntimeInstance`（precheck/readiness/drift/resolved 状态与最近 task 摘要）。
- `Node` 主要保留节点资源与 agent 在线事实，`Model` 主要消费 instance 投影。
- 阶段 B 将继续推进：instance-first reconcile、precheck/conflict/drift 深化、进一步收缩 direct fallback。
