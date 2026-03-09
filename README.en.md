# ModelIntegrator (MVP)

- [中文（默认）](./README.md)
- English (current)

ModelIntegrator is a local multi-node LLM control plane for Linux server + LAN Mac mini runtime management.

## Implementation Snapshot

- Go backend + unified REST API + static web console
- Two-column UI: node list on the left, model list on the right
- Node list and model state are now synchronized:
  - Node cards show runtime count, loaded model count, and runtime status summary
  - Model tabs show loaded model count per node (`Main (N)` / `Sub1 (N)`)
- Model list supports node tabs, with the first node selected by default
- Docker Compose stack (`nginx` gateway + `model-integrator`, other services via `addons` profile)

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

- Docker/Portainer adapters are still placeholders
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
cp resource/docker/compose.example.env .env
mkdir -p resource/config resource/models
touch resource/config/modelintegrator.db
chmod 666 resource/config/modelintegrator.db
```

```bash
# Core services
./scripts/one-click-up.sh

# Core + addons
./scripts/one-click-up.sh --addons
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
- Control-plane config: `resource/config/config.example.yaml`
- Compose env example: `resource/docker/compose.example.env`
- Gateway config: `resource/nginx/nginx.example.conf`
- Frontend: `resource/web/index.html` / `app.css` / `app.js`
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
