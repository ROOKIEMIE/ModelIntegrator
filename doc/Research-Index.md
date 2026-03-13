# Research Index：分层研究与增强路线索引

本文档用于维护“外部成熟成果 -> 当前项目对象 -> 后续阶段”的统一入口，避免研究材料散落在单次日志中。

## 1. 分层文档导航

- Layer 1（模型服务 / 动态装卸 / 多模型 serving 原语）  
  [`./Research-L1-Serving-and-Dynamic-Loading.md`](./Research-L1-Serving-and-Dynamic-Loading.md)
- Layer 2（资源调度 / 冷启动 / GPU 复用 / 多模型并发 serving）  
  [`./Research-L2-Scheduling-ColdStart-and-GPU-Pooling.md`](./Research-L2-Scheduling-ColdStart-and-GPU-Pooling.md)
- Layer 3（应用层路由 / 级联 / 多模型协作）  
  [`./Research-L3-Routing-Cascade-and-MultiModel-Orchestration.md`](./Research-L3-Routing-Cascade-and-MultiModel-Orchestration.md)
- 增强路线图（A/B/C/D/E）  
  [`./Enhancement-Roadmap.md`](./Enhancement-Roadmap.md)

## 2. 三层与项目对象映射（总览）

| 增强层 | 主要参考材料 | 当前项目主要对象 |
| --- | --- | --- |
| Layer 1 | Triton / ModelMesh / Ray Serve / NVIDIA Dynamo | `RuntimeTemplate` / `RuntimeBinding` / `RuntimeInstance` / `RuntimeBundleManifest` / `agent` |
| Layer 2 | Clipper / INFaaS / MuxServe / ServerlessLLM / Aegaeon | `controller`（reconcile/precheck/conflict/load-unload planner）/ `agent` / `RuntimeInstance` / `testsystem` |
| Layer 3 | FrugalGPT / RouteLLM / MoA | 未来 `application planner / workflow orchestrator`（建立在 `controller + agent + runtime instance` 之上） |

## 3. 文档维护规则

- Layer 文档只记录“可借鉴机制、不可直接照搬点、对象映射、阶段化增强建议”。
- `doc/Schema.md` 负责系统级定位与阶段对齐，不堆叠细节研究条目。
- `doc/Enhancement-Roadmap.md` 负责路线编排与阶段目标，变更时同步更新 `doc/LOG.md`。
- Layer 3 内容默认视为长期方向，不得写成“当前已实现”。
