# Schema：架构与能力总说明

本文档承接原 README 中的详细章节，并按“前半架构设计 / 后半能力与排障”重新编排。

- 精简入口文档：[`../README.md`](../README.md)
- 变更日志：[`./LOG.md`](./LOG.md)
- 研究索引：[`./Research-Index.md`](./Research-Index.md)
- 增强路线图：[`./Enhancement-Roadmap.md`](./Enhancement-Roadmap.md)

---

## 0. 阶段 0：运行对象模型重构（2026-03-13）

本节是当前版本的建模基线，先于后续 A/B/C/D/E 阶段执行。目标是把“模型资产 / 运行模板 / 绑定配置 / 运行实例 / 运行包约束”彻底拆开，并给 controller 后续编排留出稳定对象面。

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

### F. 与阶段 A/B/C/D/E 的关系

当前开发顺序（新计划）：

1. 阶段 0：运行对象模型重构（本次）
2. 阶段 A：Agent 节点执行面做实
3. 阶段 B：Controller 编排内核做深
4. 阶段 C：外围组件真实联调（Nginx -> LiteLLM -> 外部 embedding/RAG）
5. 阶段 D：产品化与长期扩展（UI、更多模型类型、custom bundle/expert mode、审计恢复策略插件化）
6. 阶段 E：应用层任务调度器（application planner / heterogeneous orchestration / workflow planner）

衔接关系：

- 阶段 0 先明确对象模型，避免后续执行面/编排面继续绑定在“模型即运行态”的旧路径上。
- 阶段 A 使用 `RuntimeInstance + Binding + Manifest` 做节点侧 precheck/执行输入。
- 阶段 B 围绕 `RuntimeInstance` 进行 reconcile/conflict/drift 协调。
- 阶段 C 使用 `RuntimeInstance.endpoint/exposed_ports` 承接 Nginx/LiteLLM/外部 client 联调。
- 阶段 D 在 `custom_bundle + manifest` 约束下演进专家模式，避免一次性 hack。
- 阶段 E 建立在 `controller + agent + RuntimeInstance` 之上，进入应用层任务拆解、模型选择、结果聚合编排。

## 0.1 阶段 A 第 1 步：Agent 任务输入升级为 Instance-First（2026-03-14）

阶段 0 已把运行对象拆分完成，但 agent 任务仍主要依赖 `model_id/runtime_id` 粗粒度输入。  
如果不升级输入协议，controller 在创建节点本地任务时仍要靠模型 metadata 猜上下文，agent 端也无法稳定获得 binding/manifest 约束信息。

本轮原则：

- agent 任务以 `RuntimeInstance` 为第一入口对象（`runtime_instance_id`）。
- controller 创建任务时先解析：`RuntimeInstance -> RuntimeBinding -> RuntimeTemplate -> RuntimeBundleManifest`。
- 解析结果写入任务 `payload/detail/resolved_context`，agent 拉到任务即可直接消费。
- 旧 `model_id/node_id` 路径仅保留兼容用途，标记为 `legacy/compatibility`。

### A. task 与 instance/binding/manifest 的关系（阶段 A 第 1 步）

```text
Task(agent.*)
  input: runtime_instance_id (preferred)
    -> resolve RuntimeInstance
    -> resolve RuntimeBinding
    -> resolve RuntimeTemplate
    -> resolve RuntimeBundleManifest
  output payload:
    runtime_instance_id/runtime_binding_id/runtime_template_id/manifest_id
    node_id/model_id/task_scope
    resolved_context(binding_mode/runtime_kind/template_type/model_path/script_ref/ports/env...)
```

### B. 为什么必须升级输入对象

- `RuntimeInstance` 才是 controller reconcile 与节点执行的运行态对象。
- `RuntimeBinding` 才能表达 binding mode/script/env/挂载策略。
- `RuntimeBundleManifest` 才能表达模板约束（runtime kind、ports、required env）。
- 只有把三者一起放入 agent 任务上下文，后续 preflight、bundle 检查、状态反哺才可持续演进。

## 0.2 阶段 A 第 4 步：状态归属收口到 RuntimeInstance（2026-03-14）

阶段 A 第 4 步把“agent 节点事实”统一收口到 `RuntimeInstance`，形成 instance-first 运行态视图。

### A. 状态归属原则（收口版）

- `RuntimeInstance`：实例运行态主对象，优先承载 `precheck_* / observed_state / readiness / health_message / drift_reason / resolved_* / last_agent_task`。
- `Node`：节点级事实对象，承载 agent 在线状态、资源快照摘要、节点能力画像、last_seen。
- `Model`：资产与对外能力对象，优先消费 instance 投影，不再沉淀过多实例级检查细节。
- `Task`：一次动作过程对象，保留原始 detail/result，不是长期运行态唯一归宿。

### B. 统一映射（agent -> instance-first）

| agent task type | RuntimeInstance 主更新 | Node 摘要回写 | Model 投影 |
| --- | --- | --- | --- |
| `agent.runtime_precheck` | `precheck_status/gating/reasons/precheck_result/resolved_mounts/resolved_ports/resolved_script` | 无强制（可留 task 原文） | 读取 instance 投影更新 readiness/health |
| `agent.runtime_readiness_check` | `readiness/health_message/observed_state` | 无强制 | 读取 instance 投影 |
| `agent.port_check` | `resolved_ports/health_message`（失败可降 readiness） | 无强制 | 读取 instance 投影 |
| `agent.model_path_check` | `resolved_mounts/health_message`（失败可降 readiness） | 无强制 | 读取 instance 投影 |
| `agent.resource_snapshot` | `last_agent_task + instance metadata snapshot 摘要` | 节点资源摘要（CPU/内存/磁盘/docker 可达性） | 仅保留轻量 metadata |
| `agent.docker_inspect` | `observed_state/readiness/endpoint/health_message` | 运行时在线状态摘要 | 读取 instance 投影 |
| `agent.docker_start_container` / `agent.docker_stop_container` | `observed_state/readiness/health_message` | 节点 runtime 在线状态摘要 | 读取 instance 投影 |

### C. 阶段 B 进入条件（文档口径）

满足以下条件即可进入阶段 B：

1. agent 检查/执行结果已稳定先写入 `RuntimeInstance`。  
2. API/前端可直接观察 instance 的 precheck/readiness/drift/last agent task。  
3. controller 仍保留 direct fallback，但已是兼容路径而非主路径。  
4. testsystem 可重复验证“任务成功 + instance 状态变化”链路。  

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
- 阶段 A 第 3 步约定：`one-click-up` 默认启用 local-agent，controller node 的节点局部动作优先走 agent。

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
- 阶段 E：应用层任务调度器（在现有控制面之上实现 heterogeneous orchestration / workflow planner）

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

- 阶段 A（Layer 1/2 衔接）：agent 节点执行面继续做实，强化 instance-first 状态收口与 local-agent-first 主路径。
- 阶段 B（Layer 2 重点）：controller 编排内核深化（instance-first reconcile、precheck/conflict/drift、load/unload planner）。
- 阶段 C（Layer 1/2 联通）：外围组件真实联调（Nginx -> LiteLLM -> 外部 embedding/RAG）与端到端链路稳定化。
- 阶段 D（Layer 1/2 完善）：产品化与专家模式演进（custom bundle 在 manifest 约束下扩展）。
- 阶段 E（Layer 3 进入）：应用层任务调度器（application planner / workflow orchestrator），承接复杂任务拆解、模型选择与结果聚合。
- 研究与增强资料索引：[`./Research-Index.md`](./Research-Index.md)
- 阶段增强路线图：[`./Enhancement-Roadmap.md`](./Enhancement-Roadmap.md)

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
- `agent.docker_start_container`
- `agent.docker_stop_container`

执行类与检查类边界（阶段 A）：

- 检查类：`runtime_precheck/runtime_readiness/port_check/model_path_check`
- 执行类：`docker_start_container/docker_stop_container`
- 观测类：`docker_inspect/resource_snapshot`

local-agent-first（controller node）：

- `runtime.start/stop/refresh` 在 controller node 场景优先派发 local-agent 子任务。
- controller direct 路径保留为 compatibility fallback（agent 不在线或子任务失败时触发）。

核心接口：

- `GET /api/v1/agents/{id}/tasks/next`
- `POST /api/v1/agents/{id}/tasks/{taskID}/report`

#### 13.5 独立测试工具链（`testsystem/`）

- `docker-compose.test.yml`
- `scripts/run_test.sh`
- `scripts/collect_logs.sh`
- `scenarios/e5_embedding_smoke.sh`
- `scenarios/local_agent_execution_smoke.sh`
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

- 路线已从“单轴阶段”升级为“阶段 + 三层增强”双视图：详见 [`./Enhancement-Roadmap.md`](./Enhancement-Roadmap.md)。
- 阶段 A：继续做实 agent 节点执行面（Layer 1/2 衔接）。
- 阶段 B：做深 controller 调度与编排内核（Layer 2 重点）。
- 阶段 C：做通外围组件真实联调（Layer 1/2 系统联通）。
- 阶段 D：做完产品化与专家模式（Layer 1/2 完善）。
- 阶段 E：进入应用层任务调度器（Layer 3），但不替代当前 controller。
- 演进记录继续在 [`./LOG.md`](./LOG.md) 维护。

### 16. 研究参考与系统增强方向

#### 16.1 A. 三层增强视图

当前项目的增强路径不是单一控制面扩展，而是逐步形成三层系统：

1. 底层：模型服务与动态装卸层（Layer 1）
2. 中层：资源调度与 GPU 复用层（Layer 2）
3. 上层：应用层任务路由与多模型协作层（Layer 3）

对应文档：

- [`./Research-L1-Serving-and-Dynamic-Loading.md`](./Research-L1-Serving-and-Dynamic-Loading.md)
- [`./Research-L2-Scheduling-ColdStart-and-GPU-Pooling.md`](./Research-L2-Scheduling-ColdStart-and-GPU-Pooling.md)
- [`./Research-L3-Routing-Cascade-and-MultiModel-Orchestration.md`](./Research-L3-Routing-Cascade-and-MultiModel-Orchestration.md)

#### 16.2 B. 当前项目所处位置

- 当前项目已较强覆盖底层控制平面与部分中层调度基础：对象模型已拆分、instance-first 已进入 agent/controller 协作主链路。
- 当前项目尚未完成中层资源调度内核：系统化冷启动优化、GPU 池化、多模型并发策略仍在后续阶段。
- 当前项目尚未进入完整上层 application planner 阶段：Layer 3 仍属长期目标，不是当前实现状态。

#### 16.3 C. 后续阶段与三层增强路线对应关系

- 阶段 A：Agent 节点执行面做实（衔接 Layer 1/2）。
- 阶段 B：Controller 编排内核做深（重点进入 Layer 2）。
- 阶段 C：外围组件真实联调（Layer 1/2 的系统联通）。
- 阶段 D：产品化与专家模式（Layer 1/2 完善 + custom bundle）。
- 阶段 E：应用层任务调度器 / heterogeneous orchestration / workflow planner（进入 Layer 3）。

#### 16.4 D. 阶段 E 的定位

- 阶段 E 不是替代当前 `controller`。
- 阶段 E 建立在 `controller / agent / RuntimeInstance` 之上，消费现有控制面能力。
- 阶段 E 负责复杂任务拆解、模型选择、阶段编排、结果合并。
- 阶段 E 将把 Layer 3 的 FrugalGPT / RouteLLM / MoA 思想引入系统级策略。
