#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${1:-http://127.0.0.1:8080}"
TOKEN="${2:-}"
AGENT_ID="${3:-}"
MODEL_ID="${4:-local-multilingual-e5-base}"
AUTH_ARGS=()
if [[ -n "$TOKEN" ]]; then
  AUTH_ARGS=(-H "Authorization: Bearer $TOKEN")
fi

wait_task() {
  local task_id="$1"
  local timeout="${2:-40}"
  local i=0
  while [[ $i -lt $timeout ]]; do
    task_json="$(curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/tasks/$task_id")"
    status="$(printf '%s' "$task_json" | jq -r '.data.status // ""')"
    if [[ "$status" == "success" || "$status" == "failed" || "$status" == "timeout" || "$status" == "canceled" ]]; then
      printf '%s' "$task_json"
      return 0
    fi
    i=$((i + 1))
    sleep 1
  done
  curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/tasks/$task_id"
}

curl -sS "$BASE_URL/healthz" | jq '{ok:.success,version:.data.version}'
curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/nodes" | jq '{ok:.success,count:(.data|length)}'
curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/agents" | jq '{ok:.success,count:(.data|length)}'
curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/models" | jq '{ok:.success,count:(.data|length)}'
curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/tasks?limit=5" | jq '{ok:.success,count:(.data|length)}'
curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/test-runs?limit=5" | jq '{ok:.success,count:(.data|length)}'

if [[ -n "$AGENT_ID" ]]; then
  instance_id="$(curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/runtime-instances" | jq -r --arg model "$MODEL_ID" '.data[] | select(.model_id==$model) | .id' | head -n1)"
  container_id="$(curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/models/$MODEL_ID" | jq -r '.data.metadata.runtime_container_id // ""')"

  precheck_payload="{\"agent_id\":\"$AGENT_ID\",\"model_id\":\"$MODEL_ID\",\"task_type\":\"agent.runtime_precheck\",\"triggered_by\":\"controller_api_smoke\""
  if [[ -n "$instance_id" ]]; then
    precheck_payload+=",\"runtime_instance_id\":\"$instance_id\""
  fi
  precheck_payload+="}"

  precheck_resp="$(curl -sS "${AUTH_ARGS[@]}" -X POST "$BASE_URL/api/v1/tasks/agent/node-local" \
    -H "Content-Type: application/json" \
    -d "$precheck_payload")"
  precheck_task_id="$(printf '%s' "$precheck_resp" | jq -r '.data.id // empty')"
  echo "$precheck_resp" | jq '{ok:.success,task_id:.data.id,type:.data.type,assigned_agent_id:.data.assigned_agent_id}'
  if [[ -n "$precheck_task_id" ]]; then
    wait_task "$precheck_task_id" 45 | jq '{task_id:.data.id,status:.data.status,message:.data.message}'
  fi

  inspect_payload="{\"agent_id\":\"$AGENT_ID\",\"model_id\":\"$MODEL_ID\",\"task_type\":\"agent.docker_inspect\",\"triggered_by\":\"controller_api_smoke\""
  if [[ -n "$instance_id" ]]; then
    inspect_payload+=",\"runtime_instance_id\":\"$instance_id\""
  fi
  if [[ -n "$container_id" ]]; then
    inspect_payload+=",\"payload\":{\"runtime_container_id\":\"$container_id\"}"
  fi
  inspect_payload+="}"

  inspect_resp="$(curl -sS "${AUTH_ARGS[@]}" -X POST "$BASE_URL/api/v1/tasks/agent/node-local" \
    -H "Content-Type: application/json" \
    -d "$inspect_payload")"
  inspect_task_id="$(printf '%s' "$inspect_resp" | jq -r '.data.id // empty')"
  echo "$inspect_resp" | jq '{ok:.success,task_id:.data.id,type:.data.type,assigned_agent_id:.data.assigned_agent_id}'
  if [[ -n "$inspect_task_id" ]]; then
    wait_task "$inspect_task_id" 30 | jq '{task_id:.data.id,status:.data.status,message:.data.message}'
  fi

  snapshot_payload="{\"agent_id\":\"$AGENT_ID\",\"model_id\":\"$MODEL_ID\",\"task_type\":\"agent.resource_snapshot\",\"triggered_by\":\"controller_api_smoke\""
  if [[ -n "$instance_id" ]]; then
    snapshot_payload+=",\"runtime_instance_id\":\"$instance_id\""
  fi
  snapshot_payload+="}"

  curl -sS "${AUTH_ARGS[@]}" -X POST "$BASE_URL/api/v1/tasks/agent/node-local" \
    -H "Content-Type: application/json" \
    -d "$snapshot_payload" \
    | jq '{ok:.success,task_id:.data.id,type:.data.type,assigned_agent_id:.data.assigned_agent_id}'

  if [[ -n "$instance_id" ]]; then
    curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/runtime-instances/$instance_id/summary?limit=5" \
      | jq '{ok:.success,instance:.data.runtime_instance.id,precheck:.data.precheck_status,gating:.data.precheck_gating,readiness:.data.readiness,last_agent_task:.data.last_agent_task.task_type,recent_task_count:(.data.recent_agent_tasks|length)}'
  fi
fi
