# Research Layer 1：Serving 与动态装卸（load/unload）

## 1. 这一层解决的问题

Layer 1 聚焦“模型如何被正式装载、卸载、复用和承载”：

- serving 基础能力（模型可服务）
- dynamic load/unload 控制原语
- 多模型共享 runtime 的基础机制
- 运行态对象与执行面的清晰边界

对当前项目而言，Layer 1 是 `RuntimeTemplate/RuntimeBinding/RuntimeInstance` 建模后最直接的增强层。

## 2. 关键参考项目 / 论文

### 2.1 NVIDIA Triton Inference Server

- 一句话定位：成熟推理服务系统，强调显式模型控制。
- 关键机制：explicit model control、支持模型 `load / unload`。
- 与当前项目关联：可借鉴为 `controller` 的正式控制原语，而非仅靠容器启停隐式表达。

### 2.2 KServe ModelMesh

- 一句话定位：面向高密度多模型 serving 的模型承载层。
- 关键机制：模型缓存、LRU eviction、predictive loading、动态加载/卸载、资源共享与高密度 packing。
- 与当前项目关联：可映射为 `RuntimeInstance` 级别的多模型缓存与装卸策略基础。

### 2.3 Ray Serve Model Multiplexing

- 一句话定位：一组 replicas 服务多个模型的 multiplexing 路径。
- 关键机制：按请求路由到目标模型；replica 内模型数量超限时按 LRU 卸载。
- 与当前项目关联：可借鉴“多模型池化 + 热模型缓存 + 超限淘汰”机制，连接 `agent` 执行面与 `controller` 策略面。

### 2.4 NVIDIA Dynamo

- 一句话定位：偏资源调度系统的推理基础设施组件，而非单一模型服务器。
- 关键机制：GPU Resource Planner、Smart Router / KV-aware routing、动态 GPU 调度。
- 与当前项目关联：Layer 1 可先吸收其“资源规划原语”思想，为 Layer 2 调度内核预留接口。

## 3. 值得借鉴的能力

- 把 `load/unload` 升级为可审计的正式控制原语（参考 Triton）。
- 在 `RuntimeInstance` 上引入“热模型缓存 + LRU 淘汰”策略接口（参考 ModelMesh / Ray Serve）。
- 在 `RuntimeBinding` 上显式表达多模型复用约束，避免 runtime 行为隐式化。
- 为 `controller` 预留资源规划输入输出结构（参考 Dynamo 的 planner/router 思想）。

## 4. 不应直接照搬的能力

- ModelMesh/Ray Serve/Dynamo 面向大规模多节点/多 GPU 场景，不能直接等价到当前 homelab 或单机环境。
- Dynamo 更偏资源调度系统，不应被简化为“替代当前 runtime server”的单组件改造。
- 不能把外部系统默认机制直接写进当前实现结论，必须通过 `RuntimeBundleManifest` 和对象约束落地。

## 5. 与当前项目对象模型映射

- `RuntimeTemplate`：承载“运行环境能力边界”，决定可支持的 load/unload 机制范围。
- `RuntimeBinding`：承载“模型进入某类 runtime 的策略参数”，适合落多模型池化与缓存策略位。
- `RuntimeInstance`：承载“运行态事实”，适合挂载热度、缓存命中、装卸状态等观测字段。
- `RuntimeBundleManifest`：约束 custom/expert 模式下可接受的注入与端口/健康检查契约。
- `controller`：负责将 load/unload 从动作变成编排原语。
- `agent`：负责节点本地执行与事实回传，是 Layer 1 执行面的第一落点。

## 6. 对当前项目的增强建议

### 近期（1~2 个版本内）

- 在 `controller` 的 runtime 任务编排中显式区分 `load/unload` 与 `start/stop`。
- 在 `RuntimeInstance` 摘要视图中补充可观测字段（装载状态、最近装卸动作摘要）。
- 在 `testsystem` 增加“实例级 load->readiness->unload”smoke 验证链路。

### 中期

- 引入 `RuntimeInstance` 级缓存策略位（如 LRU/热模型上限），先在本地单节点闭环。
- 在 `RuntimeBinding` 约束中加入“多模型复用与隔离策略”声明。
- 把预测性加载作为可选策略输入，而非默认行为。

### 长期

- 为 Layer 2 的资源调度内核提供标准化输入：模型热度、装卸成本、可复用 runtime 容量。
- 与未来 application planner 对齐，使上层任务编排可显式触发底层模型装卸策略。

## 7. 边界说明（避免误解）

- 本文档中的能力为“参考与增强方向”，不是“当前仓库已实现能力清单”。
- 当前仓库已实现基础对象模型与 instance-first 执行收口；多模型缓存/LRU/预测加载仍属后续增强。
