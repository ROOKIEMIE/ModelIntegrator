# 独立测试工具链（testsystem）

本目录用于维护与主系统隔离的测试运行环境，不直接修改主 compose。

## 目录

- `Dockerfile`：测试 runner 镜像（bash/curl/jq）
- `docker-compose.test.yml`：测试专用 compose
- `scenarios/stage0_runtime_object_smoke.sh`：阶段 0 对象链路 smoke（Model→Template→Binding→Instance→Manifest）
- `scenarios/e5_embedding_smoke.sh`：E5 embedding 端到端 smoke 场景
- `scenarios/local_agent_execution_smoke.sh`：阶段 A 收口 + 阶段 B 起步 smoke（E5 默认样板，覆盖 instance-first precheck、结构化结果、RuntimeInstance/API 可见性、reconcile-summary 校验）
- `scenarios/e5_gating_blocked_smoke.sh`：阶段 B 深化 smoke（通过 binding/script 冲突触发 precheck+gating 阻塞，并验证 runtime.start fail-fast）
- `scenarios/stage0_to_b_full_smoke.sh`：阶段 0~B 串联 smoke（按序执行 stage0/local-agent/e5-embedding/gating-blocked）
- `scripts/run_test.sh`：统一入口，创建 run id 与日志目录
- `scripts/collect_logs.sh`：收集日志清单
- `logs/`：默认日志目录（可挂载为宿主机目录）

## 环境变量

- `CONTROLLER_BASE_URL`：controller API 地址，默认 `http://controller:8080`
- `CONTROLLER_AUTH_TOKEN`：controller Bearer token（可空）
- `TEST_SCENARIO`：场景名，当前允许：
  - `stage0_to_b_full_smoke`（推荐）
  - `stage0_runtime_object_smoke`
  - `e5_embedding_smoke`
  - `local_agent_execution_smoke`
  - `e5_gating_blocked_smoke`
- `TEST_LOG_ROOT_HOST`：宿主机日志目录，默认 `./testsystem/logs`
- `E5_MODEL_ID`：E5 模型 id，默认 `local-multilingual-e5-base`
- `E5_EXPECTED_DIM`：embedding 维度，默认 `768`
- `MCP_CONTAINER_HOST_ALIAS`：controller 在容器内访问宿主机 runtime 端口的别名（默认 `host.docker.internal`）

## 运行方式

```bash
docker compose -f testsystem/docker-compose.test.yml up --build --abort-on-container-exit
```

日志会写入挂载目录 `TEST_LOG_ROOT_HOST/<run-id>/`，包含：

- `run.log`
- `summary.json`
- `e5_embedding_report.json`（场景成功时）

## 常见故障排查

- `mkdir /opt/controller/test-logs/...: permission denied`
  - 检查 `.env` 中 `MCP_TEST_LOG_ROOT_HOST` 指向目录权限。
  - 重新执行 `./scripts/one-click-up.sh`，脚本会自动创建并探测目录可写性。
- `dial tcp 127.0.0.1:58001: connect: connection refused`
  - 多见于 controller 运行在容器内时误把 `127.0.0.1` 当成宿主机。
  - 检查 `docker-compose.yml` 是否包含：
    - `extra_hosts: ["${MCP_CONTAINER_HOST_ALIAS:-host.docker.internal}:host-gateway"]`
    - `MCP_CONTAINER_HOST_ALIAS` 环境变量。
