# 变更日志

## 2026-03-09

### v0.2-prep 基础设施改造

- 新增 GPU/CUDA/Driver 启动前预检查（`src/pkg/preflight/gpu.go`）
  - 若检测到 `nvidia-smi`，输出 Driver/CUDA 版本
  - 非 CUDA 平台或检测失败时输出 warning，不阻断启动

- 新增 SQLite 路径配置与启动预备（`src/pkg/storage/sqlite.go`）
  - 默认路径：`./resource/config/modelintegrator.db`
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

- Nginx 网关配置更新（`resource/nginx/nginx.example.conf`）
  - `/` -> `model-integrator`
  - `/openwebui/` -> `openwebui`
  - `/litellm/` -> `litellm`

- 文档更新：
  - `README.zh-CN.md`
  - `resource/docker/compose.example.env`
  - `resource/config/config.example.yaml`

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

- 节点列表与模型状态联动（`resource/web/app.js`）
  - 节点卡片新增“已装载模型数”统计（按节点实时计算）
  - 节点卡片新增 Runtime 状态摘要（按 backend 聚合 loaded 数）
  - 节点标签页新增已装载计数展示（`Main (N)` / `Sub1 (N)`）

- 模型动作按钮行为优化（`resource/web/app.js`）
  - `backend_type=lmstudio` 的模型仅展示 `load/unload`
  - 其他后端保持 `load/unload/start/stop`
  - 动作后同步刷新节点列表、标签页和模型列表，保持统计一致
  - 前端保留节点级动作锁；后端也已补充节点级并发动作锁，避免绕过前端并发提交

- 节点展示信息微调
  - 节点卡片移除角色显示（`main/sub`），保留名称与描述区分
