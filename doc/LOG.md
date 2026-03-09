# 变更日志

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
  - `model-integrator`
  - 其中 `portainer/nginx-ui/litellm/openwebui` 归入 `addons` profile，按需启动

- 网络与端口策略：
  - 所有服务同一网络 `mcp_net`
  - 仅 `nginx` 暴露宿主端口
  - 默认外部端口修改为 `59081`

- 路径策略：
  - 所有挂载路径基于 `docker-compose.yml` 所在目录
  - 通过 `MCP_PROJECT_DIR` 统一路径前缀

- Nginx 网关配置更新（`resources/nginx/nginx.example.conf`）
  - `/` -> `model-integrator`
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

- 节点平台信息填充增强（`src/cmd/model-integrator/main.go`）
  - 对所有启用 `docker` runtime 的节点填充平台信息（不再仅限主节点）
  - Docker 探测 endpoint 优先取 `nodes[].runtimes[]` 中启用的 docker endpoint

- 容器运行权限调整（`Dockerfile`）
  - 移除 `USER app`，默认以 root 运行，确保容器内可访问 `/var/run/docker.sock` 进行 Docker 探测

### v0.2-prep Web 控制台状态同步与交互优化

- 节点列表与模型状态联动（`resources/web/app.js`）
  - 节点卡片新增“已装载模型数”统计（按节点实时计算）
  - 节点卡片新增 Runtime 状态摘要（按 backend 聚合 loaded 数）
  - 节点标签页新增已装载计数展示（`Main (N)` / `Sub1 (N)`）

- 模型动作按钮行为优化（`resources/web/app.js`）
  - `backend_type=lmstudio` 的模型仅展示 `load/unload`
  - 其他后端保持 `load/unload/start/stop`
  - 动作后同步刷新节点列表、标签页和模型列表，保持统计一致
  - 前端保留节点级动作锁；后端也已补充节点级并发动作锁，避免绕过前端并发提交

- 节点展示信息微调
  - 节点卡片移除角色显示（`main/sub`），保留名称与描述区分

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
    - 下载功能仅面向“主节点且具备 docker 能力”的部署场景。
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
  - 当前定位：面向“主节点 + docker + NVIDIA”场景的运行模板，为后续按模型实例化编排做准备。

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
  - `go build ./src/cmd/model-integrator` 通过
  - `docker compose config`（含 `download` / `vllm` profile）通过
  - `bash -n scripts/one-click-up.sh scripts/one-click-down.sh` 通过
