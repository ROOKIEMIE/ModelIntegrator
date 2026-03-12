# Local LLM Control Plane (Controller)

- [中文（默认）](./README.md)
- [中文镜像](./README.zh-CN.md)
- [English](./README.en.md)

## README Guide & Links

This README is intentionally trimmed to be a quick-start entry.

Detailed documentation has been moved to:

- Full architecture and capability schema: [`doc/Schema.md`](./doc/Schema.md)
- Change log and evolution history: [`doc/LOG.md`](./doc/LOG.md)

Architecture design details, system-shape sections, capability tiers, feature deep-dives, and troubleshooting guidance are now consolidated in `doc/Schema.md`, organized as:

- Front chapters: current architecture design
- Later chapters: implemented/planned capabilities, feature details, and troubleshooting

## Brief Description

Local LLM Control Plane is a local multi-node LLM control plane based on `controller + agent + managed node`, providing unified model/node/runtime/task management.

## Project Positioning

- Built for local/LAN multi-node model operations
- Controller owns unified API, state, scheduling, and orchestration
- Agent executes node-local actions and reports facts back
- Supports Docker/Portainer/LM Studio runtime integrations
- Provides Web console + REST API + SQLite persistence

## Zero-to-Deployment

Target directory example: `/tank/docker_data/model_control_plane`

```bash
sudo mkdir -p /tank/docker_data/model_control_plane
sudo chown -R $USER:$USER /tank/docker_data/model_control_plane
rsync -a --delete /home/whoami/Dev/model-control-plane/ /tank/docker_data/model_control_plane/
cd /tank/docker_data/model_control_plane
```

Initialize:

```bash
cp resources/docker/compose.example.env .env
mkdir -p resources/config resources/models testsystem/logs
touch resources/config/controller.db
chmod 777 resources/config testsystem/logs
chmod 666 resources/config/controller.db
```

Start:

```bash
# Core (recommended)
./scripts/one-click-up.sh

# Core + local agent (recommended for same-host validation)
./scripts/one-click-up.sh --local-agent

# Core + addons
./scripts/one-click-up.sh --addons

# Core + download containers
./scripts/one-click-up.sh --download

# Core + vLLM runtime template container
./scripts/one-click-up.sh --vllm
```

Verify:

```bash
curl -sS http://127.0.0.1:59081/healthz
curl -sS http://127.0.0.1:59081/api/v1/models
curl -sS http://127.0.0.1:59081/api/v1/nodes
```

Stop:

```bash
./scripts/one-click-down.sh
```

## Key Project Files

- Orchestration: `docker-compose.yml`
- Build: `Dockerfile`
- Control-plane config: `resources/config/config.example.yaml`
- Compose env template: `resources/docker/compose.example.env`
- Gateway config: `resources/nginx/nginx.example.conf`
- Frontend: `resources/web/index.html` / `resources/web/app.css` / `resources/web/app.js`
- One-click scripts: `scripts/one-click-up.sh` / `scripts/one-click-down.sh`
- Architecture schema: `doc/Schema.md`
- Change log: `doc/LOG.md`

## Key Config Keys

- `MCP_EXTERNAL_PORT`: external gateway port (default `59081`)
- `MCP_SQLITE_PATH`: SQLite file path
- `MCP_MODEL_DIR_HOST`: host model directory (default `./resources/models`)
- `MCP_MODEL_ROOT_DIR`: in-container model directory (default `/opt/controller/models`)
- `MCP_TEST_LOG_ROOT_HOST`: host test log directory (default `./testsystem/logs`)
- `MCP_TEST_LOG_ROOT_DIR`: in-container test log directory (default `/opt/controller/test-logs`)
- `MCP_LMSTUDIO_ENDPOINT`: LM Studio endpoint
- `MCP_DOCKER_ENDPOINT`: Docker endpoint
- `MCP_CONTAINER_HOST_ALIAS`: in-container host alias (default `host.docker.internal`)
- `MCP_VLLM_EXTERNAL_PORT`: vLLM external port
- `MCP_VLLM_MODEL`: default vLLM model
- `HUGGING_FACE_HUB_TOKEN`: optional token for private/gated models

## API

Base:

- `GET /healthz`
- `GET /api/v1/version`

Nodes and models:

- `GET /api/v1/nodes`
- `GET /api/v1/models`
- `GET /api/v1/models/{id}`
- `POST /api/v1/models/{id}/load`
- `POST /api/v1/models/{id}/unload`
- `POST /api/v1/models/{id}/start`
- `POST /api/v1/models/{id}/stop`

Runtime templates:

- `GET /api/v1/runtime-templates`
- `POST /api/v1/runtime-templates/validate`
- `POST /api/v1/runtime-templates`

Agents and tasks:

- `GET /api/v1/agents`
- `POST /api/v1/agents/register`
- `POST /api/v1/agents/{id}/heartbeat`
- `POST /api/v1/agents/{id}/capabilities`
- `GET /api/v1/agents/{id}/tasks/next`
- `POST /api/v1/agents/{id}/tasks/{taskID}/report`
- `GET /api/v1/tasks`
- `GET /api/v1/tasks/{id}`
- `POST /api/v1/tasks/runtime/start`
- `POST /api/v1/tasks/runtime/stop`
- `POST /api/v1/tasks/runtime/restart`
- `POST /api/v1/tasks/runtime/refresh`
- `POST /api/v1/tasks/agent/runtime-readiness`
- `POST /api/v1/tasks/agent/node-local`

Test runs:

- `GET /api/v1/test-runs`
- `GET /api/v1/test-runs/{id}`
- `POST /api/v1/test-runs`
