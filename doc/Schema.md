# Schema：架构与能力总说明

本文档承接原 README 中的详细章节，并按“前半架构设计 / 后半能力与排障”重新编排。

- 精简入口文档：[`../README.md`](../README.md)
- 变更日志：[`./LOG.md`](./LOG.md)

---

## 第一部分（前半）：当前项目架构设计

### 1. 架构基准（2026-03 第一阶段修正版）

#### 1.1 术语与角色（强约束）

- `controller`：控制平面，负责全局状态、任务编排、调度决策、统一 API、前端控制台、持久化状态。
- `agent`：节点执行面，运行在每个 managed node 上，负责节点本地动作执行与事实回传。
- `managed node`：被纳管节点；每个 managed node 都应运行 agent。
- `controller node`：承载 controller 的节点；它同时是 managed node，也应运行 agent。
- 架构主术语统一为 `controller / agent / managed node`。

#### 1.2 职责边界

- controller 负责“决定做什么”：目标状态、全局协调、冲突检测、reconcile、审计。
- agent 负责“在本机怎么做”：端口检查、路径检查、docker inspect、runtime precheck/readiness、资源采集。
- 节点局部动作优先下沉到 agent，controller 基于 agent 回传做全局判断。

#### 1.3 同机 Agent 原则

- controller node 也运行 local agent。
- controller 与 local agent 复用与 remote agent 一致的协议链路。
- 目标：统一执行路径、减少本机特例、简化同机联调和回归。

#### 1.4 Controller 最小降级自检边界

controller 仅保留自举与存活必需的本机检查：

- 配置读取与基础参数检查
- SQLite 初始化与可写性
- 关键目录检查（模型目录、测试日志目录）
- 监听与启动前最小健康准备

以上能力属于 `controller self-check / bootstrap fallback`，不能扩展为 controller 代替 agent 执行节点动作。

#### 1.5 测试策略（当前阶段）

- 第一优先：`controller + local agent`
- 第二优先：`controller + remote agent`

先稳定同机协议链路，再扩展跨机变量，降低联调复杂度。

#### 1.6 阶段计划（修正版）

第一阶段（当前）：

- 增强 agent 任务执行面（`runtime_readiness_check`、`runtime_precheck`、`port_check`、`model_path_check`、`resource_snapshot`、`docker_inspect`）
- 结果反哺 node/runtime/model 状态（含 readiness/drift）
- controller 持续 reconcile，解释 desired/observed 差异
- precheck/conflict 贯彻“局部事实走 agent、全局协调在 controller”

第二阶段（后续）：

- 外围组件联调（Nginx / LiteLLM / 外部 embedding client）
- 完善 remote agent 与多 runtime 协同闭环、稳定性与回归体系

### 2. 当前系统形态（2026-03）

- 主控制面入口：`src/cmd/controller`
- 已具备 Web 控制台、REST API、节点/模型管理、运行时模板、任务系统
- 已接入 LM Studio 与 Docker/Portainer 适配器
- 通过 Compose profile 扩展 addons/download/vllm/local-agent

### 3. 目标系统形态（定稿）

三层结构：

1. controller（中央控制器）
2. agent（节点执行面，覆盖所有 managed node）
3. external runtimes（LM Studio / Ollama / vLLM / OpenAI-compatible）

### 4. 节点分类（定稿）

- `Controller Node`
- `Managed Node`
- `Runtime-only Node`
- `Offline/Catalog Node`

### 5. 节点能力分级（定稿）

- `runtime_managed`
- `service_observed`
- `agent_managed`

### 6. 混合节点管理（定稿）

- 节点是宿主，runtime 是能力单元
- 一个节点可同时挂载多个 runtime
- 调度目标应尽量落到“节点上的某个 runtime”

### 7. 前端展示策略（定稿）

- 展示模型：`node + runtime + capability`
- 节点卡片展示分类、agent 状态、能力级别、心跳与能力来源
- runtime 子项展示在线状态、能力集、可执行动作

### 8. Agent 部署形态（定稿）

- 原生二进制（优先）：macOS / Windows / Linux
- 容器版（可选）：Linux + Docker/Podman

### 9. llmfit 集成方案（定稿）

- 作为 agent 可选能力，不做源码级嵌入
- 主路径：agent 托管 llmfit serve
- 通信：agent 通过 loopback HTTP 调用
- 兜底：保留 CLI fallback

### 10. 建议目录结构与演进方向

```text
src/cmd/
  controller/
  agent/

src/pkg/
  config/
  model/
  registry/
  runtime/
  adapter/
  agent/
  controller/
  fit/
  capability/
  scheduler/
  telemetry/
```

---

## 第二部分（后半）：能力现状、规划、功能说明与排障

### 11. 已实现能力总览

- Go 后端 + REST API + 静态前端控制台
- Runtime/List + Runtime/Template + Download 页面
- 节点/模型状态联动、模型动作互斥
- 运行时模板校验与注册
- LM Studio + Docker/Portainer 适配
- 任务系统（runtime 动作、agent 任务）
- 测试运行系统（test-runs）
- SQLite 持久化关键状态

### 12. 计划实现能力（阶段化）

- 完整 remote agent 扩展与多节点规模化纳管
- 更完整 runtime 类型覆盖与动作编排
- 调度策略、冲突策略与动作审计增强
- 下载编排 UI 与测试体系完善

### 13. 功能详细说明

#### 13.1 E5 embedding 样板（TEI）

- 样板模型：`local-multilingual-e5-base`
- 默认模板：`tei-embedding-e5-local`（端口 `58001:80`）
- 支持 runtime start/stop/refresh 任务化控制
- 测试脚本：`scripts/e5_embedding_client.sh`
- 场景：`testsystem/scenarios/e5_embedding_smoke.sh`

#### 13.2 runtime 动作任务化

- 持久化表：`tasks`
- runtime 任务接口：
  - `POST /api/v1/tasks/runtime/start`
  - `POST /api/v1/tasks/runtime/stop`
  - `POST /api/v1/tasks/runtime/refresh`
- 状态：`pending/dispatched/running/success/failed/timeout/canceled`

#### 13.3 desired / observed / readiness

模型状态关键字段：

- `desired_state`
- `observed_state`
- `readiness`
- `health_message`
- `last_reconciled_at`

#### 13.4 agent 最小任务执行面

已落地任务类型：

- `agent.runtime_readiness_check`
- `agent.runtime_precheck`
- `agent.port_check`
- `agent.model_path_check`
- `agent.resource_snapshot`
- `agent.docker_inspect`

核心接口：

- `GET /api/v1/agents/{id}/tasks/next`
- `POST /api/v1/agents/{id}/tasks/{taskID}/report`

#### 13.5 独立测试工具链（`testsystem/`）

- `docker-compose.test.yml`
- `scripts/run_test.sh`
- `scripts/collect_logs.sh`
- `scenarios/e5_embedding_smoke.sh`
- 日志输出：`./testsystem/logs/<run-id>/`

#### 13.6 controller 测试运行能力 + 前端一键测试

- `POST /api/v1/test-runs`
- `GET /api/v1/test-runs`
- `GET /api/v1/test-runs/{id}`
- 前端按钮仅调用后端 API，不执行本地 shell

#### 13.7 主 compose 与测试 compose 区分

- 主系统：`docker-compose.yml`
- 测试系统：`testsystem/docker-compose.test.yml`

#### 13.8 外部日志目录挂载

主系统：

```text
${MCP_TEST_LOG_ROOT_HOST:-./testsystem/logs}:${MCP_TEST_LOG_ROOT_DIR:-/opt/controller/test-logs}
```

测试系统：

```text
${TEST_LOG_ROOT_HOST:-./testsystem/logs}:/workspace/test-logs
```

#### 13.9 nodes 配置说明

- 建议显式配置 `controller.node_id/node_name/node_host`
- 节点 ID 建议使用 `node-controller`、`node-managed-*`
- 角色仅使用 `controller/managed`

#### 13.10 LM Studio 兼容说明

- 列表优先 `/api/v1/models`，回退 `/v1/models`
- `load/unload` 优先新接口，必要时回退旧接口
- `unload` 优先 `instance_id`

#### 13.11 Download 功能说明（当前阶段）

- Download 页签当前为预留页
- `download` profile 提供 `hf-downloader` 与 `aria2-downloader`

#### 13.12 vLLM 容器说明

- `vllm` profile 提供 `vllm-runtime`
- 当前聚焦 `controller + Docker + NVIDIA` 场景

#### 13.13 运行时模板扩展

核心接口：

- `GET /api/v1/runtime-templates`
- `POST /api/v1/runtime-templates/validate`
- `POST /api/v1/runtime-templates`

支持内置、配置、自定义模板统一注册。

### 14. 日志排障与运维建议

#### 14.1 2026-03-11 故障修复（已验证）

- 修复 one-click-up 后偶发 502（历史路径与 SQLite 权限问题）
- 修复一键测试 E5 失败（日志目录权限与容器内 loopback 访问问题）

#### 14.2 通用排障流程

1. 先看 `summary.json` 的 `status/error`
2. 再看 `run.log` 失败阶段
3. readiness 失败时检查 `desired/observed/readiness/health_message`
4. embedding 失败时用 `scripts/e5_embedding_client.sh` 重放
5. `permission denied` 先检查 `MCP_TEST_LOG_ROOT_HOST` 可写性

#### 14.3 SQLite 持久化说明

- 持久化 agent/node/model/runtime 关键状态
- 建议持久挂载 `resources/config/controller.db`
- controller 重启后会回填关键状态并继续 reconcile

### 15. 路线说明（已确认）

- 继续强化 agent-first 的节点动作链路
- 扩展远端 agent/runtime 协调能力
- 完善下载编排、测试覆盖与可观测性
- 持续在 `doc/LOG.md` 维护演进记录
