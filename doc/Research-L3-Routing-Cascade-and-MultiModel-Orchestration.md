# Research Layer 3：应用层路由、级联与多模型协作

## 1. 这一层解决的问题

Layer 3 聚焦“应用层任务如何做模型选择与协作编排”：

- query 到模型的质量/成本路由
- 多模型级联（cascade）
- 多模型协作生成与聚合
- 复杂任务的阶段化工作流编排

这一层不替代当前 `controller`，而是未来建立在 `controller + agent + RuntimeInstance` 之上的 application planner / workflow orchestrator。

## 2. 关键参考项目 / 论文

### 2.1 FrugalGPT

- 一句话定位：通过 prompt adaptation / approximation / cascade 进行成本-效果优化的路线。
- 关键机制：用不同模型组合降低成本并保持或提升效果；不是所有 query 都需要最强模型。
- 与当前项目关联：可作为应用层任务分流和级联策略的思想来源。

### 2.2 RouteLLM

- 一句话定位：在强模型与弱模型之间做质量/成本权衡路由。
- 关键机制：使用 router 学习 query 应该走哪个模型。
- 与当前项目关联：可作为未来 application planner 的“路由器”能力原型方向。

### 2.3 Mixture-of-Agents（MoA）

- 一句话定位：通过多模型协作提高生成质量的协作范式。
- 关键机制：多个模型先各自输出，再由下一层聚合/继续生成。
- 与当前项目关联：可映射到多阶段工作流中的“并行候选 + 聚合”编排单元。

## 3. 值得借鉴的能力

- 明确把“任务分解 + 模型选择 + 成本约束 + 结果聚合”作为上层 planner 责任。
- 让不同强度模型形成成本分层，不默认所有请求走最高成本路径。
- 在工作流中支持“并行候选 + 质量评估 + 聚合继续生成”链路。

## 4. 不应直接照搬的能力

- FrugalGPT/RouteLLM/MoA 多为应用层路由思想，不能替代 Layer 1/2 的底层装卸与调度内核。
- 当前项目尚未进入 application planner 阶段，不应把 Layer 3 写成既有功能。
- 不能把单一研究在单指标上的最优结果，直接等价为本项目端到端最优策略。

## 5. 与当前项目对象模型映射

- 未来 `application planner / workflow orchestrator`：Layer 3 主承载。
- `controller`：继续作为底层控制与资源编排基座，提供可调用的实例与任务控制接口。
- `agent`：继续承担节点执行面，不承担应用层决策。
- `RuntimeInstance`：作为 planner 选择执行目标与回收结果的运行态对象。
- `RuntimeBinding/RuntimeTemplate`：为 planner 提供“可选模型运行形态”目录与约束边界。

## 6. 对当前项目的增强建议

### 近期（1~2 个版本内）

- 在文档与接口层保留 planner 扩展点，不改变当前 `controller` 主职责。
- 在 `Enhancement-Roadmap` 中正式纳入阶段 E（应用层任务调度器）作为长期目标。

### 中期

- 在阶段 C/D 稳定后，定义 planner 所需的最小输入输出协议：
  - 输入：任务目标、成本约束、质量目标。
  - 输出：模型路由决策、阶段执行计划、聚合策略。

### 长期

- 进入阶段 E：实现 heterogeneous orchestration / workflow planner。
- 将 FrugalGPT / RouteLLM / MoA 思路转化为系统可配置策略，而非硬编码路径。

## 7. 边界说明（避免误解）

- Layer 3 为明确长期方向，当前仓库未开始完整实现。
- 当前优先级仍是 Layer 1/2 的执行与调度基础夯实。
