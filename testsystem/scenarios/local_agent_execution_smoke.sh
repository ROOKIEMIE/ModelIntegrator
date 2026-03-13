#!/usr/bin/env bash
set -euo pipefail

RUN_DIR="${1:?run dir required}"
CONTROLLER_BASE_URL="${CONTROLLER_BASE_URL:-http://127.0.0.1:8080}"
TOKEN="${CONTROLLER_AUTH_TOKEN:-}"
MODEL_ID="${E5_MODEL_ID:-local-multilingual-e5-base}"
AGENT_ID="${LOCAL_AGENT_ID:-agent-controller-local}"
REPORT_FILE="$RUN_DIR/local_agent_execution_report.json"

AUTH_ARGS=()
if [[ -n "$TOKEN" ]]; then
  AUTH_ARGS=(-H "Authorization: Bearer $TOKEN")
fi

step() {
  printf '[%s] [step] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

json_post() {
  local url="$1"
  local data="$2"
  curl -sS "${AUTH_ARGS[@]}" -H "Content-Type: application/json" -X POST "$url" -d "$data"
}

json_get() {
  local url="$1"
  curl -sS "${AUTH_ARGS[@]}" "$url"
}

wait_task_terminal() {
  local task_id="$1"
  local timeout="${2:-45}"
  for i in $(seq 1 "$timeout"); do
    task_json="$(json_get "$CONTROLLER_BASE_URL/api/v1/tasks/$task_id")"
    status="$(printf '%s' "$task_json" | jq -r '.data.status // ""')"
    if [[ "$status" == "success" || "$status" == "failed" || "$status" == "timeout" || "$status" == "canceled" ]]; then
      printf '%s' "$task_json"
      return 0
    fi
    sleep 1
  done
  json_get "$CONTROLLER_BASE_URL/api/v1/tasks/$task_id"
}

step "discover runtime instance"
instance_id="$(json_get "$CONTROLLER_BASE_URL/api/v1/runtime-instances" | jq -r --arg model "$MODEL_ID" '.data[] | select(.model_id==$model) | .id' | head -n1)"
container_id="$(json_get "$CONTROLLER_BASE_URL/api/v1/models/$MODEL_ID" | jq -r '.data.metadata.runtime_container_id // ""')"

build_payload() {
  local task_type="$1"
  local payload="{\"agent_id\":\"$AGENT_ID\",\"model_id\":\"$MODEL_ID\",\"task_type\":\"$task_type\",\"triggered_by\":\"local_agent_execution_smoke\""
  if [[ -n "$instance_id" ]]; then
    payload+=",\"runtime_instance_id\":\"$instance_id\""
  fi
  if [[ "$task_type" == "agent.docker_inspect" && -n "$container_id" ]]; then
    payload+=",\"payload\":{\"runtime_container_id\":\"$container_id\"}"
  fi
  payload+="}"
  printf '%s' "$payload"
}

submit_and_wait() {
  local task_type="$1"
  local timeout="${2:-45}"
  local req
  req="$(build_payload "$task_type")"
  local resp
  resp="$(json_post "$CONTROLLER_BASE_URL/api/v1/tasks/agent/node-local" "$req")"
  local task_id
  task_id="$(printf '%s' "$resp" | jq -r '.data.id // empty')"
  if [[ -z "$task_id" ]]; then
    echo "create task failed: $resp"
    exit 1
  fi
  local final
  final="$(wait_task_terminal "$task_id" "$timeout")"
  printf '%s' "$final"
}

step "run agent.resource_snapshot"
snapshot_task="$(submit_and_wait "agent.resource_snapshot" 30)"
snapshot_status="$(printf '%s' "$snapshot_task" | jq -r '.data.status // ""')"
snapshot_worker="$(printf '%s' "$snapshot_task" | jq -r '.data.worker_id // .data.assigned_agent_id // ""')"
if [[ "$snapshot_status" != "success" ]]; then
  echo "resource snapshot failed: $snapshot_task"
  exit 1
fi
if [[ -n "$AGENT_ID" && "$snapshot_worker" != "$AGENT_ID" ]]; then
  echo "resource snapshot not executed by expected local-agent: worker=$snapshot_worker expected=$AGENT_ID"
  exit 1
fi

step "run agent.docker_inspect"
inspect_task="$(submit_and_wait "agent.docker_inspect" 30)"
inspect_status="$(printf '%s' "$inspect_task" | jq -r '.data.status // ""')"
inspect_worker="$(printf '%s' "$inspect_task" | jq -r '.data.worker_id // .data.assigned_agent_id // ""')"
if [[ -n "$AGENT_ID" && "$inspect_worker" != "$AGENT_ID" ]]; then
  echo "docker inspect not executed by expected local-agent: worker=$inspect_worker expected=$AGENT_ID"
  exit 1
fi

step "run agent.runtime_precheck"
precheck_task="$(submit_and_wait "agent.runtime_precheck" 45)"
precheck_status="$(printf '%s' "$precheck_task" | jq -r '.data.status // ""')"
precheck_worker="$(printf '%s' "$precheck_task" | jq -r '.data.worker_id // .data.assigned_agent_id // ""')"
if [[ -n "$AGENT_ID" && "$precheck_worker" != "$AGENT_ID" ]]; then
  echo "runtime precheck not executed by expected local-agent: worker=$precheck_worker expected=$AGENT_ID"
  exit 1
fi

instance_summary_status=""
instance_summary_readiness=""
instance_summary_precheck=""
instance_summary_last_task_type=""
if [[ -n "$instance_id" ]]; then
  step "verify runtime instance summary updated"
  instance_summary="$(json_get "$CONTROLLER_BASE_URL/api/v1/runtime-instances/$instance_id/summary?limit=5")"
  summary_instance_id="$(printf '%s' "$instance_summary" | jq -r '.data.runtime_instance.id // ""')"
  instance_summary_status="$(printf '%s' "$instance_summary" | jq -r '.success // false')"
  instance_summary_readiness="$(printf '%s' "$instance_summary" | jq -r '.data.readiness // ""')"
  instance_summary_precheck="$(printf '%s' "$instance_summary" | jq -r '.data.precheck_status // ""')"
  instance_summary_last_task_type="$(printf '%s' "$instance_summary" | jq -r '.data.last_agent_task.task_type // ""')"
  if [[ "$instance_summary_status" != "true" || "$summary_instance_id" != "$instance_id" ]]; then
    echo "runtime instance summary query failed: $instance_summary"
    exit 1
  fi
  if [[ -z "$instance_summary_precheck" || "$instance_summary_precheck" == "unknown" ]]; then
    echo "runtime instance precheck status not updated: $instance_summary"
    exit 1
  fi
  if [[ -z "$instance_summary_last_task_type" ]]; then
    echo "runtime instance last agent task summary missing: $instance_summary"
    exit 1
  fi
fi

cat >"$REPORT_FILE" <<JSON
{
  "scenario": "local_agent_execution_smoke",
  "agent_id": "$AGENT_ID",
  "model_id": "$MODEL_ID",
  "runtime_instance_id": "$instance_id",
  "resource_snapshot_status": "$snapshot_status",
  "docker_inspect_status": "$inspect_status",
  "runtime_precheck_status": "$precheck_status",
  "instance_summary_readiness": "$instance_summary_readiness",
  "instance_summary_precheck_status": "$instance_summary_precheck",
  "instance_summary_last_task_type": "$instance_summary_last_task_type",
  "status": "success",
  "finished_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
JSON

step "scenario success snapshot=$snapshot_status inspect=$inspect_status precheck=$precheck_status"
