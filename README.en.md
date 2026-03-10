# ModelIntegrator (MVP)

- [中文（默认）](./README.md)
- English (current)

ModelIntegrator is a local multi-node LLM control plane for Linux server + LAN Mac mini runtime management.

## Implementation Snapshot

- Go backend + unified REST API + static web console
- Top-level dual-page UI: `Runtime` / `Download`
  - `Runtime`: includes `List` / `Template` sub-tabs
    - `List`: node list + model list
    - `Template`: runtime-template list + validate/register form
  - `Download`: currently an empty reserved page
- Node list and model state are now synchronized:
  - Node cards show runtime count, loaded model count, and runtime status summary
  - Model tabs show loaded model count per node (`Main (N)` / `Sub1 (N)`)
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
  - first node is auto-assigned `Main`, following nodes become `Sub1/Sub2...`
  - `name` is system-generated; use `description` for human-readable info
  - sub-nodes are ICMP-checked before returning `online`

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
- Download capability currently targets deployments where the main node has docker runtime
- One-click startup scripts included

## Zero-to-Deployment

Target directory: `/tank/docker_data/model_intergrator`

```bash
sudo mkdir -p /tank/docker_data/model_intergrator
sudo chown -R $USER:$USER /tank/docker_data/model_intergrator
rsync -a --delete /home/whoami/Dev/ModelIntegrator/ /tank/docker_data/model_intergrator/
cd /tank/docker_data/model_intergrator
```

```bash
cp resources/docker/compose.example.env .env
mkdir -p resources/config resources/models
touch resources/config/modelintegrator.db
chmod 666 resources/config/modelintegrator.db
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
- `MCP_LMSTUDIO_ENDPOINT`
- `MCP_LMSTUDIO_CACHE_ENABLED`
- `MCP_LMSTUDIO_CACHE_REFRESH_SECONDS`
- `MCP_DOCKER_ENDPOINT` (docker runtime / GPU fallback probe)
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

## nodes Config Notes (Important)

- Manual `id` is no longer required in `nodes`.
- The first node is automatically `Main` (`id=node-main`).
- Nodes after that are `Sub1/Sub2...` (`id=node-sub-1/node-sub-2...`).
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
- This capability is currently intended for deployments where the main node has docker runtime.
- If non-docker platforms are supported in future, a separate downloader path will be designed.

## vLLM Runtime Notes (New)

- Compose now includes `vllm-runtime` under the `vllm` profile.
- It is intended for `main node + docker + NVIDIA Container Toolkit`.
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
- SQLite: persistent model/node read-write will be added in the next phase (currently path/file prep only).

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
