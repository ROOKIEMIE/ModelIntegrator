# Enhancement Roadmap：三层增强路线与阶段计划（A/B/C/D/E）

## 1. 背景与目标

当前项目已完成阶段 0（运行对象模型重构），阶段 A 已推进到 instance-first 与 local-agent-first。  
本轮路线文档目标不是重复功能说明，而是建立以下正式映射：

- 外部成熟成果（分层）
- 当前项目对象与能力边界
- 后续阶段 A/B/C/D/E 的增强落点

## 2. 三层增强视图

1. Layer 1：模型服务与动态装卸层  
2. Layer 2：资源调度与 GPU 复用层  
3. Layer 3：应用层任务路由与多模型协作层

分层研究文档：

- [`./Research-L1-Serving-and-Dynamic-Loading.md`](./Research-L1-Serving-and-Dynamic-Loading.md)
- [`./Research-L2-Scheduling-ColdStart-and-GPU-Pooling.md`](./Research-L2-Scheduling-ColdStart-and-GPU-Pooling.md)
- [`./Research-L3-Routing-Cascade-and-MultiModel-Orchestration.md`](./Research-L3-Routing-Cascade-and-MultiModel-Orchestration.md)

## 3. 外部成果 -> 当前项目对象映射

| 外部成果 | 当前项目主要映射对象 | 当前状态 |
| --- | --- | --- |
| Triton（explicit load/unload） | `controller` runtime 动作原语、`RuntimeInstance` 状态 | 已有基础动作链路，待做原语化与策略化 |
| ModelMesh（缓存/LRU/预测加载） | `RuntimeBinding` / `RuntimeInstance` 缓存与淘汰策略位 | 未实现，列入阶段 B/C 深化 |
| Ray Serve Multiplexing | `RuntimeInstance` 多模型复用、`agent` 执行回传 | 未实现，列入中期增强 |
| NVIDIA Dynamo（resource planner/router） | `controller` 资源规划与路由接口 | 未实现，作为 Layer 2 规划基座 |
| Clipper | `controller` 调度目标与低延迟基线 | 仅作为策略参考 |
| INFaaS | 模型/硬件/优化联合选择（controller 策略层） | 未实现，阶段 B/C 参考 |
| MuxServe | 多模型并发与 prefill/decode 复用方向 | 未实现，阶段 C/D 参考 |
| ServerlessLLM | 冷启动成本建模与加载路径优化 | 未实现，阶段 B/C 参考 |
| Aegaeon | GPU pooling 与 autoscaling 方向 | 未实现，阶段 C/D 参考 |
| FrugalGPT / RouteLLM / MoA | 未来 `application planner / workflow orchestrator` | 阶段 E 长期目标，当前未开始实现 |

## 4. 阶段路线（A/B/C/D/E）

### 4.1 阶段 A：Agent 节点执行面做实（衔接 Layer 1/2）

- 目标：把节点执行与状态事实稳定收口到 `RuntimeInstance`，夯实 local-agent-first 主路径。
- 当前状态：已进入并推进中。
- 关联增强层：Layer 1 为主，Layer 2 基础事实回传。

### 4.2 阶段 B：Controller 编排内核做深（重点进入 Layer 2）

- 目标：instance-first reconcile 主循环、precheck/conflict/drift 深化、load/unload planner。
- 关键映射：INFaaS / ServerlessLLM / Clipper 的调度与冷启动思路。
- 产出方向：资源与动作策略从“可执行”升级到“可优化”。

### 4.3 阶段 C：外围组件真实联调（Layer 1/2 系统联通）

- 目标：Nginx -> LiteLLM -> 外部 embedding/RAG 联调稳定化。
- 关键映射：Layer 1 装卸能力与 Layer 2 调度策略联动，验证端到端链路。
- 产出方向：从单点能力走向系统联通与真实流量验证。

### 4.4 阶段 D：产品化与专家模式（Layer 1/2 完善 + custom bundle）

- 目标：产品化体验完善、更多模型类型、`custom_bundle` 在 manifest 约束下演进。
- 关键映射：多模型并发、GPU 复用策略逐步产品化落地。
- 产出方向：可运维、可治理、可扩展的控制平面。

### 4.5 阶段 E：应用层任务调度器（进入 Layer 3）

- 目标：构建 application planner / heterogeneous orchestration / workflow planner。
- 定位：阶段 E 不是替代当前 `controller`；它建立在 `controller + agent + RuntimeInstance` 之上。
- 职责：复杂任务拆解、模型选择、阶段编排、结果合并。
- 参考思想：FrugalGPT / RouteLLM / MoA。
- 边界：阶段 E 是长期目标，不属于当前已实现范围。

## 5. 已实现能力与后续借鉴边界

当前已实现（阶段 0 + 阶段 A 已推进）：

- 运行对象模型拆分：`Model/RuntimeTemplate/RuntimeBinding/RuntimeInstance/RuntimeBundleManifest`
- agent 任务 instance-first 输入与状态归属收口
- local-agent-first 与 testsystem smoke/log 归档链路

当前计划借鉴（阶段 B/C/D）：

- load/unload 原语化
- 冷启动与调度成本建模
- 多模型复用、缓存、并发资源策略

长期进入（阶段 E）：

- 应用层任务路由、级联、多模型协作编排

## 6. 维护约定

- 每次阶段目标变化，先更新本路线图，再在 `doc/LOG.md` 记录变更理由。
- 研究细节变化只改 Layer 文档；`Schema.md` 只维护系统定位与阶段对应关系。
