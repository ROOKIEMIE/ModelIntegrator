# 变更日志

## 2026-03-14

### 文档改造：外部参考脉络分层归档与增强路线补全

本轮不是改代码主逻辑，而是补齐系统“外部参考脉络”，把成熟项目/论文与当前对象模型、阶段路线正式对齐。

### 为什么现在做这件事

- 阶段 0 已完成运行对象模型重构，阶段 A 已进入 instance-first 与 local-agent-first，系统基础面已具备“承接外部成熟成果”的条件。
- 若继续只在 LOG 里追加零散说明，后续阶段 B/C/D/E 会缺少统一参考底座，评估与取舍容易漂移。
- 因此本轮把研究材料下沉到 `doc/` 分层文档，并把增强计划提升为正式路线文档。

### 新增文档（按三层归置）

- Layer 1（模型服务 / 动态装卸 / 多模型 serving 原语）：
  - `doc/Research-L1-Serving-and-Dynamic-Loading.md`
- Layer 2（资源调度 / 冷启动 / GPU 复用 / 多模型并发 serving）：
  - `doc/Research-L2-Scheduling-ColdStart-and-GPU-Pooling.md`
- Layer 3（应用层路由 / 级联 / 多模型协作）：
  - `doc/Research-L3-Routing-Cascade-and-MultiModel-Orchestration.md`
- 总索引与路线图：
  - `doc/Research-Index.md`
  - `doc/Enhancement-Roadmap.md`

### 路线更新（A/B/C/D/E）

- 在 `doc/Schema.md` 正式补充“研究参考与系统增强方向”章节，明确三层增强视图与当前所处位置。
- 后续路线从 A/B/C/D 扩展为 A/B/C/D/E，并明确：
  - 阶段 A：Agent 节点执行面做实（Layer 1/2 衔接）
  - 阶段 B：Controller 编排内核做深（Layer 2 重点）
  - 阶段 C：外围组件真实联调（Layer 1/2 联通）
  - 阶段 D：产品化与专家模式（Layer 1/2 完善）
  - 阶段 E：应用层任务调度器（Layer 3 进入）

### 为什么新增阶段 E

- 项目长期价值不止于底层控制平面，还包括复杂任务的多模型协作编排。
- 阶段 E 的定位是建立在 `controller + agent + RuntimeInstance` 之上的 application planner/workflow orchestrator。
- 阶段 E 不是替代 controller，而是消费 controller 的底层能力，并把 FrugalGPT/RouteLLM/MoA 思想转化为上层策略。

### 对后续阶段的影响

- 阶段 A/B：继续优先做实 instance-first 执行与调度内核，为 Layer 2 打底。
- 阶段 C/D：把 Layer 1/2 的能力做成可联调、可产品化、可治理的系统链路。
- 阶段 E：在前述基础稳定后再进入 Layer 3，避免上层路由先行导致底层失稳。

### 阶段 A 第 4 步：agent 结果状态归属收口（instance-first）

本轮把 agent 结果反哺规则明确为：`RuntimeInstance` 为第一落点，`Node/Model/Task` 为投影或过程承载。

### 收口原则

- `RuntimeInstance` 优先承载：
  - `precheck_status/precheck_gating/precheck_reasons/precheck_result`
  - `observed_state/readiness/health_message/drift_reason`
  - `resolved_mounts/resolved_ports/resolved_script`
  - `last_agent_task` 摘要
- `Node` 继续承载：
  - agent 在线状态、last_seen
  - 节点资源快照摘要（CPU/内存/磁盘/docker 可达性）
- `Model` 调整为：
  - 以 instance 投影更新对外状态（readiness/health/observed）
  - 不再优先沉淀实例级节点检查细节
- `Task` 保留：
  - 一次动作原始 detail/result（审计与排障）

### 代码收敛点

- `TaskService.ReportAgentTask` 调整为 instance-first 顺序：
  - 先 `RuntimeObjectService.ApplyAgentTaskObservation`
  - 再 `NodeService.ApplyAgentTaskObservation`
  - 最后 `ModelService.ApplyAgentTaskObservation`
- `RuntimeObjectService.ApplyAgentTaskObservation` 扩展为统一映射：
  - `agent.runtime_precheck`
  - `agent.runtime_readiness_check`
  - `agent.port_check`
  - `agent.model_path_check`
  - `agent.resource_snapshot`
  - `agent.docker_inspect`
  - `agent.docker_start_container`
  - `agent.docker_stop_container`
- `RuntimeInstance` API 视图增强：
  - 新增 `binding_mode/manifest_id/resolved_*`
  - 新增 `last_agent_task` 摘要
  - 新增 `GET /api/v1/runtime-instances/{id}/summary`

### testsystem 收口验证

- `local_agent_execution_smoke` 新增 instance 收口检查：
  - 基于 instance 触发 `agent.runtime_precheck`
  - 查询 `runtime-instances/{id}/summary`
  - 验证 `precheck_status` 与 `last_agent_task` 已回写 instance

### 阶段 B 进入条件（明确）

阶段 A 到达以下状态即可进入阶段 B：

1. agent 结果稳定先写 RuntimeInstance，再做 Node/Model 投影。  
2. API/前端可直接观察 instance 的 precheck/readiness/drift/last task 摘要。  
3. local-agent-first 是主路径，controller direct 仅兼容 fallback。  
4. testsystem 能重复验证“任务完成 + instance 状态变化”。

阶段 B 重点：

- instance-first reconcile 主循环
- precheck/conflict/drift 深化
- 更少 direct fallback（继续收缩兼容路径）

不再属于阶段 A 的问题：

- 更复杂的冲突仲裁策略（多实例/多节点）
- 全量调度阻塞策略与回退编排
- 深度 bundle 执行器与跨 runtime 复杂编排

### 阶段 A 第 3 步：local-agent-first + 节点执行面扩展

本轮把 agent 从“检查器”为主推进到“最小真实执行面”，并让 controller node 的 local-agent 成为默认标准路径。

### local-agent-first（controller node）

- `scripts/one-click-up.sh` 现在默认启用 `local-agent` profile，`--no-local-agent` 仅保留兼容排障用途。
- `docker-compose.yml` 明确 local-agent 是阶段 A 的标准执行路径，并补充执行能力声明（`docker-start/docker-stop/docker-inspect/resource-snapshot`）。
- `TaskService` 在 `runtime.start/runtime.stop/runtime.refresh` 上增加 local-agent-first 尝试逻辑：
  - 优先派发 agent 子任务（controller node + local-agent 期望场景）
  - 失败时回退 `controller direct`（compatibility fallback）
  - 回退原因写入父任务 detail（可审计）

### agent 执行动作扩展

新增并接入任务类型：

- `agent.docker_start_container`
- `agent.docker_stop_container`

现有任务增强：

- `agent.docker_inspect`：继续作为标准容器事实探针
- `agent.resource_snapshot`：增加磁盘、模型目录、docker 可达性、关键依赖可用性摘要

执行器特性：

- `docker` CLI 优先，缺失时自动走 Docker API fallback（`unix:///var/run/docker.sock` / tcp endpoint）
- 返回结构化 detail，包含 `runtime_exists/runtime_running/observed_state/action_transport` 等字段
- 明确禁止任意 shell 通道，仍保持受约束动作集合

### controller 侧消费增强

- `TaskService.ReportAgentTask` 现在会把 agent 结果同步反哺：
  - `ModelService`
  - `NodeService`
  - `RuntimeObjectService`（新增）
- `RuntimeObjectService.ApplyAgentTaskObservation` 新增：
  - 消费 precheck/docker/snapshot 结果
  - 更新 `RuntimeInstance` 的 `observed_state/readiness/health_message/precheck_*`
  - 记录 runtime instance 元数据中的快照事实与容器事实

### 前端与 smoke

- 任务列表新增“检查/执行/观测”分类（agent 任务可快速区分执行类与检查类）。
- 节点卡片增加 local-agent 可见性：`preferred_local_agent_id`、最近 agent 动作与时间。
- runtime instance 卡片增加最近执行类动作摘要。
- `scripts/controller_api_smoke.sh` 增强：支持 local-agent 路径触发并等待
  - `agent.runtime_precheck`
  - `agent.docker_inspect`
  - `agent.resource_snapshot`
- 新增 `testsystem/scenarios/local_agent_execution_smoke.sh`，并在 `TestRunService` 中开放 `local_agent_execution_smoke` 场景。

### fallback/self-check（当前仍保留）

- controller `self-check`：SQLite/目录/启动前最小检查（不扩展为节点动作执行）。
- runtime 动作：当 local-agent-first 子任务不可用或失败时，仍回退 controller direct 路径（兼容期保留）。

### 阶段 A 第 1 步：agent 任务切换到 instance-first 视角

本轮重点不是执行器大改，而是把 agent 任务输入协议从 `model/runtime` 粗粒度升级为围绕运行对象：

- `RuntimeInstance`
- `RuntimeBinding`
- `RuntimeTemplate`
- `RuntimeBundleManifest`

### 协议字段与上下文补齐

- agent node-local 任务 payload/detail 新增（或统一透传）：
  - `runtime_instance_id`
  - `runtime_binding_id`
  - `runtime_template_id`
  - `manifest_id`
  - `node_id`
  - `model_id`
  - `task_scope`
  - `payload_context`
  - `resolved_context`
- 兼容原则：`tasks` 表结构不变，继续通过 JSON 字段持久化与 API 输出。

### controller 侧能力

- `TaskService` 支持最小输入仅 `runtime_instance_id` 创建 node-local agent task。
- 创建时执行解析链：
  - `RuntimeInstance -> RuntimeBinding -> RuntimeTemplate -> RuntimeBundleManifest`
- 自动补齐 node/agent 选择与 resolved context（binding mode/runtime kind/template type/路径/脚本/端口/env 约束）。
- 增加清晰错误：instance/binding/template/manifest 缺失或不匹配时直接返回可定位错误。

### API 增量

- 新增：
  - `POST /api/v1/tasks/agent/instance-local`（instance-first 创建入口）
  - `GET /api/v1/runtime-instances/{id}/tasks`（查询某 runtime instance 最近 agent tasks）
- 增强：
  - `POST /api/v1/tasks/agent/node-local` 支持 `runtime_instance_id` 与新上下文字段
  - `GET /api/v1/tasks` 支持 `runtime_instance_id` / `agent_only` 过滤

### agent 侧能力

- agent 在执行前统一解析 `resolved_context/payload`：
  - `runtime_instance_id`
  - `binding_mode`
  - `manifest` 核心字段（`runtime_kind/template_type/ports/env`）
  - `model_path/script_ref/runtime_container_id` 等执行上下文
- 上下文缺失改为结构化错误（`AgentTaskProtocolError`），不再只返回裸字符串。

### 前端最小增强

- 任务列表新增展示：
  - `runtime_instance_id`
  - `runtime_binding_id`
  - `binding_mode`
- runtime instance 列表新增“最近 agent task 摘要”。

### compatibility path（保留）

- `model_id/node_id` 直接创建路径继续保留用于兼容或过渡。
- 当 instance 解析不可用时，任务会标记 `task_scope=legacy_model_or_node`（compatibility path）。

### 2026-03-14 补充修复：agent/console 在 API 抖动或 SQLite 争用下的可用性

- `sqlite` 增加运行时 pragma（`busy_timeout=5000`、`journal_mode=WAL`、`synchronous=NORMAL`）以降低 `SQLITE_BUSY` 导致的任务查询/agent 心跳失败概率。
- agent `doJSON` 错误输出追加服务端 detail，避免只看到“拉取 agent task 失败/心跳失败”而无法定位根因。
- 前端初始化改为 `Promise.allSettled`，`tasks` 接口失败不再拖垮整个控制台页面（节点/模型/runtime 视图可继续加载）。

## 2026-03-13

### 阶段 0（运行对象模型重构）插入为最高优先级

- 当前系统已经有 `Model` 和 `RuntimeTemplate`，但二者关系长期靠隐式 metadata（如 `runtime_template_id`）拼接，controller 运行态也主要围绕 model 状态字段演进。
- 若继续直接推进 agent 执行面（阶段 A）和 controller 编排（阶段 B），会把“模型=模板=实例”的历史耦合放大，后续 precheck/conflict/drift 很难收敛。
- 因此先执行阶段 0，先把运行对象关系拆清，再推进 A/B/C/D。

### 本轮架构拆分（正式对象）

本轮在代码、存储、API、前端、文档同步落地：

- `Model`：模型资产定义（新增 `display_name/model_type/source_type/format/path_or_ref/default_args/requires_script/script_ref/tags`）
- `RuntimeTemplate`：运行环境模板（新增 `template_type/runtime_kind/supported_model_types/supported_formats/capabilities/injectable_* / healthcheck / exposed_ports / dedicated / manifest`）
- `RuntimeBinding`（LaunchProfile）：模型与模板的桥接绑定对象
- `RuntimeInstance`（Deployment）：运行态实例对象（controller 后续调度对象）
- `RuntimeBundleManifest`：模板/自定义 bundle 的约束契约对象

### Binding Modes 定稿（四种）

- `dedicated`
- `generic_injected`
- `generic_with_script`
- `custom_bundle`

阶段 0 可执行路径：

- 已最小可用：`dedicated`、`generic_injected`
- 已建模+校验占位：`generic_with_script`、`custom_bundle`

### 为什么需要 custom_bundle，但必须受 Manifest 约束

- 真实生产场景存在多步骤/多容器/sidecar/脚本前后处理，必须保留高级托底模式。
- 但“任意 compose + 任意脚本裸接入”会破坏控制面可审计性与可维护性。
- 因此本轮将 custom 模式收敛为 `custom_bundle + RuntimeBundleManifest`，先立约束，再逐步放开执行能力。

### 本轮代码落地范围（阶段 0 实施）

- 数据模型：
  - `src/pkg/model/types.go` 新增/增强 Model、RuntimeTemplate、RuntimeBinding、RuntimeInstance、RuntimeBundleManifest 结构与枚举。
- SQLite schema/store：
  - `src/pkg/store/sqlite/schema.go` 新增 `runtime_bindings`、`runtime_instances`、`runtime_bundle_manifests` 表，并扩展 `models` 字段。
  - `src/pkg/store/sqlite/store.go` 新增 bindings/instances/manifests 的 upsert/list/get；Model 持久化字段扩展。
- 服务层：
  - 新增 `src/pkg/service/runtime_object_service.go`，实现：
    - binding 创建/读取/列出
    - template manifest 解析与最小校验
    - `binding -> runtime instance` 生成更新
    - 启动时 bootstrap 与模板 manifest 同步
  - `ModelService` 接入 `RuntimeObjectSyncer`，在刷新/持久化链路中触发 runtime object 同步。
- API：
  - 新增接口：
    - `GET /api/v1/runtime-bindings`
    - `POST /api/v1/runtime-bindings`
    - `GET /api/v1/runtime-bindings/{id}`
    - `GET /api/v1/runtime-instances`
    - `GET /api/v1/runtime-instances/{id}`
    - `GET /api/v1/runtime-templates/{id}/manifest`
- 前端（最小增量）：
  - 模型卡片展示 binding/instance 关系（含 binding mode）
  - Runtime/Observe 面板新增 runtime bindings、runtime instances 列表
  - 模板列表新增 manifest 与绑定数量可见性
- E5 样板迁移链路：
  - `local-multilingual-e5-base`（Model）+
  - `tei-embedding-e5-local` / `tei-embedding`（RuntimeTemplate）+
  - 自动生成 `RuntimeBinding`（默认 `generic_injected`）+
  - 自动生成 `RuntimeInstance`（`ri-local-multilingual-e5-base`）

### 下一步与阶段 A/B/C/D 的衔接

- 阶段 A：让 agent 任务直接引用 `RuntimeInstance`，并利用 binding/manifest 做节点侧 precheck 输入。
- 阶段 B：controller reconcile 从 model 状态逐步转为 instance 优先，显式处理 conflict/drift。
- 阶段 C：围绕 runtime instance endpoint/exposed_ports 对接 Nginx、LiteLLM 与外部 embedding/RAG client。
- 阶段 D：在 manifest 约束下扩展 custom bundle/expert mode、审计恢复与策略插件化。

## 2026-03-12

### 架构术语与第一阶段计划修正（controller/agent/managed node）

- 本轮澄清并落地了统一术语：
  - `controller` = 控制平面
  - `agent` = 节点执行面
  - `managed node` = 受管节点
  - `controller node` 同时是 `managed node`，应运行 local agent
- 明确系统仅使用 `controller / agent / managed node` 术语，不再保留旧角色命名。

### 为什么 controller 节点也要运行 agent

- 若 controller node 不跑 agent，则本机节点动作会走“特例路径”，与远端节点不一致，导致：
  - 执行路径分裂
  - 测试路径分裂
  - 回归复杂度上升
- 因此本轮将 controller node 明确建模为 managed node，并优先走 agent 协议链路执行节点本地动作。

### 节点局部动作优先 agent 的落地

- 已补齐/统一 agent 任务类型（第一阶段修正版）：
  - `agent.runtime_readiness_check`
  - `agent.runtime_precheck`
  - `agent.port_check`
  - `agent.model_path_check`
  - `agent.resource_snapshot`
  - `agent.docker_inspect`
- controller 新增通用 agent node-local 任务入口：
  - `POST /api/v1/tasks/agent/node-local`
- TaskService 现在可按 node 自动解析在线 agent 并分发节点本地任务。
- ModelService 在容器运行时 reconcile 中优先尝试 `agent.runtime_precheck`，失败时才回退 controller 本地检查路径（fallback/compatibility path）。

### controller 最小本机降级自检边界（已收敛）

- controller 启动时仅保留自举所需自检：
  - 配置读取
  - SQLite 路径与初始化
  - 模型目录与测试日志目录检查
- 启动日志已明确标注为 `controller self-check`，避免与节点执行逻辑混淆。

### 状态反哺与持续 reconcile 同步

- agent 任务上报后，TaskService 会将结果反哺到：
  - model（readiness/observed/health_message/last_reconciled/drift 语义）
  - node（last_seen/status/最近 agent 任务元信息，运行时状态可见性）
- 保持 `desired_state` / `observed_state` / `readiness` 的持续 reconcile 方向，不做大爆炸重构。

### 配置、前端、脚本同步

- 配置层：
  - 新增 `controller` 配置段（`node_id/node_name/node_host/local_agent_expected/local_agent_id`）。
  - `nodes` 归一化统一为 `controller/managed`，移除旧角色兼容分支。
- 前端：
  - 节点卡片新增架构角色提示（controller + managed node）、本机 agent 期望信息。
  - 任务列表新增执行路径展示（`agent` / `controller-direct`）。
- 脚本与编排：
  - `docker-compose.yml` 新增 `local-agent` profile（controller 本机 agent）。
  - `scripts/one-click-up.sh` 新增 `--local-agent`。
  - `scripts/controller_api_smoke.sh` 新增 `agents` 检查，并支持触发 `agent.resource_snapshot` 任务。

### 第一阶段修正版剩余项

- 继续将更多 runtime 细分动作从 controller direct path 下沉为 agent 任务（尤其跨 runtime 的 precheck/conflict 细化）。
- 完善 controller + remote agent 场景下的稳定性回归与故障注入测试。
- 补充外围组件联调闭环（Nginx / LiteLLM / 外部 embedding client）并形成更完整的 agent/runtime 协调矩阵。

## 2026-03-11

### one-click-up 后前端 502 + 一键 E5 测试失败修复（已验证）

- 问题现象
  - 执行 `./scripts/one-click-up.sh` 后，前端出现 `502`。
  - 控制台“一键测试 E5”失败，错误为：
    - `mkdir /opt/controller/test-logs/<run-id>: permission denied`
    - `dial tcp 127.0.0.1:58001: connect: connection refused`

- 根因拆解
  - 历史 `.env` 残留旧路径（`/opt/modelintegrator/...`）与旧 sqlite 文件名，导致 controller 启动异常或数据库只读。
  - 测试日志宿主机挂载目录权限不足，容器内 `app` 用户无法写入。
  - controller 运行在容器内时，`127.0.0.1` 指向容器自身，无法访问宿主机 TEI 端口。
  - runtime `start` 在 `stopped/running` 状态下非幂等，导致一键测试重复触发时任务容易失败。

- 修复内容
  - `scripts/one-click-up.sh`
    - 增加旧路径/旧数据库文件名自动迁移。
    - 增加 sqlite 目录写权限处理（目录 + 文件）。
    - 增加测试日志目录创建、权限设置与可写性探测（失败即中止）。
  - `docker-compose.yml` / `resources/docker/compose.example.env`
    - 增加 `MCP_CONTAINER_HOST_ALIAS`。
    - controller 增加 `extra_hosts`：`host-gateway` 映射（默认 `host.docker.internal`）。
  - `src/pkg/service/model_service.go`
    - 容器 runtime `start` 改为幂等（`stopped/loaded/running/busy/unknown/error` 均可触发）。
    - embedding 模板 readiness 判定改为严格健康检查（含自定义 E5 模板 id）。
  - `src/pkg/service/test_run_service.go` + `src/pkg/service/endpoint_util.go`
    - 在 controller 容器内自动将 loopback endpoint 重写为宿主机别名，避免 `127.0.0.1` 误指向容器自身。
    - `failRun` 补充 `started_at` 兜底，避免前端显示 `0001-01-01`。

- 验证结果
  - `e5_embedding_smoke` 成功：`status=success`，`dim=768`。
  - 测试日志目录正常写入：`./testsystem/logs/<run-id>/`。
  - 前端 502 消失（容器重建切换窗口内短暂抖动除外）。

## 2026-03-10

### 控制平面状态持久化接入（SQLite，最小可用）

- 新增 SQLite 持久化层
  - 新增 `src/pkg/store/sqlite/schema.go`：集中维护 `agents/nodes/models/runtimes` schema 与索引初始化
  - 新增 `src/pkg/store/sqlite/store.go`：基于 `database/sql` + `modernc.org/sqlite` 实现最小 Store
  - 新增 `src/pkg/store/sqlite/store_test.go`：覆盖 agent/node+runtime/model 的基本读写回归

- service 接库改造
  - `src/pkg/service/agent_service.go`
    - 新增 `SetStore`
    - register/heartbeat/capabilities 上报写入 SQLite（upsert）
    - list/get/listByNode 优先读 SQLite，失败回退内存
  - `src/pkg/service/node_service.go`
    - 新增 `SetStore`、`SyncRegistryToStore`
    - 节点查询支持“配置 + SQLite”融合
    - 每次聚合后回写 nodes/runtimes 持久化状态
  - `src/pkg/service/model_service.go`
    - 新增 `SetStore`、`SyncRegistryToStore`
    - 模型列表/详情支持“内存 + SQLite”融合
    - 动作执行后与刷新链路后回写模型状态

- controller 启动 wiring
  - `src/cmd/controller/main.go` 在启动时完成：
    - SQLite 路径准备
    - schema 自动初始化
    - Store 注入 AgentService / NodeService / ModelService
    - 节点与模型配置的幂等同步入库

- 文档增量
  - `README.md` 新增 “SQLite 持久化说明（控制平面状态）” 小节，明确可恢复状态与部署建议

### controller 命名收口（module / import / 构建 / 文案）

- Go module 收口
  - `go.mod` module 已由 `ModelIntegrator` 调整为 `model-control-plane`
  - 全仓 Go import 前缀已统一为 `model-control-plane/...`

- 构建与运行入口收口
  - 主入口统一为 `./src/cmd/controller`
  - agent 入口保持 `./src/cmd/agent`
  - Docker/Compose 默认路径收口到 `/opt/controller`
  - SQLite 默认文件名收口到 `controller.db`

- 前端与文档文案收口
  - 控制台标题统一为 `Controller - Local LLM Control Plane`
  - README 中主名称统一为 `Local LLM Control Plane（Controller）`
  - 示例目录与命令中的旧主命名已同步调整

- 兼容性处理
  - Docker 容器标签已切换为 `com.controller.*`
  - 为兼容历史容器，保留对 `com.modelintegrator.*` 的读取与清理兜底

### agent llmfit managed serve 与健康管理落地（已完成）

- 新增 `src/pkg/fit/managed_serve.go`
  - 提供 llmfit 托管管理器（`ManagedServe`），支持：
    - 托管启动 `llmfit serve`
    - 启动期健康检查（超时失败）
    - 周期健康探测
    - 连续失败阈值触发自动重启
    - 运行状态快照（endpoint/health/pid/last_error/healthy）
  - 停机时由 agent 统一触发进程回收

- 新增 `src/pkg/fit/managed_serve_test.go`
  - 覆盖 llmfit 启动参数构造与 `serve` 参数归一化逻辑

- 改造 `src/cmd/agent/main.go`
  - 增加 llmfit 配置读取与托管启动链路（可通过环境变量启用）
  - 当 llmfit 启动成功时，自动补齐 `fit` 能力与 `fit` runtime 能力
  - 当 llmfit 启动失败时，自动降级移除 `fit` 能力并继续 agent 主流程
  - 在 `register/capabilities/heartbeat` 请求 metadata 中上报 llmfit 管理状态与健康状态

- 新增 agent llmfit 相关环境变量
  - `AGENT_LLMFIT_ENABLED`
  - `AGENT_LLMFIT_BINARY`
  - `AGENT_LLMFIT_ENDPOINT`
  - `AGENT_LLMFIT_HEALTH_PATH`
  - `AGENT_LLMFIT_SERVE_ARGS`
  - `AGENT_LLMFIT_STARTUP_TIMEOUT_SECONDS`
  - `AGENT_LLMFIT_HEALTH_INTERVAL_SECONDS`
  - `AGENT_LLMFIT_HEALTH_TIMEOUT_SECONDS`
  - `AGENT_LLMFIT_FAILURE_THRESHOLD`

- 回归验证
  - `gofmt -w`：通过（Docker Go 工具链）
  - `go test ./...`：通过
  - `go build ./src/cmd/controller`：通过
  - `go build ./src/cmd/agent`：通过

### controller 主入口迁移 + agent 最小链路落地（已完成）

- 入口迁移完成
  - 主入口统一为 `src/cmd/controller/main.go`
  - 删除旧入口文件：`src/cmd/model-integrator/main.go`
  - 删除旧入口目录：`src/cmd/model-integrator`
  - 构建与部署引用已切换为 `controller`：
    - `Dockerfile`
    - `docker-compose.yml`
    - `resources/nginx/nginx.example.conf`

- 后端模型与能力分级落地
  - 在 `src/pkg/model/types.go` 增量补齐：
    - `classification`
    - `capability_tier`
    - `capability_source`
    - `agent_status`
    - runtime 扩展字段：`status/capabilities/actions/last_seen_at`
  - 新增 agent 领域请求/响应模型：
    - `AgentRegisterRequest/Response`
    - `AgentHeartbeatRequest/Response`
    - `AgentCapabilitiesReportRequest/Response`

- capability 规则实现落地（非占位）
  - 新增 `src/pkg/capability/capability.go`
  - 实现最小可运行规则：
    - 节点分类推导（controller/worker/agent-host/hybrid/unknown）
    - 能力分级推导（tier-0/tier-1/tier-2/tier-3）
    - 能力来源推导（static/runtime/agent-reported/merged/unknown）
    - runtime 能力与动作合成

- controller <-> agent 最小链路打通
  - 新增 `src/pkg/service/agent_service.go`（内存注册表）
  - 支持：
    - `Register`
    - `Heartbeat`
    - `ReportCapabilities`
    - `List/GetByID/ListByNodeID/IsOnline`
    - TTL 在线判定（超时离线）
  - `src/pkg/service/node_service.go` 已接入 `AgentService` 并聚合输出节点能力/agent 状态

- API 链路打通
  - `src/pkg/api/router.go` 新增接口：
    - `POST /api/v1/agents/register`
    - `POST /api/v1/agents/{id}/heartbeat`
    - `POST /api/v1/agents/{id}/capabilities`
    - `GET /api/v1/agents`
  - `src/pkg/api/handler.go` 已接入真实服务调用与参数校验，返回统一 JSON 结构

- 前端展示联通
  - `resources/web/app.js` 节点卡片新增展示：
    - `classification`
    - `capability_tier`
    - `capability_source`
    - `agent status`
    - `last_heartbeat_at`
  - runtime 明细新增展示：
    - `status`
    - `capabilities`
    - `actions`
    - `last_seen_at`
  - 空值兜底：`unknown` / `-`

- 兼容与命名收敛
  - 运行主服务命名统一为 `controller`
  - GPU 探针容器命名由 `model-integrator-gpu-probe-*` 调整为 `controller-gpu-probe-*`
  - README/中英文文档中的 compose 服务名描述已同步为 `controller`

- 回归验证结果
  - `gofmt -w`：已执行（通过 Docker 中 Go 工具链）
  - `go test ./...`：通过
  - `go build ./src/cmd/controller`：通过
  - `docker compose config`：通过
  - 前端静态资源引用检查：`index.html` 与 router 路由匹配通过

### v0.2-prep 架构演进定稿（controller + agent + runtime）

- 背景
  - 本条目由项目根目录 `LOG.md` 合并而来，作为当前阶段架构决策记录。

- 1) 为什么从单控制面演进为 `controller + agent`
  - 单控制面已能覆盖基础管理，但对异构节点可控性不一致。
  - managed 节点的本机能力（资源快照、本地目录、本机下载、本机容器）不应长期依赖中心直接操作。
  - 采用中心控制 + 节点执行拆分后，可在不破坏现有控制面的前提下渐进增强。
  - 结论：定稿为“controller + 可选 agent + 外部运行时分级接入”。

- 2) 为什么资源感知不能做统一真相，而要做分级
  - LM Studio、Ollama、vLLM、Docker 容器、纯服务进程的可观测性天然不同。
  - 强行统一会制造“表面一致、实质不可控”的假象，影响调度和运维判断。
  - 结论：采用能力分级并显式暴露边界：
    - `runtime_managed`：以运行时动作结果与反馈为主
    - `service_observed`：以健康和指标观测为主
    - `agent_managed`：具备更强本机执行能力

- 3) 为什么managed 节点 Docker 管理需要 agent
  - 中央控制器不能假设天然可安全稳定地直管所有managed 节点 Docker。
  - 远程直连 Docker 会放大网络暴露、安全边界与运维一致性风险。
  - 结论：需要 Docker 级纳管时，由节点本机 agent 暴露能力并执行动作，controller 负责策略与编排。

- 4) 为什么 agent 必须支持原生 + 容器双形态
  - 仅容器部署会排斥无 Docker/Podman 节点。
  - 仅原生部署又无法充分利用 Linux 容器化运维能力。
  - 结论：双形态是覆盖 macOS/Windows/Linux 异构节点的必要条件：
    - 原生二进制优先
    - 容器版可选

- 5) 为什么前端从“单层节点列表”演进为“节点 + runtime + capability”
  - 混合节点常态下，节点层“在线/离线”不足以表达可执行能力。
  - 前端必须显示 runtime 级能力，避免误导为“所有节点能力等价”。
  - 目标展示模型：
    - 节点卡片：分类、agent 状态、能力来源、心跳
    - 节点内 runtime 列表：类型、endpoint、能力、动作
    - 分级能力文案：观测级/运行时反馈级/增强纳管级

- 6) 为什么混合节点必须视为常态
  - 生产与实验环境通常并存 Docker、LM Studio、Ollama、vLLM 等多运行时。
  - 节点是宿主抽象，runtime 才是能力单元。
  - 结论：调度目标应尽量落在“节点上的 runtime”，而非仅落在抽象节点。

- 7) 为什么 llmfit 采用“agent 托管二进制 + 本地 HTTP 调用”
  - 源码级嵌入/FFI 会显著提高构建复杂度与跨平台维护成本。
  - llmfit 已具备可复用的进程/服务能力，最稳妥方案是托管进程 + 本地调用。
  - 定稿方案：
    - agent 附带 llmfit 二进制
    - agent 优先托管 `llmfit serve`
    - agent 通过 loopback HTTP 调用
    - agent 退出时主动回收子进程
    - 必要时保留 CLI fallback

- 8) 下一阶段方向与未完成事项
  - 结构上保持单主入口并新增 agent 入口：
    - `src/cmd/controller`
    - `src/cmd/agent`
    - 删除旧入口 `src/cmd/model-integrator`
  - 将节点分类与能力分级写入 API 模型与前端数据结构。
  - 建立 controller<->agent 最小注册、心跳、能力上报链路。
  - 在 agent 落地 llmfit managed serve 与健康管理。
  - 当前明确不做：完整 agent、复杂分布式通信、完整下载分发、复杂资源估算引擎。

### v0.2-prep 管理边界原则与实现概述

- 管理边界原则（后续迭代约束）
  - 系统仅管理“当前系统模型清单中的受管模型容器”。
  - 不干涉已有、已启动的外部容器（无论是否运行模型）。
  - 若发现同名外部容器，系统应拒绝接管并返回冲突错误，而非停止/删除该容器。

- 当前实现概述（截至本次修复）
  - 控制面：Go 后端 + REST API + Web 控制台（Runtime/Download 页签）
  - Runtime 模型控制：支持 `load/unload/start/stop`，并区分 `lmstudio` 与 `docker/portainer` 动作矩阵
  - 容器后端：Docker/Portainer 适配器已接入真实容器编排 API，支持容器创建、启动、停止、卸载与状态查询
  - 模型来源：支持本地模型目录扫描（`resources/models`）与 LM Studio 刷新
  - 模板体系：支持 builtin/config/custom Runtime Template 校验与注册，并在容器动作前执行模板解析与校验
  - 停机回收：`one-click-down` 会清理系统受管模型容器并下线控制面

- 下一阶段开发共识
  - 优先推进 SQLite 持久化完整落地（模型状态、模板、动作记录、重启恢复）

### v0.2-prep Docker 模型动作状态机修复

- Docker/Portainer 模型按钮状态矩阵修复（`resources/web/app.js`）
  - `stopped`: 仅 `load` 可用
  - `loaded`: `unload/start` 可用
  - `running`: `unload/stop` 可用
  - `busy`: `unload/stop` 可用
  - 其余动作按上述矩阵严格禁用，避免 `load/start` 错误同步
  - 模型列表状态文案调整：容器后端 `stopped` 在 UI 中显示为 `unload`，页面主状态统一为 `unload/loaded/running`

- 后端动作状态校验新增（`src/pkg/service/model_service.go`）
  - 在 `ModelService.executeAction` 中新增 `docker/portainer` 严格状态校验
  - 非法状态动作直接返回失败，不再调用适配器，防止通过 API 绕过前端限制
  - `running/busy` 状态下执行 `unload` 时，先执行 `stop`，成功后再执行真实 `unload`
  - 容器后端动作语义保持：`load->loaded`、`start->running`、`stop->loaded`、`unload->stopped`
  - 新增容器状态对账刷新：以运行时实际状态同步模型状态（`exists=false -> stopped`，`exists=true/running=false -> loaded`，`running=true -> running`）

- 单测补充与更新（`src/pkg/service/model_service_test.go`）
  - `load` 测试更新为容器后端统一进入 `loaded`
  - 新增容器动作状态矩阵测试
  - 新增“`stopped` 状态下 `start` 被拒绝且不调用适配器”测试
  - 新增“容器状态对账刷新”测试，覆盖 `running->loaded`、`missing->stopped` 与容器元信息清理

- Docker/Portainer 适配器管理边界增强（`src/pkg/adapter/dockerctl/adapter.go`）
  - 增加容器归属校验：仅操作带 `com.modelintegrator.managed=true` 且 `model_id` 匹配当前模型的容器
  - 对同名非受管容器不再停止/删除，改为拒绝接管并返回冲突错误

## 2026-03-09

### v0.2-prep 前端页签分隔与模型元信息展示优化

- Runtime 页面页签视觉分隔优化（`resources/web/app.css`）
  - 顶层页签 `Runtime / Download` 增加竖向分隔线
  - Runtime 子页签 `List / Template` 增加竖向分隔线
  - 顶层页签与 Runtime 子页签之间增加层级分隔线，提升结构可读性

- 模型元信息展示规则调整（`resources/web/app.js`）
  - `backend_type=lmstudio` 的模型在列表元信息中不再显示“模板”字段
  - Docker/Portainer 等容器后端模型保留“模板”展示

### v0.2-prep 基础设施改造

- 新增 GPU/CUDA/Driver 启动前预检查（`src/pkg/preflight/gpu.go`）
  - 若检测到 `nvidia-smi`，输出 Driver/CUDA 版本
  - 非 CUDA 平台或检测失败时输出 warning，不阻断启动

- 新增 SQLite 路径配置与启动预备（`src/pkg/storage/sqlite.go`）
  - 默认路径：`./resources/config/modelintegrator.db`
  - 支持环境变量覆盖：`MCP_SQLITE_PATH`
  - 启动时自动创建目录与文件（占位）

- 扩展 docker compose 栈，新增服务：
  - `nginx`（外层唯一入口）
  - `portainer`
  - `nginx-ui`
  - `litellm`
  - `openwebui`
  - `controller`
  - 其中 `portainer/nginx-ui/litellm/openwebui` 归入 `addons` profile，按需启动

- 网络与端口策略：
  - 所有服务同一网络 `mcp_net`
  - 仅 `nginx` 暴露宿主端口
  - 默认外部端口修改为 `59081`

- 路径策略：
  - 所有挂载路径基于 `docker-compose.yml` 所在目录
  - 通过 `MCP_PROJECT_DIR` 统一路径前缀

- Nginx 网关配置更新（`resources/nginx/nginx.example.conf`）
  - `/` -> `controller`
  - `/openwebui/` -> `openwebui`
  - `/litellm/` -> `litellm`

- 文档更新：
  - `README.zh-CN.md`
  - `resources/docker/compose.example.env`
  - `resources/config/config.example.yaml`

### v0.2-prep 文档与 LM Studio 稳定性修复

- LM Studio 适配器增强（`src/pkg/adapter/lmstudio/adapter.go`）
  - 在 `load/unload/start/stop` 前先调用 `/v1/models` 校验目标模型名称
  - 优先按配置中的 `name` 匹配，次级按 `id` 匹配
  - 未匹配时返回可用模型列表，避免直接调用动作接口导致报错
  - 新增可选缓存：内存缓存 + goroutine 定时刷新（配置开关与刷新间隔）

- README 体系重构
  - 新增 `README.md` 作为主文档（中文）
  - 新增 `README.en.md` 英文版本
  - 主 README 增加英文相对路径跳转链接
  - 从 0 部署步骤（含 `/tank/docker_data/model_intergrator`）已同步写入 README

- Web UI 重构
  - 标题文案由“占位页”调整为正式控制台文案
  - 调整为左右双栏布局：节点列表在左、模型列表在右
  - 模型列表新增节点标签页，默认展示第一个节点下模型

- 配置与启动增强
  - 新增模型目录配置：`storage.model_root_dir` / `MCP_MODEL_ROOT_DIR`
  - compose 支持模型目录挂载：`MCP_MODEL_DIR_HOST -> MCP_MODEL_ROOT_DIR`
  - 新增一键脚本：`scripts/one-click-up.sh` / `scripts/one-click-down.sh`

- 模型刷新能力增强
  - `ModelService` 新增刷新逻辑：服务启动后后台定时刷新模型列表
  - `GET /api/v1/models` 返回前会触发刷新（至少保证 LM Studio 侧不是 mock 静态值）

### v0.2-prep LM Studio API 兼容与状态一致性

- LM Studio 列表接口兼容增强（`src/pkg/adapter/lmstudio/adapter.go`）
  - 查询模型列表优先使用 `GET /api/v1/models`，并兼容回退到 `GET /v1/models`
  - 兼容解析 `models[] / data[] / 直接数组` 三种响应包裹
  - 兼容 `key/display_name` 与 `id/name` 字段映射
  - 识别 `loaded_instances` 并映射为模型状态 `loaded/stopped`

- LM Studio 动作接口兼容增强（`src/pkg/adapter/lmstudio/adapter.go`）
  - `load` 优先使用 `POST /api/v1/models/load`，失败回退 `POST /v1/models/load`
  - `unload` 优先使用 `POST /api/v1/models/unload`，失败回退 `POST /v1/models/unload`
  - 若模型列表中存在 `loaded_instances[].id`，`unload` 优先按 `instance_id` 调用（兼容新版本 LM Studio）
  - 动作结果 `detail.path` 会返回实际命中的下游路径，便于排障
  - 模型匹配策略调整为优先使用 LM Studio 模型 `id/key` 作为动作入参

- LM Studio 缓存与刷新稳定性增强
  - `load/unload` 成功后会触发异步缓存刷新
  - 列表刷新失败时会优先回退已缓存模型，降低瞬时波动影响
  - 避免远端返回 `unknown` 时覆盖本地已知状态

- LM Studio 适配器新增测试（`src/pkg/adapter/lmstudio`）
  - 新增 `action_path_test.go`：覆盖 `/api/v1` 优先、回退、`instance_id` 卸载等关键路径
  - 补充 `adapter_test.go`：覆盖 loaded 状态解析（含 `loaded_instances`）

### v0.2-prep 节点硬件探测与平台信息增强

- GPU 探测策略升级（`src/pkg/preflight/gpu.go`）
  - 优先本地 `nvidia-smi` 探测
  - 本地探测失败时，支持通过 Docker endpoint 启动探针容器回退探测 GPU/CUDA/Driver
  - 新增探针镜像环境变量：`MCP_GPU_PROBE_IMAGE`（默认 `nvidia/cuda:12.4.1-base-ubuntu22.04`）

- 节点平台信息填充增强（`src/cmd/controller/main.go`）
  - 对所有启用 `docker` runtime 的节点填充平台信息（不再仅限controller 节点）
  - Docker 探测 endpoint 优先取 `nodes[].runtimes[]` 中启用的 docker endpoint

- 容器运行权限调整（`Dockerfile`）
  - 移除 `USER app`，默认以 root 运行，确保容器内可访问 `/var/run/docker.sock` 进行 Docker 探测

### v0.2-prep Web 控制台状态同步与交互优化

- 节点列表与模型状态联动（`resources/web/app.js`）
  - 节点卡片新增“已装载模型数”统计（按节点实时计算）
  - 节点卡片新增 Runtime 状态摘要（按 backend 聚合 loaded 数）
  - 节点标签页新增已装载计数展示（每节点已装载计数）

- 模型动作按钮行为优化（`resources/web/app.js`）
  - `backend_type=lmstudio` 的模型仅展示 `load/unload`
  - 其他后端保持 `load/unload/start/stop`
  - 动作后同步刷新节点列表、标签页和模型列表，保持统计一致
  - 前端保留节点级动作锁；后端也已补充节点级并发动作锁，避免绕过前端并发提交

- 节点展示信息微调
  - 节点卡片移除角色显示（`controller/managed`），保留名称与描述区分

### v0.2-prep 路线计划评估与阶段落地

- 计划 1：Docker/Portainer 适配器先以 NVIDIA GPU 为基础，后续支持其他硬件
  - 合理性判断：合理。先收敛 NVIDIA 可降低早期实现复杂度，且当前 GPU 探测链路已以 CUDA/NVIDIA 为主。
  - 本次落地：记录为阶段路线，继续保持“先 NVIDIA，后扩展 AMD/ROCm 等平台”。
  - 当前状态：Docker/Portainer 业务适配器仍为 placeholder，尚未进入真实容器编排控制逻辑。

- 计划 2：实现 SQLite 持久化读写
  - 合理性判断：合理且必要。当前运行态主要在内存中，缺少重启恢复能力。
  - 本次落地：记录为优先级较高的下一阶段工作；本轮未引入持久化读写 schema 与 DAO。
  - 当前状态：已具备 SQLite 路径准备与文件创建，未接入模型/节点状态持久化。

- 计划 3：`resource` 目录改为 `resources`，并修复 `config.example.yaml` 中 `models`
  - 合理性判断：合理。统一命名可降低误用概率；`models` 空数组比空条目更安全。
  - 本次落地（已完成）：
    - 项目目录从 `resource` 迁移为 `resources`。
    - 代码、compose、脚本、配置、README、LOG 中路径引用已同步迁移。
    - `resources/config/config.example.yaml` 已修复为 `models: []`。

- 计划 4：增加模型下载容器与前端 Download 页签
  - 合理性判断：合理。下载能力和运行能力拆分有助于后续扩展。
  - 本次落地（阶段性完成）：
    - compose 新增 `download` profile，加入下载容器：
      - `hf-downloader`（Hugging Face CLI 环境容器）
      - `aria2-downloader`（通用下载器）
    - 一键脚本支持 `--download` 参数（可与 `--addons` 组合）。
    - 前端新增顶层页签：`Runtime` / `Download`。
      - `Runtime` 页签承载现有节点与模型控制台。
      - `Download` 页签当前为空白预留页，用于后续下载任务面板。
  - 使用约束（当前阶段）：
    - 下载功能仅面向“controller 节点且具备 docker 能力”的部署场景。
    - 若后续扩展到其他运行平台，再评估跨平台下载能力与 UI 适配策略。

### v0.2-prep vLLM 下载兼容与运行模板补充

- 对现有下载容器能力补充结论
  - `hf-downloader` 可用于下载 vLLM 常用的 Hugging Face 模型权重（如 safetensors）。
  - `aria2-downloader` 可用于直链下载模型文件分片或镜像文件。
  - 结论：下载链路可覆盖 vLLM 模型准备场景，但此前缺少 vLLM 运行模板。

- 新增 vLLM 运行模板（`docker-compose.yml`）
  - 新增 `vllm-runtime` 服务（`profile=vllm`）。
  - 默认参数支持通过环境变量配置模型、服务名、端口、显存占用、最大上下文。
  - 复用 `resources/models` 与 HF 缓存目录，便于与下载容器协同。
  - 当前定位：面向“controller 节点 + docker + NVIDIA”场景的运行模板，为后续按模型实例化编排做准备。

- 一键脚本与配置示例同步
  - `scripts/one-click-up.sh` 新增 `--vllm` 参数。
  - `resources/docker/compose.example.env` 新增 vLLM 相关环境变量示例。

- 文档同步
  - `README.md` / `README.zh-CN.md` / `README.en.md` 新增 vLLM 支持说明与启动示例。

### v0.2-prep 运行时模板可扩展机制（开发计划与落地）

- 合理性判断
  - 该方案合理，且是后续支持 vLLM/ollama/vLLM 多实例编排的必要基础。
  - 将“下载能力”和“运行模板能力”解耦，可避免强绑定某一后端实现。

- 开发计划（本轮执行）
  - 1) 定义统一 Runtime Template 数据模型，支持 builtin/config/custom 来源。
  - 2) 实现后端模板注册表与校验服务（先支持 docker/portainer 模板字段校验）。
  - 3) 提供模板 API：列举、校验、注册。
  - 4) 将模板校验接入模型动作前置检查，确保模板可用后再执行 docker/portainer 动作。
  - 5) 升级前端 Download 页签为模板管理页（列表 + 校验 + 注册）。
  - 6) 文档同步与回归测试。

- 本次落地结果
  - 新增类型定义：
    - `model.RuntimeTemplate`
    - `model.RuntimeTemplateValidationResult`
  - 新增模板注册表与服务：
    - `registry.RuntimeTemplateRegistry`
    - `service.RuntimeTemplateService`
    - 内置模板：`docker-generic`、`vllm-openai`
  - 新增 API：
    - `GET /api/v1/runtime-templates`
    - `POST /api/v1/runtime-templates/validate`
    - `POST /api/v1/runtime-templates`
  - 配置支持：
    - `config.runtime_templates`（启动时执行校验，失败则拒绝启动）
  - 模型动作前置模板校验：
    - Docker/Portainer 模型会解析 `metadata.runtime_template_id`
    - 本地扫描模型默认绑定 `docker-generic`
  - 前端 Download 页：
    - 已支持模板列表展示、模板参数填写、校验与注册调用
  - 测试：
    - 新增 `runtime_template_service_test.go` 覆盖模板校验与注册路径

### v0.2-prep Runtime 子页签重排与 Docker/Portainer 编排落地

- 需求评估
  - 将 Runtime Template 从 Download 顶层页签迁回 Runtime 内部子页签是合理的，符合“运行态能力聚合”原则。
  - Download 顶层页签保留为空白预留页，便于后续独立承载下载任务编排。
  - Docker/Portainer 适配器从 placeholder 升级为真实编排调用是必要改造，可直接打通模型容器生命周期。

- 前端重排（`resources/web/index.html` / `app.js` / `app.css`）
  - Runtime 顶层页新增子页签：
    - `List`：保留原节点列表 + 模型列表
    - `Template`：承载 Runtime Template 列表、校验、注册
  - Download 顶层页签恢复为空白预留页（仅说明文案）。

- Docker/Portainer 适配器落地（`src/pkg/adapter/dockerctl/adapter.go`）
  - 接入 Docker Engine API（支持 `unix://`、`http(s)://`、`tcp://` endpoint）。
  - Portainer 支持：
    - 若 endpoint 已是 `/api/endpoints/{id}/docker` 代理路径，直接调用。
    - 若为 Portainer 根地址，自动查询 `/api/endpoints` 选择首个 endpoint 并切换到 Docker Proxy 调用。
  - 实现真实容器生命周期动作：
    - `load`：确保容器存在（必要时创建/拉镜像）
    - `start`：启动容器
    - `stop`：停止容器
    - `unload`：停止并移除容器
    - `status`：查询容器运行状态
  - 创建容器时写入管理标签（model id/runtime id/template id）并支持端口/卷/env/GPU 设备请求映射。

- 模型动作与模板绑定强化（`src/pkg/service/model_service.go`）
  - 动作前将模板序列化写入 `metadata.runtime_template_payload`，供适配器执行使用。
  - 动作后回填容器信息（如 `runtime_container_id`）到模型 metadata，保持状态连续性。

- 回归验证
  - `go test ./...` 通过
  - `go build ./src/cmd/controller` 通过
  - `docker compose config`（含 `download` / `vllm` profile）通过
  - `bash -n scripts/one-click-up.sh scripts/one-click-down.sh` 通过
