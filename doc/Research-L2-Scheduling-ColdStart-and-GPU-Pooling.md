# Research Layer 2：资源调度、冷启动与 GPU 池化

## 1. 这一层解决的问题

Layer 2 聚焦“模型已经可运行后，如何做更高效的资源调度与容量利用”：

- 资源调度与多模型并发 serving
- 冷启动成本控制
- GPU 复用与池化
- query-level 与系统级调度协同

对当前项目而言，Layer 2 主要落在 `controller` 编排内核深化（阶段 B）与 `agent` 节点执行面的协同。

## 2. 关键参考项目 / 论文

### 2.1 Clipper

- 一句话定位：经典在线推理系统基线。
- 关键机制：自适应模型选择、缓存、批处理、低延迟 serving。
- 与当前项目关联：可作为“调度与延迟目标并存”的基准参照。

### 2.2 INFaaS

- 一句话定位：Automated Model-less Inference Serving 路线。
- 关键机制：自动选择模型、硬件、优化变体；兼顾 query-level 选择与 autoscaling。
- 与当前项目关联：可借鉴“模型/硬件/优化联合选择”思想，进入 controller 策略层。

### 2.3 MuxServe

- 一句话定位：多个 LLM 同时服务的并发复用路径。
- 关键机制：空间-时间复用；围绕 prefill / decode 特征做多模型并发资源复用。
- 与当前项目关联：可映射到 `RuntimeInstance` 级并发调度策略与资源占用控制。

### 2.4 ServerlessLLM

- 一句话定位：面向 LLM 冷启动问题的系统路线。
- 关键机制：通过加载路径、存储层级、迁移，降低“未加载 -> 可服务”成本。
- 与当前项目关联：可作为 load/unload planner 的冷启动成本建模参考。

### 2.5 Aegaeon

- 一句话定位：多模型并发场景下的有效 GPU pooling 路径。
- 关键机制：effective GPU pooling、token-granularity autoscaling、goodput 提升。
- 与当前项目关联：可为阶段 B/C 后续的 GPU 池化指标与 autoscaling 策略提供方向。

## 3. 值得借鉴的能力

- 在 `controller` 里把调度目标从“动作完成”扩展到“延迟/吞吐/goodput 权衡”。
- 为冷启动建立显式成本面（加载路径、迁移代价、缓存命中）。
- 为 `RuntimeInstance` 建立并发与资源占用观测字段，支持后续调度反馈闭环。
- 在 `testsystem` 中引入并发与冷启动 smoke，避免只验证单模型 happy path。

## 4. 不应直接照搬的能力

- INFaaS/MuxServe/Aegaeon 多以更大规模资源池和复杂负载为前提，不能直接替换当前控制面实现。
- token-granularity autoscaling 对当前阶段可能过重，应先做可观测与基础策略。
- Clipper 等基线强调端到端低延迟，但当前项目仍需优先完成 instance-first 编排闭环。

## 5. 与当前项目对象模型映射

- `controller`：Layer 2 的核心承载，负责 reconcile/precheck/conflict/load-unload planner 演进。
- `RuntimeInstance`：承载资源占用、冷启动状态、并发事实与策略输入。
- `RuntimeBinding`：承载硬件偏好、节点选择、优化变体选择约束（通过 binding 约束表达）。
- `agent`：执行节点本地动作并回传可调度事实（容器状态、资源快照、就绪度）。
- `testsystem`：验证调度策略是否在真实链路上成立（冷启动、并发、回退）。

## 6. 对当前项目的增强建议

### 近期（1~2 个版本内）

- 阶段 B 先把 `controller` 的 instance-first reconcile 做深：precheck/conflict/drift 与 load/unload planner。
- 扩展 `RuntimeInstance` summary，沉淀冷启动与最近调度动作摘要字段。
- 在 testsystem 增加“并发任务 + 状态收口”场景，不只验证单条任务成功。

### 中期

- 引入基础资源池化策略（先节点级，再 runtime 级），支持多实例并发与冲突仲裁。
- 引入冷启动优化策略位（预加载、延迟卸载、迁移优先级）并做灰度验证。
- 形成最小 autoscaling 原型（以实例/节点粒度为主）。

### 长期

- 演进到更细粒度的 GPU pooling 与多模型并发调度策略。
- 将 Layer 2 调度内核作为 Layer 3 application planner 的资源执行后端。

## 7. 边界说明（避免误解）

- 当前项目“已具备 Layer 2 基础面”：instance-first 对象、agent 事实回传、smoke 验证链路。
- 当前项目“尚未完成 Layer 2 内核”：系统化资源调度、冷启动优化、GPU 池化仍在后续阶段。
