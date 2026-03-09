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
