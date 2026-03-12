# Local LLM Control Plane (Controller)

- [中文（默认）](./README.md)
- English (current)

Local LLM Control Plane is a local multi-node LLM control plane for Linux server + LAN Mac mini runtime management.

## Architecture Baseline (Phase-1 Revised, 2026-03)

### Terms and Roles (authoritative)

- `controller`: control plane; owns global state, orchestration, scheduling decisions, unified APIs, web console, and persistence.
- `agent`: node execution plane; runs on every managed node and executes node-local actions.
- `managed node`: any managed host in the fleet; each managed node should run an agent.
- `controller node`: the host running controller; it is also a managed node and should run a local agent.
- Primary architecture terms are `controller / agent / managed node` and the system uses these terms end-to-end.

### Responsibility Boundaries

- Controller decides what should happen: desired state, global coordination, conflict handling, reconcile loops.
- Agent decides how to execute locally: runtime precheck/readiness, port checks, path checks, docker inspect, resource snapshots.
- Node-local facts should be collected by agent first; controller consumes reports and performs global decisions.

### Local-Agent Principle

- The controller node also runs an agent.
- The controller-to-local-agent flow should reuse the same protocol path as controller-to-remote-agent.
- This keeps execution paths unified and reduces test/debug complexity.

### Minimal Controller Fallback Self-Checks

- Controller keeps only bootstrap-survival self-checks:
  - config readability
  - SQLite initialization and writeability
  - required directories
  - required listener/bootstrap checks
- These are explicitly `controller self-check / bootstrap fallback` paths, not a replacement for agent execution.

### Testing Strategy

- First stabilize `controller + local agent` (same-host path).
- Then expand to `controller + remote agent` (cross-host path).
- This order reduces variables and speeds up regression/debug cycles.

### Revised Phase Plan

- Phase 1 (current):
  - Strengthen agent node execution plane (`agent.runtime_readiness_check`, `agent.runtime_precheck`, `agent.port_check`, `agent.model_path_check`, `agent.resource_snapshot`, `agent.docker_inspect`).
  - Feed agent results back into node/runtime/model state (including readiness and drift context).
  - Continue desired-vs-observed reconcile loops in controller.
  - Keep precheck/conflict split clear: node-local facts via agent, global decisions via controller.
- Phase 2 (next):
  - Peripheral integration hardening (Nginx / LiteLLM / external embedding client).
  - Richer agent/runtime coordination for remote nodes and expanded runtime coverage.

## Implementation Snapshot

- Go backend + unified REST API + static web console
- Top-level dual-page UI: `Runtime` / `Download`
  - `Runtime`: includes `List` / `Template` sub-tabs
    - `List`: node list + model list
    - `Template`: runtime-template list + validate/register form
  - `Download`: currently an empty reserved page
- Node list and model state are now synchronized:
  - Node cards show runtime count, loaded model count, and runtime status summary
  - Model tabs show loaded model count per node
- Model list supports node tabs, with the first node selected by default
- Docker Compose stack (`nginx` gateway + `controller`, other services via `addons` / `download` / `vllm` profiles)

- LM Studio adapter:
  - model list query prefers `GET /api/v1/models`, with fallback to `GET /v1/models`
  - parses `models[] / data[] / direct array` payload shapes
  - supports field mapping for `key/display_name` and `id/name`
  - syncs `loaded/stopped` state from `loaded_instances`
  - model actions prefer `POST /api/v1/models/load|unload`, with legacy fallback
  - unload prefers `instance_id` when available (for newer LM Studio behavior)
  - validates target model before `load/unload/start/stop`
  - optional in-memory cache + background refresh goroutine

- Model refresh strategy:
  - periodic background refresh after startup
  - one refresh trigger before returning `GET /api/v1/models`

- Local model directory:
  - scans `storage.model_root_dir` and auto-registers local models (`source=local-scan`)

- Node role and connectivity:
  - node roles are normalized to `controller` / `managed`
  - `name` is system-generated; use `description` for human-readable info
  - when agent data is unavailable, controller keeps a minimal runtime/ping fallback probe

- Node hardware info (currently NVIDIA-focused):
  - platform info is filled for nodes with enabled docker runtime
  - local `nvidia-smi` check first; Docker probe fallback if local check fails
  - unsupported hardware remains `unknown`

- Model action mutual exclusion:
  - frontend keeps a per-node action lock
  - backend also enforces a per-node action lock

- Frontend action differences:
  - `backend_type=lmstudio`: only `load/unload` buttons
  - other backends: `load/unload/start/stop`
- Runtime template extensibility:
  - built-in templates: `docker-generic`, `vllm-openai`
  - users can submit custom templates for backend validation and registration (Runtime -> Template)
  - Docker/Portainer model actions validate runtime-template binding before execution

- Docker/Portainer adapters now execute real container orchestration calls (Docker Engine API / Portainer Docker Proxy)
- Download containers available via `download` profile:
  - `hf-downloader`
  - `aria2-downloader`
- vLLM runtime template container via `vllm` profile:
  - `vllm-runtime` (NVIDIA GPU, for future model instance orchestration)
- Download capability currently targets deployments where the controller node has docker runtime
- One-click startup scripts included

## P0 Additions (Current Delivery)

### 1) E5 embedding sample (TEI) end-to-end

- Added `local-multilingual-e5-base` sample in `resources/config/config.example.yaml`.
- Default template binding: `tei-embedding-e5-local` (TEI CPU template, `58001:80`).
- Controller can start/stop/refresh this runtime through task APIs.
- Added minimal embedding client: `scripts/e5_embedding_client.sh`.
- Added reusable smoke scenario: `testsystem/scenarios/e5_embedding_smoke.sh`.

### 2) Runtime actions are task-based

- Added task persistence in SQLite (`tasks` table).
- Runtime task APIs:
  - `POST /api/v1/tasks/runtime/start`
  - `POST /api/v1/tasks/runtime/stop`
  - `POST /api/v1/tasks/runtime/refresh`
  - `GET /api/v1/tasks`
  - `GET /api/v1/tasks/{id}`
- Supported task status:
  `pending/dispatched/running/success/failed/timeout/canceled`.

### 3) desired / observed / readiness model

- Model state now includes:
  - `desired_state`
  - `observed_state`
  - `readiness`
  - `health_message`
  - `last_reconciled_at`
- For the E5 template, readiness is health-probed and can expose
  "container running but service not ready".

### 4) Minimal agent execution plane

- Added controller-agent task protocol and task report path.
- Added agent-side polling execution loop.
- Added agent task APIs:
  - `POST /api/v1/tasks/agent/runtime-readiness`
  - `POST /api/v1/tasks/agent/node-local`
- Phase-1 task types now include:
  - `agent.runtime_readiness_check`
  - `agent.runtime_precheck`
  - `agent.port_check`
  - `agent.model_path_check`
  - `agent.resource_snapshot`
  - `agent.docker_inspect`
- Agent task reports are persisted and fed back into model/node readiness and health context.

### 5) Isolated testing toolchain (`testsystem/`)

- Added:
  - `testsystem/Dockerfile`
  - `testsystem/docker-compose.test.yml`
  - `testsystem/scenarios/e5_embedding_smoke.sh`
  - `testsystem/scripts/run_test.sh`
  - `testsystem/scripts/collect_logs.sh`
- Logs are persisted under host-mounted path by run-id.

### 6) Controller test-run APIs + one-click frontend test

- Added test-run APIs:
  - `POST /api/v1/test-runs` (only predefined scenario `e5_embedding_smoke`)
  - `GET /api/v1/test-runs`
  - `GET /api/v1/test-runs/{id}`
- Frontend button "One-click E5 Test" now calls backend API only (no shell from browser).

## Zero-to-Deployment

Target directory: `/tank/docker_data/model_control_plane`

```bash
sudo mkdir -p /tank/docker_data/model_control_plane
sudo chown -R $USER:$USER /tank/docker_data/model_control_plane
rsync -a --delete /home/whoami/Dev/model-control-plane/ /tank/docker_data/model_control_plane/
cd /tank/docker_data/model_control_plane
```

```bash
cp resources/docker/compose.example.env .env
mkdir -p resources/config resources/models
mkdir -p testsystem/logs
touch resources/config/controller.db
chmod 777 resources/config testsystem/logs
chmod 666 resources/config/controller.db
```

```bash
# Core services
./scripts/one-click-up.sh

# Core + addons
./scripts/one-click-up.sh --addons

# Core + download containers
./scripts/one-click-up.sh --download

# Core + addons + download containers
./scripts/one-click-up.sh --addons --download

# Core + vLLM template runtime
./scripts/one-click-up.sh --vllm

# Core + addons + download containers + vLLM template runtime
./scripts/one-click-up.sh --addons --download --vllm

# Core + controller local agent (recommended for same-host link)
./scripts/one-click-up.sh --local-agent
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

## Key Files

- Orchestration: `docker-compose.yml`
- Build: `Dockerfile`
- Control-plane config: `resources/config/config.example.yaml`
- Compose env example: `resources/docker/compose.example.env`
- Gateway config: `resources/nginx/nginx.example.conf`
- Frontend: `resources/web/index.html` / `app.css` / `app.js`
- One-click scripts: `scripts/one-click-up.sh` / `scripts/one-click-down.sh`
- Changelog: `doc/LOG.md`

## Important Config Keys

- `MCP_EXTERNAL_PORT` (default `59081`)
- `MCP_SQLITE_PATH`
- `MCP_MODEL_DIR_HOST` (host model directory)
- `MCP_MODEL_ROOT_DIR` (container model directory)
- `MCP_TEST_LOG_ROOT_HOST` (host test-log directory, default `./testsystem/logs`)
- `MCP_TEST_LOG_ROOT_DIR` (controller container test-log directory, default `/opt/controller/test-logs`)
- `MCP_LMSTUDIO_ENDPOINT`
- `MCP_LMSTUDIO_CACHE_ENABLED`
- `MCP_LMSTUDIO_CACHE_REFRESH_SECONDS`
- `MCP_DOCKER_ENDPOINT` (docker runtime / GPU fallback probe)
- `MCP_CONTAINER_HOST_ALIAS` (controller-in-container host alias, default `host.docker.internal`)
- `MCP_GPU_PROBE_IMAGE` (optional probe image, default `nvidia/cuda:12.4.1-base-ubuntu22.04`)
- `MCP_HF_CACHE_DIR` (HF download cache directory, download profile)
- `MCP_ARIA2_RPC_SECRET` (aria2 RPC secret, download profile)
- `MCP_ARIA2_RPC_PORT` (aria2 RPC port, download profile)
- `MCP_ARIA2_LISTEN_PORT` (aria2 listen port, download profile)
- `MCP_VLLM_EXTERNAL_PORT` (vLLM external port, vllm profile)
- `MCP_VLLM_MODEL` (default model for vLLM; HF repo id or local path)
- `MCP_VLLM_SERVED_MODEL_NAME` (served model name exposed by vLLM)
- `MCP_VLLM_GPU_MEMORY_UTILIZATION` (vLLM GPU memory utilization cap)
- `MCP_VLLM_MAX_MODEL_LEN` (vLLM max context length)
- `HUGGING_FACE_HUB_TOKEN` (optional token for private/gated HF models)

## Primary Compose vs Test Compose

- Primary system: `docker-compose.yml`
  - Runs controller/nginx and optional addons/download/vllm profiles.
  - Serves production runtime, web console, and APIs.
- Test system: `testsystem/docker-compose.test.yml`
  - Runs only the test runner for predefined test scenarios.
  - Does not serve production traffic.

## External Log Directory Mounts

Controller (main compose) default mount:

```text
${MCP_TEST_LOG_ROOT_HOST:-./testsystem/logs}:${MCP_TEST_LOG_ROOT_DIR:-/opt/controller/test-logs}
```

Test runner (test compose) default mount:

```text
${TEST_LOG_ROOT_HOST:-./testsystem/logs}:/workspace/test-logs
```

Each run writes into `<log-root>/<run-id>/` with `run.log` and `summary.json`.

## One-click Test from Frontend

1. Open Runtime page in web console.
2. Click `One-click E5 Test` in the Task/Test panel.
3. Frontend calls `POST /api/v1/test-runs` with fixed scenario `e5_embedding_smoke`.
4. Check recent run status, summary, and log path in the same panel.

## 2026-03-11 Incident Fixes (Verified)

- Fixed occasional frontend `502` after `one-click-up`:
  - Root cause: legacy `.env` paths + sqlite directory/file permission mismatch.
  - Fix: path migration + sqlite directory/file writable setup in `scripts/one-click-up.sh`.
- Fixed one-click E5 test failures:
  - Root cause 1: test-log mount permission denied.
  - Root cause 2: in-container `127.0.0.1:58001` pointed to controller container, not host runtime.
  - Fix: test-log writable probe + host-gateway mapping + endpoint rewrite for controller-in-container.
- Verified result:
  - `e5_embedding_smoke` succeeds with embedding dimension check (`dim=768`).
  - Logs are persisted under `./testsystem/logs/<test-run-id>/`.

## Troubleshooting

1. Check `summary.json` first (`status/error`).
2. Check `run.log` for failed stage (`runtime_start`, `readiness_poll`, `embedding_request`).
3. For readiness failures, inspect `desired_state/observed_state/readiness/health_message`.
4. For embedding failures, replay with `scripts/e5_embedding_client.sh`.
5. If `/opt/controller/test-logs` shows `permission denied`, verify host path in `MCP_TEST_LOG_ROOT_HOST`.
6. If `127.0.0.1:58001 connect refused` occurs in container mode, verify `MCP_CONTAINER_HOST_ALIAS` and compose `extra_hosts`.

### SQLite Persistence Notes (Control Plane State)

- The control plane persists key state into the SQLite file configured by `MCP_SQLITE_PATH`:
  - agent registration, latest heartbeat, and capability reports
  - node base info and latest aggregated state (classification/tier/source/agent status)
  - model base registration and latest runtime state
  - minimal runtime linkage state (status/capabilities/actions/last seen)
- It is recommended to mount `resources/config/controller.db` as a persistent volume.
- After controller restart, key state is reloaded from SQLite and continues with an in-memory cache + SQLite persistence mode.

## nodes Config Notes (Important)

- Configure `controller.node_id/node_name/node_host` to explicitly define the controller node (which is also a managed node).
- Explicit node IDs are recommended (for example `node-controller`, `node-managed-*`) to avoid cross-env ambiguity.
- If `role` is omitted, the first node is normalized as `controller` and the rest as `managed`.
- Node roles should be declared as `controller/managed` only.
- Use `description` for human-readable node description.

## LM Studio Compatibility Notes (Important)

- Model list: prefer `GET /api/v1/models`, fallback to `GET /v1/models`.
- Load: prefer `POST /api/v1/models/load`.
- Unload: prefer `POST /api/v1/models/unload` and `instance_id` when available.
- If only legacy routes are supported, automatic fallback to `/v1/models/*` is applied.

## Download Feature Notes (Current Stage)

- The Download page is currently an empty reserved page for future download job orchestration UI.
- Download containers are enabled by compose `download` profile.
- `hf-downloader` can fetch Hugging Face model artifacts used by vLLM (for example, safetensors checkpoints).
- `aria2-downloader` is for direct-link artifact downloads.
- This capability is currently intended for deployments where the controller node has docker runtime.
- If non-docker platforms are supported in future, a separate downloader path will be designed.

## vLLM Runtime Notes (New)

- Compose now includes `vllm-runtime` under the `vllm` profile.
- It is currently intended for `controller node + docker + NVIDIA Container Toolkit`.
- It uses `./resources/models` as model storage and reuses the HF cache path.

## Runtime Template Extensibility (New)

- Templates can be provided via config: `runtime_templates: []`.
- Backend validates template schema before registration.
- Locally scanned Docker models are bound to default template `docker-generic` (can be overridden by model metadata `runtime_template_id`).
- Console entry: `Runtime` tab -> `Template` sub-tab.

Endpoints:

- `GET /api/v1/runtime-templates`: list templates (builtin/config/custom).
- `POST /api/v1/runtime-templates/validate`: validate a template payload.
- `POST /api/v1/runtime-templates`: register a template after validation.

Example payload:

```json
{
  "id": "vllm-qwen-7b",
  "name": "vLLM Qwen 7B",
  "runtime_type": "docker",
  "image": "vllm/vllm-openai:latest",
  "command": ["--host", "0.0.0.0", "--port", "8000", "--model", "Qwen/Qwen2.5-7B-Instruct"],
  "volumes": ["./resources/models:/models", "./resources/download-cache/hf:/data/hf-cache"],
  "ports": ["58000:8000"],
  "env": {"HF_HOME": "/data/hf-cache"},
  "needs_gpu": true
}
```

## Confirmed Roadmap

- Docker/Portainer adapter: NVIDIA-first implementation, then expand to other hardware.
- SQLite: key agent/node/model/runtime state persistence is now enabled; migration tooling and action audit tables are next.

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
- `GET /api/v1/runtime-templates`
- `POST /api/v1/runtime-templates/validate`
- `POST /api/v1/runtime-templates`
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
- `GET /api/v1/test-runs`
- `GET /api/v1/test-runs/{id}`
- `POST /api/v1/test-runs`
