# Schema：架构与能力总说明

本文档承接原 README 中的详细章节，并按“前半架构设计 / 后半能力与排障”重新编排。

- 精简入口文档：[`../README.md`](../README.md)
- 变更日志：[`./LOG.md`](./LOG.md)

---

## 0. 阶段 0：运行对象模型重构（2026-03-13）

本节是当前版本的建模基线，先于后续 A/B/C/D 阶段执行。目标是把“模型资产 / 运行模板 / 绑定配置 / 运行实例 / 运行包约束”彻底拆开，并给 controller 后续编排留出稳定对象面。

### A. 运行对象模型总览

本轮正式引入五类对象：

| 对象 | 语义 | 关键字段（节选） |
| --- | --- | --- |
| `Model` | 模型资产定义（模型是什么） | `id/name/display_name/model_type/source_type/format/path_or_ref/default_args/requires_script/script_ref/tags` |
| `RuntimeTemplate` | 运行环境模板（怎么跑） | `template_type/runtime_kind/supported_model_types/supported_formats/capabilities/injectable_mounts/injectable_env/healthcheck/exposed_ports/dedicated` |
| `RuntimeBinding` / `LaunchProfile` | 模型与模板之间的启动绑定层 | `model_id/template_id/binding_mode/node_selector/preferred_node/mount_rules/env_overrides/command_override/script_ref/compatibility_status` |
| `RuntimeInstance` / `Deployment` | 实际运行态对象（调度与状态归集对象） | `binding_id/model_id/template_id/node_id/desired_state/observed_state/readiness/drift_reason/endpoint` |
| `RuntimeBundleManifest` | 模板包/自定义包约束层（受控高级模式） | `manifest_version/template_type/runtime_kind/model_injection_mode/mount_points/required_env/healthcheck/exposed_ports` |

### B. 五层对象关系说明

```text
Model(asset)
  -> RuntimeBinding(launch profile, mode + overrides + compatibility)
      -> RuntimeTemplate(runtime env recipe)
          -> RuntimeBundleManifest(constraint contract)
  -> RuntimeInstance(deployment/state object scheduled by controller)
```

强约束：

- 模型不是模板：`Model` 只描述模型资产，不承载运行时拼装细节。
- 模板不是实例：`RuntimeTemplate` 只定义运行环境，不代表已启动对象。
- `Binding` 是桥梁：模型与模板必须通过 `RuntimeBinding` 关联。
- `RuntimeInstance` 才是运行态：controller 后续 reconcile/precheck/conflict/drift 的主对象是实例。
- `Manifest` 是约束层：高级模式必须通过受约束 manifest 进入系统，不允许裸接任意 compose+脚本。

### C. Binding Modes（阶段 0 定稿）

| 模式 | 典型场景 | 优点 | 局限（阶段 0） |
| --- | --- | --- | --- |
| `dedicated` | 模型专有模板 | 行为固定、可预测 | 复用性差，模板数量可能上升 |
| `generic_injected` | 通用模板 + 模型目录/参数注入 | 复用性好，当前主路径 | 对注入规范和兼容性约束要求更高 |
| `generic_with_script` | 通用模板 + 注入 + 脚本覆盖 | 可处理复杂启动前后动作 | 阶段 0 仅完成建模/校验，占位执行 |
| `custom_bundle` | 专家模式/BYOR | 覆盖复杂多组件场景 | 阶段 0 仅开放受约束入口，非完整执行器 |

阶段 0 可用性：

- 已最小可用：`dedicated`、`generic_injected`（含 E5 样板链路）
- 已建模并接入校验：`generic_with_script`、`custom_bundle`

### D. 为什么“旧三类情况”不覆盖全部场景

旧路径可以覆盖多数“本地目录挂载 + 单容器 + 简单参数注入”的 Docker 化模型场景，但不能覆盖全部：

- 非本地目录挂载型模型（远程模型引用/下载中转）
- 单 runtime 多模型并存（实例与模型一对多/多对一关系）
- 多步骤/多容器/sidecar 场景
- 模板与模型之间显式兼容性约束（model type / format / capability）

因此本轮必须补齐：

- `RuntimeBinding`：显式表达模型-模板关系与注入策略
- `RuntimeBundleManifest`：显式表达模板/自定义运行包约束契约

### E. `custom_bundle` 定位（受约束高级模式）

- `custom_bundle` 是必要托底模式，用于专家/BYOR 场景。
- 该模式不是“任意 compose + 任意脚本裸奔接入”。
- 进入系统前必须通过 `RuntimeBundleManifest`（能力、注入方式、健康检查、端口暴露、环境变量要求）的约束校验。
- 阶段 0 已提供 manifest 数据结构、最小校验入口、API 可见性；完整 bundle 执行器放在后续阶段 D 演进。

### F. 与阶段 A/B/C/D 的关系

当前开发顺序（新计划）：

1. 阶段 0：运行对象模型重构（本次）
2. 阶段 A：Agent 节点执行面做实
3. 阶段 B：Controller 编排内核做深
4. 阶段 C：外围组件真实联调（Nginx -> LiteLLM -> 外部 embedding/RAG）
5. 阶段 D：产品化与长期扩展（UI、更多模型类型、custom bundle/expert mode、审计恢复策略插件化）

衔接关系：

- 阶段 0 先明确对象模型，避免后续执行面/编排面继续绑定在“模型即运行态”的旧路径上。
- 阶段 A 使用 `RuntimeInstance + Binding + Manifest` 做节点侧 precheck/执行输入。
- 阶段 B 围绕 `RuntimeInstance` 进行 reconcile/conflict/drift 协调。
- 阶段 C 使用 `RuntimeInstance.endpoint/exposed_ports` 承接 Nginx/LiteLLM/外部 client 联调。
- 阶段 D 在 `custom_bundle + manifest` 约束下演进专家模式，避免一次性 hack。

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

#### 1.6 阶段计划（新基线）

- 阶段 0（当前最优先）：运行对象模型重构（`Model / RuntimeTemplate / RuntimeBinding / RuntimeInstance / RuntimeBundleManifest`）
- 阶段 A：Agent 节点执行面做实（节点局部动作优先 agent，local-agent 成为标准路径）
- 阶段 B：Controller 编排内核做深（围绕 `RuntimeInstance` reconcile/precheck/conflict/drift）
- 阶段 C：外围组件真实联调（Nginx -> LiteLLM -> 外部 embedding/RAG，以 E5 链路为第一条标准链）
- 阶段 D：产品化与长期扩展（更完整 UI、多模型类型、custom bundle/expert mode、审计恢复与策略插件化）

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
- `GET /api/v1/runtime-templates/{id}/manifest`
- `GET /api/v1/runtime-bindings`
- `POST /api/v1/runtime-bindings`
- `GET /api/v1/runtime-bindings/{id}`
- `GET /api/v1/runtime-instances`
- `GET /api/v1/runtime-instances/{id}`

支持内置、配置、自定义模板统一注册，并提供 `binding -> instance` 运行态可见性。

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
