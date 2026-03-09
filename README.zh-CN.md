# ModelIntegrator（MVP）

ModelIntegrator 是一个本地多节点 LLM 控制平面，用于统一管理 Linux 服务器与局域网 Mac mini 上的模型运行时。

当前版本聚焦可运行 MVP：
- 控制平面后端（Go）
- 统一 REST API
- 静态 Web 占位控制台
- Docker Compose 一键启动骨架（含 Portainer / Nginx / Nginx-UI / LiteLLM / OpenWebUI）

## 已实现能力

- Go 项目结构：`go.mod` + `src/` + `resource/`
- HTTP 服务：`net/http` + `chi`
- 配置：YAML + 环境变量覆盖
- 日志：`slog`
- 领域对象：`Node` / `Runtime` / `Model` / `ActionResult`
- 注册表：内存 Node/Model Registry
- 服务层：NodeService / ModelService
- 调度器骨架：互斥组、优先级、自动回收入口预留
- 适配器：
  - LM Studio（基础 HTTP 操作）
  - Docker/Portainer（placeholder，已预留真实接入点）
- 启动前检查：GPU/CUDA/Driver 预检查（非 CUDA 平台会给出 warning）
- SQLite 路径预备：默认 `resource/config/modelintegrator.db`（可环境变量覆盖）

## 目录结构

```text
.
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
├── README.zh-CN.md
├── src
│   ├── cmd
│   │   └── model-integrator
│   │       └── main.go
│   └── pkg
│       ├── adapter
│       ├── api
│       ├── config
│       ├── health
│       ├── logger
│       ├── model
│       ├── preflight
│       ├── registry
│       ├── scheduler
│       ├── server
│       ├── service
│       ├── storage
│       └── version
└── resource
    ├── config
    │   └── config.example.yaml
    ├── docker
    │   └── compose.example.env
    ├── litellm
    │   └── config.yaml
    ├── nginx
    │   └── nginx.example.conf
    ├── nginx-ui
    ├── openwebui
    ├── portainer
    └── web
```

## API

- `GET /healthz`
- `GET /api/v1/version`
- `GET /api/v1/nodes`
- `GET /api/v1/models`
- `GET /api/v1/models/{id}`
- `POST /api/v1/models/{id}/load`
- `POST /api/v1/models/{id}/unload`
- `POST /api/v1/models/{id}/start`
- `POST /api/v1/models/{id}/stop`

统一返回格式：`success` / `message` / `data` / `error` / `timestamp`

## 本地 Go 运行

```bash
go mod tidy
go build ./src/cmd/model-integrator
go run ./src/cmd/model-integrator
```

访问：
- `http://localhost:8080/healthz`
- `http://localhost:8080/api/v1/models`
- `http://localhost:8080/`

## Docker Compose 运行

默认以 `docker-compose.yml` 所在目录作为项目根目录，所有挂载路径均基于此目录。

1. 可选：拷贝示例环境文件

```bash
cp resource/docker/compose.example.env .env
```

2. 启动

```bash
docker compose up -d --build
```

如需一并启动 Portainer / Nginx-UI / LiteLLM / OpenWebUI：

```bash
docker compose --profile addons up -d --build
```

3. 访问统一外部入口

- `http://localhost:59081/`（默认网关端口，可由 `MCP_EXTERNAL_PORT` 覆盖）

## 网络与端口策略

- 所有组件共享单一网络：`mcp_net`
- 仅 `nginx` 暴露宿主端口（默认 `59081`）
- `model-integrator` / `portainer` / `nginx-ui` / `litellm` / `openwebui` 均不直接映射宿主机端口
- 通过网关转发内部服务，避免直接暴露内部组件

## 配置说明

默认配置文件：`./resource/config/config.example.yaml`

关键环境变量：
- `MCP_PROJECT_DIR`：Compose 根目录（默认 `.`）
- `MCP_EXTERNAL_PORT`：外部网关端口（默认 `59081`）
- `MCP_CONFIG`：控制平面配置路径
- `MCP_SQLITE_PATH`：SQLite 文件路径
- `MCP_LMSTUDIO_ENDPOINT`
- `MCP_DOCKER_ENDPOINT`
- `MCP_PORTAINER_ENDPOINT`

## 当前未完成项（占位）

- Docker/Portainer 适配器尚未接入真实 API（目前返回明确 placeholder 消息）
- LM Studio 适配器已完成基础对接，具体端点字段可能需按目标版本微调

## 下一阶段建议

- 真实接入 Docker Engine / Portainer API
- 引入 SQLite 持久化（Node/Model/Action）
- 增加异步任务队列与动作状态追踪
- 增加鉴权、审计日志和最小 RBAC
