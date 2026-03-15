#!/usr/bin/env bash
set -euo pipefail

RUN_DIR="${1:?run dir required}"
CONTROLLER_BASE_URL="${CONTROLLER_BASE_URL:-http://127.0.0.1:8080}"
TOKEN="${CONTROLLER_AUTH_TOKEN:-}"
MODEL_ID="${E5_MODEL_ID:-local-multilingual-e5-base}"
AGENT_ID="${LOCAL_AGENT_ID:-agent-controller-local}"
REPORT_FILE="$RUN_DIR/e5_gating_blocked_report.json"

AUTH_ARGS=()
if [[ -n "$TOKEN" ]]; then
  AUTH_ARGS=(-H "Authorization: Bearer $TOKEN")
fi

step() {
  printf '[%s] [step] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

json_get() {
  local url="$1"
  curl -sS "${AUTH_ARGS[@]}" "$url"
}

json_post() {
  local url="$1"
  local data="$2"
  curl -sS "${AUTH_ARGS[@]}" -H "Content-Type: application/json" -X POST "$url" -d "$data"
}

wait_task_terminal() {
  local task_id="$1"
  local timeout="${2:-45}"
  for _ in $(seq 1 "$timeout"); do
    local task_json
    task_json="$(json_get "$CONTROLLER_BASE_URL/api/v1/tasks/$task_id")"
    local status
    status="$(printf '%s' "$task_json" | jq -r '.data.status // ""')"
    if [[ "$status" == "success" || "$status" == "failed" || "$status" == "timeout" || "$status" == "canceled" ]]; then
      printf '%s' "$task_json"
      return 0
    fi
    sleep 1
  done
  json_get "$CONTROLLER_BASE_URL/api/v1/tasks/$task_id"
}

submit_agent_precheck() {
  local instance_id="$1"
  local req
  req="$(jq -nc \
    --arg agent_id "$AGENT_ID" \
    --arg model_id "$MODEL_ID" \
    --arg instance_id "$instance_id" \
    '{
      agent_id: $agent_id,
      model_id: $model_id,
      runtime_instance_id: $instance_id,
      task_type: "agent.runtime_precheck",
      triggered_by: "e5_gating_blocked_smoke"
    }')"
  local resp
  resp="$(json_post "$CONTROLLER_BASE_URL/api/v1/tasks/agent/node-local" "$req")"
  local task_id
  task_id="$(printf '%s' "$resp" | jq -r '.data.id // ""')"
  if [[ -z "$task_id" ]]; then
    echo "create agent precheck task failed: $resp"
    exit 1
  fi
  wait_task_terminal "$task_id" 45
}

ORIGINAL_BINDING_JSON=""
UPDATED_BINDING_ID=""

restore_binding() {
  if [[ -z "$ORIGINAL_BINDING_JSON" ]]; then
    return 0
  fi
  step "restore runtime binding"
  local restore_payload
  restore_payload="$(printf '%s' "$ORIGINAL_BINDING_JSON" | jq -c '{id, model_id, template_id, binding_mode, node_selector, preferred_node, mount_rules, env_overrides, command_override, script_ref, enabled, manifest_id, metadata}')"
  local resp
  resp="$(json_post "$CONTROLLER_BASE_URL/api/v1/runtime-bindings" "$restore_payload")"
  local ok
  ok="$(printf '%s' "$resp" | jq -r '.success // false')"
  if [[ "$ok" != "true" ]]; then
    echo "restore binding failed: $resp"
  fi
}
trap restore_binding EXIT

step "discover runtime instance and binding"
instance_row="$(json_get "$CONTROLLER_BASE_URL/api/v1/runtime-instances" | jq -c --arg model "$MODEL_ID" '.data[] | select(.model_id==$model)' | head -n1)"
instance_id="$(printf '%s' "$instance_row" | jq -r '.id // ""')"
binding_id="$(printf '%s' "$instance_row" | jq -r '.binding_id // ""')"
if [[ -z "$instance_id" || -z "$binding_id" ]]; then
  echo "cannot find runtime instance/binding for model=$MODEL_ID"
  exit 1
fi

ORIGINAL_BINDING_JSON="$(json_get "$CONTROLLER_BASE_URL/api/v1/runtime-bindings/$binding_id" | jq -c '.data')"
if [[ -z "$ORIGINAL_BINDING_JSON" || "$ORIGINAL_BINDING_JSON" == "null" ]]; then
  echo "cannot read runtime binding detail: $binding_id"
  exit 1
fi

step "set binding to generic_with_script with missing script"
updated_payload="$(printf '%s' "$ORIGINAL_BINDING_JSON" | jq -c '
  {
    id: .id,
    model_id: .model_id,
    template_id: .template_id,
    binding_mode: "generic_with_script",
    node_selector: (.node_selector // {}),
    preferred_node: (.preferred_node // ""),
    mount_rules: (.mount_rules // []),
    env_overrides: (.env_overrides // {}),
    command_override: (.command_override // []),
    script_ref: "/tmp/model-integrator-missing-script.sh",
    enabled: (if .enabled == false then false else true end),
    manifest_id: (.manifest_id // ""),
    metadata: ((.metadata // {}) + {"testsystem":"e5_gating_blocked_smoke"})
  }')"
update_resp="$(json_post "$CONTROLLER_BASE_URL/api/v1/runtime-bindings" "$updated_payload")"
update_ok="$(printf '%s' "$update_resp" | jq -r '.success // false')"
if [[ "$update_ok" != "true" ]]; then
  echo "update runtime binding failed: $update_resp"
  exit 1
fi
UPDATED_BINDING_ID="$binding_id"

step "run agent.runtime_precheck after binding mutation"
precheck_task="$(submit_agent_precheck "$instance_id")"
precheck_status="$(printf '%s' "$precheck_task" | jq -r '.data.status // ""')"
precheck_overall="$(printf '%s' "$precheck_task" | jq -r '.data.detail.overall_status // ""')"

step "verify instance gating/conflict"
instance_summary="$(json_get "$CONTROLLER_BASE_URL/api/v1/runtime-instances/$instance_id/summary?limit=5")"
summary_ok="$(printf '%s' "$instance_summary" | jq -r '.success // false')"
if [[ "$summary_ok" != "true" ]]; then
  echo "runtime instance summary failed: $instance_summary"
  exit 1
fi
summary_precheck_gating="$(printf '%s' "$instance_summary" | jq -r '.data.precheck_gating // false')"
summary_gating_allowed="$(printf '%s' "$instance_summary" | jq -r '.data.gating_allowed // false')"
summary_gating_status="$(printf '%s' "$instance_summary" | jq -r '.data.gating_status // ""')"
summary_conflict_status="$(printf '%s' "$instance_summary" | jq -r '.data.conflict_status // ""')"
summary_plan_action="$(printf '%s' "$instance_summary" | jq -r '.data.last_plan_action // .data.runtime_instance.last_plan_action // ""')"
summary_plan_status="$(printf '%s' "$instance_summary" | jq -r '.data.last_plan_status // .data.runtime_instance.last_plan_status // ""')"
if [[ "$summary_precheck_gating" != "true" && "$summary_gating_allowed" != "false" ]]; then
  echo "expected blocked gating after invalid script binding: $instance_summary"
  exit 1
fi

step "verify runtime.start fail-fast on gating blocked"
start_resp="$(json_post "$CONTROLLER_BASE_URL/api/v1/tasks/runtime/start" "{\"model_id\":\"$MODEL_ID\",\"triggered_by\":\"e5_gating_blocked_smoke\"}")"
start_ok="$(printf '%s' "$start_resp" | jq -r '.success // false')"
start_message="$(printf '%s' "$start_resp" | jq -r '.message // ""')"
if [[ "$start_ok" == "true" ]]; then
  echo "runtime.start should be fail-fast when gating blocked: $start_resp"
  exit 1
fi

cat >"$REPORT_FILE" <<JSON
{
  "scenario": "e5_gating_blocked_smoke",
  "model_id": "$MODEL_ID",
  "runtime_instance_id": "$instance_id",
  "runtime_binding_id": "$binding_id",
  "precheck_task_status": "$precheck_status",
  "precheck_overall_status": "$precheck_overall",
  "summary_precheck_gating": "$summary_precheck_gating",
  "summary_gating_allowed": "$summary_gating_allowed",
  "summary_gating_status": "$summary_gating_status",
  "summary_conflict_status": "$summary_conflict_status",
  "summary_last_plan_action": "$summary_plan_action",
  "summary_last_plan_status": "$summary_plan_status",
  "runtime_start_failfast_message": "$start_message",
  "status": "success",
  "finished_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
JSON

step "scenario success precheck=$precheck_status gating_allowed=$summary_gating_allowed start_failfast=true"
